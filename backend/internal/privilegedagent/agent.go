package privilegedagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

const (
	defaultSocketPath = "/run/debian-router-agent/agent.sock"
	maxRequestBytes   = 1024
	maxResponseBytes  = 1024
	connectionTimeout = 2 * time.Second
)

// Request is the only message accepted by the privileged agent.
type Request struct {
	Interface string
	Operation string `json:"operation"`
}

// Response contains either the harmless status result or a stable error code.
type Response struct {
	DHCPStatus *networkinventory.DHCPStatus
	Status string `json:"status,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Error  string `json:"error,omitempty"`
}

// DefaultSocketPath returns the systemd-managed agent socket path.
func DefaultSocketPath() string {
	return defaultSocketPath
}

// Serve accepts bounded requests from peers whose primary GID is allowedGID.
func Serve(ctx context.Context, socketPath string, allowedGID uint32) error {
	return serveWithDHCPReader(ctx, socketPath, allowedGID, networkinventory.ReadDHCPStatus)
}

func serveWithDHCPReader(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) error {
	if socketPath == "" {
		return errors.New("agent socket path is empty")
	}
	if ctx.Err() != nil {
		return nil
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on privileged agent socket: %w", err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		listener.Close()
		return errors.New("privileged agent requires a Unix stream socket")
	}
	unixListener.SetUnlinkOnClose(true)
	if err := os.Chmod(socketPath, 0660); err != nil {
		listener.Close()
		return fmt.Errorf("set privileged agent socket mode: %w", err)
	}
	defer listener.Close()
	serveDone := make(chan struct{})
	defer close(serveDone)

	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-serveDone:
		}
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept privileged agent connection: %w", err)
		}
		handleConnection(connection, allowedGID, readDHCPStatus)
	}
}

// SendRequest connects to the agent and sends one operation request.
func SendRequest(ctx context.Context, socketPath, operation string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: operation})
}

// SendDHCPStatus requests a filtered, read-only DHCP view for one interface.
func SendDHCPStatus(ctx context.Context, socketPath, interfaceName string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{
		Operation: "dhcp-status",
		Interface: interfaceName,
	})
}

func sendRequest(ctx context.Context, socketPath string, request Request) (Response, error) {
	var empty Response
	connection, err := dialAgent(ctx, socketPath)
	if err != nil {
		return empty, fmt.Errorf("connect to privileged agent: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(connectionTimeout)); err != nil {
		return empty, fmt.Errorf("set privileged agent deadline: %w", err)
	}

	encoder := json.NewEncoder(connection)
	if err := encoder.Encode(request); err != nil {
		return empty, fmt.Errorf("send privileged agent request: %w", err)
	}
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		if err := unixConnection.CloseWrite(); err != nil {
			return empty, fmt.Errorf("finish privileged agent request: %w", err)
		}
	}

	decoder := json.NewDecoder(io.LimitReader(connection, maxResponseBytes))
	decoder.DisallowUnknownFields()
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return empty, fmt.Errorf("read privileged agent response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return empty, errors.New("privileged agent returned multiple responses")
		}
		return empty, fmt.Errorf("read privileged agent response trailer: %w", err)
	}
	return response, nil
}

func dialAgent(ctx context.Context, socketPath string) (net.Conn, error) {
	deadline := time.Now().Add(connectionTimeout)
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	var lastErr error
	for {
		connection, err := dialer.DialContext(ctx, "unix", socketPath)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return nil, lastErr
		}
		if wait > 25*time.Millisecond {
			wait = 25 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func handleConnection(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(connectionTimeout))

	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		writeResponse(connection, Response{Error: "unauthorized"})
		return
	}
	peerGID, err := unixPeerGID(unixConnection)
	if err != nil || peerGID != allowedGID {
		writeResponse(connection, Response{Error: "unauthorized"})
		return
	}

	request, err := decodeRequest(connection)
	if err != nil {
		writeResponse(connection, Response{Error: "invalid_request"})
		return
	}
	switch request.Operation {
	case "status":
		if request.Interface != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		writeResponse(connection, Response{Status: "ok", Agent: "ready"})
	case "dhcp-status":
		if !networkinventory.ValidInterfaceName(request.Interface) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		status, err := readDHCPStatus(request.Interface)
		if err != nil {
			writeResponse(connection, Response{Error: "network_status_unavailable"})
			return
		}
		writeResponse(connection, Response{DHCPStatus: &status})
	default:
		writeResponse(connection, Response{Error: "unsupported_operation"})
	}
}

func decodeRequest(reader io.Reader) (Request, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxRequestBytes || payload[len(payload)-1] != '\n' {
		return Request{}, errors.New("request is incomplete or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Request{}, errors.New("request contains multiple JSON values")
		}
		return Request{}, err
	}
	if request.Operation == "" {
		return Request{}, errors.New("request operation is empty")
	}
	return request, nil
}

func writeResponse(writer io.Writer, response Response) {
	_ = json.NewEncoder(writer).Encode(response)
}
