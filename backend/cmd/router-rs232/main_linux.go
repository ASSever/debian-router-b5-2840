package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		ports, err := listPorts()
		if err != nil {
			return err
		}
		fmt.Println("DEVICE\tTYPE\tSYSFS")
		for _, port := range ports {
			fmt.Printf("%s\t%s\t%s\n", port.path, port.kind, port.sysfs)
		}
		return nil
	case len(args) > 0 && args[0] == "exchange":
		return runExchange(ctx, args[1:])
	default:
		return fmt.Errorf("usage: router-rs232 list | exchange -port /dev/ttyS0 -send-hex HEX -expect-hex HEX [-baud 9600] [-timeout 2s]")
	}
}

func runExchange(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("exchange", flag.ContinueOnError)
	port := flags.String("port", "", "serial device from list")
	send := flags.String("send-hex", "", "hexadecimal bytes to send")
	expect := flags.String("expect-hex", "", "hexadecimal bytes expected in reply")
	baud := flags.Int("baud", 9600, "bit rate (9600, 19200, 38400, 57600, 115200)")
	timeout := flags.Duration("timeout", 2*time.Second, "total exchange timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *port == "" || *timeout < time.Millisecond || *timeout > time.Minute {
		return fmt.Errorf("provide -port and a timeout from 1ms to 1m, with no positional arguments")
	}
	speed, ok := map[int]uint32{
		9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400,
		57600: unix.B57600, 115200: unix.B115200,
	}[*baud]
	if !ok {
		return fmt.Errorf("unsupported baud rate %d", *baud)
	}
	tx, err := frame(*send)
	if err != nil {
		return fmt.Errorf("-send-hex: %w", err)
	}
	want, err := frame(*expect)
	if err != nil {
		return fmt.Errorf("-expect-hex: %w", err)
	}
	if err := checkPort(*port); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	got := make([]byte, len(want))
	if err := exchange(ctx, *port, speed, tx, got); err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("reply mismatch: received %x, expected %x", got, want)
	}
	fmt.Printf("%s: sent %d bytes, received %d matching bytes (%x)\n", *port, len(tx), len(got), got)
	return nil
}

func frame(value string) ([]byte, error) {
	if len(value) < 2 || len(value) > 1024 {
		return nil, fmt.Errorf("provide 1–512 bytes as contiguous hexadecimal pairs")
	}
	return hex.DecodeString(value)
}
