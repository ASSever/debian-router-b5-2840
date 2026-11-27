package main

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExchangeWithTerminalPeer(t *testing.T) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(master)
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/dev/pts/%d", number)
	slave, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(slave)
	original, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request := []byte{0, 0x13, 0xa5, '\n'}
	reply := []byte{0xff, 0x11, '\r'}
	peer := make(chan error, 1)
	go func() {
		received := make([]byte, len(request))
		if err := transfer(ctx, master, unix.POLLIN, received, unix.Read); err != nil {
			peer <- err
		} else if !bytes.Equal(received, request) {
			peer <- fmt.Errorf("unexpected request %x", received)
		} else {
			peer <- transfer(ctx, master, unix.POLLOUT, reply, unix.Write)
		}
	}()
	got := make([]byte, len(reply))
	exchangeErr := exchange(ctx, path, unix.B9600, request, got)
	cancel()
	if err := <-peer; err != nil {
		t.Fatal(err)
	}
	if exchangeErr != nil || !bytes.Equal(got, reply) {
		t.Fatalf("exchange returned %x, %v", got, exchangeErr)
	}
	restored, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil || *restored != *original {
		t.Fatalf("terminal settings not restored: %v, %v", restored, err)
	}
}
