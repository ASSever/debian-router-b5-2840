package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/monitor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	stateDir := os.Getenv("STATE_DIRECTORY")
	if stateDir == "" {
		stateDir = "/var/lib/debian-router-monitor"
	}
	flags := flag.NewFlagSet("router-monitor", flag.ExitOnError)
	config := flags.String("config", monitor.DefaultConfigPath, "configuration file")
	state := flags.String("state-dir", stateDir, "monitor state directory")
	limit := flags.Int("limit", 100, "maximum history rows to display")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: router-monitor [options] [run|check-config|history]")
		flags.PrintDefaults()
	}
	flags.Parse(args)

	switch {
	case flags.NArg() == 0 || (flags.NArg() == 1 && flags.Arg(0) == "run"):
		return daemon(*config, *state)
	case flags.NArg() == 1 && flags.Arg(0) == "check-config":
		_, err := monitor.LoadConfig(*config)
		if err == nil {
			fmt.Println("Monitor configuration valid.")
		}
		return err
	case flags.NArg() == 1 && flags.Arg(0) == "history":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		samples, err := monitor.ReadHistory(ctx, *state, *limit)
		if err != nil {
			return fmt.Errorf("read monitor history: %w", err)
		}
		return json.NewEncoder(os.Stdout).Encode(samples)
	default:
		flags.Usage()
		return fmt.Errorf("invalid monitor command")
	}
}

func daemon(configPath, stateDir string) error {
	cfg, err := monitor.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load monitor configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	history, err := monitor.OpenHistory(initCtx, stateDir)
	cancel()
	if err != nil {
		return fmt.Errorf("open monitor history: %w", err)
	}
	defer history.Close()
	if err := notifyReady(); err != nil {
		return fmt.Errorf("notify systemd: %w", err)
	}

	prober := monitor.NewProber(cfg)
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	for ctx.Err() == nil {
		samples := prober.CheckAll(ctx, cfg.Targets)
		if ctx.Err() != nil {
			break
		}
		saveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := history.Save(saveCtx, samples, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("save monitor history: %v", err)
		}
		pause := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			pause.Stop()
		case <-pause.C:
		}
	}
	return nil
}

func notifyReady() error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = conn.Write([]byte("READY=1"))
	return err
}
