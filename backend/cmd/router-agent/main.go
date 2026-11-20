package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	socketPath := os.Getenv("ROUTER_AGENT_SOCKET")
	if socketPath == "" {
		socketPath = privilegedagent.DefaultSocketPath()
	}

	if len(arguments) == 2 && arguments[0] == "request" {
		response, err := privilegedagent.SendRequest(
			context.Background(), socketPath, arguments[1],
		)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
			return fmt.Errorf("write agent response: %w", err)
		}
		if response.Error != "" {
			return errors.New("privileged agent rejected the request")
		}
		return nil
	}
	if len(arguments) == 2 && arguments[0] == "rollback" {
		if os.Geteuid() != 0 {
			return errors.New("WAN rollback requires root")
		}
		manager, err := network.NewStaticWANManager()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return manager.RollbackFromTimer(ctx, arguments[1])
	}
	if len(arguments) != 0 {
		return errors.New("usage: router-agent [request OPERATION | rollback TRANSACTION_ID]")
	}

	allowedGIDText := os.Getenv("ROUTER_AGENT_ALLOWED_GID")
	allowedGID, err := strconv.ParseUint(allowedGIDText, 10, 32)
	if err != nil || allowedGID == 0 {
		return errors.New("ROUTER_AGENT_ALLOWED_GID must be a non-root numeric group ID")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return privilegedagent.Serve(ctx, socketPath, uint32(allowedGID))
}
