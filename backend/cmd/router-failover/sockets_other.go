//go:build !linux

package main

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
)

func bindPrimary(string) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errors.New("failover requires Linux") }
}

func originalDestination(*net.TCPConn) netip.AddrPort { return netip.AddrPort{} }
