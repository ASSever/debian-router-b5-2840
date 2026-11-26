package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type decision struct {
	proxy  bool
	streak int
}

func (d *decision) transition(primary, backup bool) bool {
	wantChange := (!d.proxy && !primary && backup) || (d.proxy && primary)
	if !wantChange {
		d.streak = 0
		return false
	}
	threshold := 2
	if d.proxy {
		threshold = 3
	}
	if d.streak < threshold {
		d.streak++
	}
	return d.streak == threshold
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", defaultConfig, "failover configuration file")
	flag.Parse()
	if flag.NArg() > 1 || (flag.NArg() == 1 && flag.Arg(0) != "check-config") {
		return errors.New("usage: router-failover [-config PATH] [check-config]")
	}
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	if flag.NArg() == 1 {
		fmt.Println("Failover configuration valid.")
		return nil
	}
	if err := cfg.validateInterfaces(); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := cfg.apply(ctx, false); err != nil {
		return err
	}
	defer clearRules()
	listener, err := cfg.listen()
	if err != nil {
		return fmt.Errorf("open failover relay: %w", err)
	}
	defer listener.Close()
	if err := notifyReady(); err != nil {
		return fmt.Errorf("notify systemd: %w", err)
	}
	acceptError := make(chan error, 1)
	go func() { acceptError <- cfg.serve(ctx, listener) }()
	log.Print("mode=zapret")

	var state decision
	var last [2]bool
	first := true
	for ctx.Err() == nil {
		primary, backup := cfg.probePaths(ctx)
		if ctx.Err() != nil {
			break
		}
		health := [2]bool{primary, backup}
		if first || health != last {
			log.Printf("primary_healthy=%t proxy_healthy=%t", primary, backup)
			last, first = health, false
		}
		if state.transition(primary, backup) {
			if err := cfg.apply(ctx, !state.proxy); err != nil {
				log.Print(err)
			} else {
				state = decision{proxy: !state.proxy}
				log.Printf("mode=%s primary_healthy=%t proxy_healthy=%t", modeName(state.proxy), primary, backup)
			}
		}
		timer := time.NewTimer(cfg.interval())
		select {
		case err := <-acceptError:
			timer.Stop()
			return err
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

func (c config) serve(ctx context.Context, listener *net.TCPListener) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	slots := make(chan struct{}, 64)
	var active sync.WaitGroup
	defer func() {
		cancel()
		active.Wait()
	}()
	for {
		client, err := listener.AcceptTCP()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
			active.Add(1)
			go func() {
				defer active.Done()
				defer func() { <-slots }()
				c.relay(ctx, client)
			}()
		default:
			client.Close()
		}
	}
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
