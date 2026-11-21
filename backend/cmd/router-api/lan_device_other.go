//go:build !linux

package main

import (
	"errors"
	"syscall"
)

func bindLANDevice(_, _ string, _ syscall.RawConn) error {
	return errors.New("LAN HTTPS is only available on Linux")
}
