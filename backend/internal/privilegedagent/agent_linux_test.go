//go:build linux

package privilegedagent

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAllowedStatusRequest(t *testing.T) {
	socketPath, stop := startTestServer(t, uint32(syscall.Getgid()))
	defer stop()

	response, err := SendRequest(context.Background(), socketPath, "status")
	if err != nil {
		t.Fatal(err)
	}
	if response != (Response{Status: "ok", Agent: "ready"}) {
		t.Fatalf("status response = %#v", response)
	}
}

func TestClientRetriesUntilSocketAppears(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	responseDone := make(chan struct {
		response Response
		err      error
	}, 1)
	go func() {
		response, err := SendRequest(context.Background(), socketPath, "status")
		responseDone <- struct {
			response Response
			err      error
		}{response: response, err: err}
	}()

	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Serve(ctx, socketPath, uint32(syscall.Getgid()))
	}()
	defer func() {
		cancel()
		if err := <-serverDone; err != nil {
			t.Errorf("stop agent server: %v", err)
		}
	}()

	select {
	case result := <-responseDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.response != (Response{Status: "ok", Agent: "ready"}) {
			t.Fatalf("status response = %#v", result.response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete after the agent socket appeared")
	}
}

func TestUnknownOperationIsRejected(t *testing.T) {
	socketPath, stop := startTestServer(t, uint32(syscall.Getgid()))
	defer stop()

	response, err := SendRequest(context.Background(), socketPath, "shell")
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != "unsupported_operation" {
		t.Fatalf("unknown operation response = %#v", response)
	}
}

func TestWrongPeerGIDIsRejected(t *testing.T) {
	allowedGID := uint32(syscall.Getgid()) + 1
	if allowedGID == ^uint32(0) {
		allowedGID -= 2
	}
	socketPath, stop := startTestServer(t, allowedGID)
	defer stop()

	response, err := SendRequest(context.Background(), socketPath, "status")
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != "unauthorized" {
		t.Fatalf("wrong GID response = %#v", response)
	}
}

func TestMalformedAndOversizedRequestsAreRejected(t *testing.T) {
	socketPath, stop := startTestServer(t, uint32(syscall.Getgid()))
	defer stop()

	for name, request := range map[string]string{
		"malformed":       "{not-json}\n",
		"oversized":       strings.Repeat("x", maxRequestBytes+1) + "\n",
		"extra field":     "{\"operation\":\"status\",\"command\":\"id\"}\n",
		"multiple values": "{\"operation\":\"status\"}\n{\"operation\":\"status\"}\n",
		"missing newline": "{\"operation\":\"status\"}",
	} {
		t.Run(name, func(t *testing.T) {
			response := sendRawRequest(t, socketPath, request)
			if response.Error != "invalid_request" {
				t.Fatalf("invalid request response = %#v", response)
			}
		})
	}
}

func TestServerRemovesSocketOnShutdown(t *testing.T) {
	socketPath, stop := startTestServer(t, uint32(syscall.Getgid()))
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("socket does not exist while server is running: %v", err)
	}
	stop()
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket path remains after server shutdown, stat error = %v", err)
	}
}

func startTestServer(t *testing.T, allowedGID uint32) (string, func()) {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Serve(ctx, socketPath, allowedGID)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Lstat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("agent socket did not appear")
		}
		time.Sleep(time.Millisecond)
	}

	var stopped bool
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("stop agent server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("agent server did not stop")
		}
	}
	t.Cleanup(stop)
	return socketPath, stop
}

func sendRawRequest(t *testing.T, socketPath, request string) Response {
	t.Helper()
	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	if err := connection.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}

	var response Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}
