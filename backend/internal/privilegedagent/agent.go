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
	connectionTimeout = 20 * time.Second
)

// Request is the only message accepted by the privileged agent.
type Request struct {
	Interface       string                            `json:"interface,omitempty"`
	Operation       string                            `json:"operation"`
	StaticWANConfig *networkinventory.StaticWANConfig `json:"static_wan_config,omitempty"`
	PPPoEConfig     *networkinventory.PPPoEConfig     `json:"pppoe_config,omitempty"`
	TransactionID   string                            `json:"transaction_id,omitempty"`
}

// Response contains harmless status data or a stable error code.
type Response struct {
	DHCPStatus           *networkinventory.DHCPStatus           `json:"dhcp_status,omitempty"`
	StaticWANTransaction *networkinventory.StaticWANTransaction `json:"static_wan_transaction,omitempty"`
	PPPoETransaction     *networkinventory.PPPoETransaction     `json:"pppoe_transaction,omitempty"`
	PPPoEStatus          *networkinventory.PPPoEStatus          `json:"pppoe_status,omitempty"`
	Status               string                                 `json:"status,omitempty"`
	Agent                string                                 `json:"agent,omitempty"`
	Error                string                                 `json:"error,omitempty"`
}

type staticWANOperations interface {
	Apply(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error)
	Confirm(context.Context, string) error
	Rollback(context.Context, string) error
}

type pppoeOperations interface {
	Apply(context.Context, networkinventory.PPPoEConfig) (networkinventory.PPPoETransaction, error)
	Confirm(context.Context, string) error
	Rollback(context.Context, string) error
	Reconnect(context.Context) error
	Status() (networkinventory.PPPoEStatus, error)
}

// DefaultSocketPath returns the systemd-managed agent socket path.
func DefaultSocketPath() string {
	return defaultSocketPath
}

// Serve accepts bounded requests from peers whose primary GID is allowedGID.
func Serve(ctx context.Context, socketPath string, allowedGID uint32) error {
	manager, err := networkinventory.NewStaticWANManager()
	if err != nil {
		return err
	}
	if err := manager.RecoverPending(ctx); err != nil {
		return fmt.Errorf("recover pending WAN configuration: %w", err)
	}
	pppoe, err := networkinventory.NewPPPoEManager()
	if err != nil {
		return err
	}
	if err := pppoe.RecoverPending(ctx); err != nil {
		return fmt.Errorf("recover pending PPPoE configuration: %w", err)
	}
	return serveWithAllOperations(ctx, socketPath, allowedGID, networkinventory.ReadDHCPStatus, manager, pppoe)
}

func serveWithDHCPReader(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) error {
	return serveWithOperations(ctx, socketPath, allowedGID, readDHCPStatus, nil)
}

func serveWithOperations(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
) error {
	return serveWithAllOperations(ctx, socketPath, allowedGID, readDHCPStatus, staticWAN, nil)
}

func serveWithAllOperations(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
	pppoe pppoeOperations,
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
		handleConnectionWithAllOperations(connection, allowedGID, readDHCPStatus, staticWAN, pppoe)
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

// SendApplyStaticWAN sends a fully validated static profile to the privileged agent.
func SendApplyStaticWAN(ctx context.Context, socketPath string, config networkinventory.StaticWANConfig) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-apply", Interface: networkinventory.StaticWANInterface, StaticWANConfig: &config})
}

// SendConfirmStaticWAN commits a verified static WAN transaction.
func SendConfirmStaticWAN(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-confirm", TransactionID: transactionID})
}

// SendRollbackStaticWAN restores the previous WAN configuration immediately.
func SendRollbackStaticWAN(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-rollback", TransactionID: transactionID})
}

func SendApplyPPPoE(ctx context.Context, socketPath string, config networkinventory.PPPoEConfig) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-apply", Interface: networkinventory.StaticWANInterface, PPPoEConfig: &config})
}

func SendPPPoEStatus(ctx context.Context, socketPath string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-status"})
}

func SendConfirmPPPoE(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-confirm", TransactionID: transactionID})
}

func SendRollbackPPPoE(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-rollback", TransactionID: transactionID})
}

func SendReconnectPPPoE(ctx context.Context, socketPath string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-reconnect"})
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
	handleConnectionWithOperations(connection, allowedGID, readDHCPStatus, nil)
}

func handleConnectionWithOperations(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
) {
	handleConnectionWithAllOperations(connection, allowedGID, readDHCPStatus, staticWAN, nil)
}

func handleConnectionWithAllOperations(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
	pppoe pppoeOperations,
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
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		writeResponse(connection, Response{Status: "ok", Agent: "ready"})
	case "dhcp-status":
		if !networkinventory.ValidInterfaceName(request.Interface) || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		status, err := readDHCPStatus(request.Interface)
		if err != nil {
			writeResponse(connection, Response{Error: "network_status_unavailable"})
			return
		}
		writeResponse(connection, Response{DHCPStatus: &status})
	case "static-wan-apply":
		if request.Interface != networkinventory.StaticWANInterface || request.StaticWANConfig == nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if err := networkinventory.ValidateStaticWAN(*request.StaticWANConfig); err != nil {
			writeResponse(connection, Response{Error: "invalid_static_config"})
			return
		}
		if staticWAN == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		transaction, err := staticWAN.Apply(ctx, *request.StaticWANConfig)
		if err != nil {
			writeResponse(connection, Response{Error: "static_wan_apply_failed"})
			return
		}
		writeResponse(connection, Response{StaticWANTransaction: &transaction})
	case "static-wan-confirm", "static-wan-rollback":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || !validTransactionID(request.TransactionID) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if staticWAN == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		var err error
		result := "confirmed"
		if request.Operation == "static-wan-confirm" {
			err = staticWAN.Confirm(ctx, request.TransactionID)
		} else {
			result = "rolled_back"
			err = staticWAN.Rollback(ctx, request.TransactionID)
		}
		if err != nil {
			writeResponse(connection, Response{Error: "static_wan_transaction_failed"})
			return
		}
		writeResponse(connection, Response{Status: result})
	case "pppoe-status":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		status, err := pppoe.Status()
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_status_unavailable"})
			return
		}
		writeResponse(connection, Response{PPPoEStatus: &status})
	case "pppoe-apply":
		if request.Interface != networkinventory.StaticWANInterface || request.PPPoEConfig == nil || request.StaticWANConfig != nil || request.TransactionID != "" || networkinventory.ValidatePPPoEConfig(*request.PPPoEConfig) != nil {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		transaction, err := pppoe.Apply(ctx, *request.PPPoEConfig)
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_apply_failed"})
			return
		}
		writeResponse(connection, Response{PPPoETransaction: &transaction})
	case "pppoe-confirm", "pppoe-rollback":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || !validTransactionID(request.TransactionID) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		result := "rolled_back"
		var err error
		if request.Operation == "pppoe-confirm" {
			result = "confirmed"
			err = pppoe.Confirm(ctx, request.TransactionID)
		} else {
			err = pppoe.Rollback(ctx, request.TransactionID)
		}
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_transaction_failed"})
			return
		}
		writeResponse(connection, Response{Status: result})
	case "pppoe-reconnect":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		if err := pppoe.Reconnect(ctx); err != nil {
			writeResponse(connection, Response{Error: "pppoe_reconnect_failed"})
			return
		}
		writeResponse(connection, Response{Status: "reconnecting"})
	default:
		writeResponse(connection, Response{Error: "unsupported_operation"})
	}
}

func validTransactionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, value := range id {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
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
