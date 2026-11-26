//go:build linux

package main

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

func bindPrimary(wan string) func(string, string, syscall.RawConn) error {
	return func(_, _ string, socket syscall.RawConn) error {
		var setting error
		err := socket.Control(func(fd uintptr) {
			setting = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, wan)
			if setting == nil {
				setting = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, 0x01000000)
			}
		})
		if err != nil {
			return err
		}
		return setting
	}
}

func originalDestination(conn *net.TCPConn) netip.AddrPort {
	raw, err := conn.SyscallConn()
	if err != nil {
		return netip.AddrPort{}
	}
	var destination netip.AddrPort
	if err := raw.Control(func(fd uintptr) {
		// Linux returns a sockaddr_in. The IPv6 mreq helper supplies a
		// byte buffer large enough for the IPv4 socket address.
		addr, err := unix.GetsockoptIPv6Mreq(int(fd), unix.SOL_IP, unix.SO_ORIGINAL_DST)
		if err != nil {
			return
		}
		b := addr.Multiaddr
		if binary.NativeEndian.Uint16(b[:2]) != unix.AF_INET {
			return
		}
		destination = netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[4:8])), binary.BigEndian.Uint16(b[2:4]))
	}); err != nil {
		return netip.AddrPort{}
	}
	return destination
}
