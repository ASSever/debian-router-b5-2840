package main

import (
	"syscall"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"golang.org/x/sys/unix"
)

func bindLANDevice(_, _ string, socket syscall.RawConn) error {
	var bindErr error
	if err := socket.Control(func(fd uintptr) {
		bindErr = unix.BindToDevice(int(fd), network.LANInterface)
	}); err != nil {
		return err
	}
	return bindErr
}
