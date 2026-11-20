//go:build linux

package privilegedagent

import (
	"fmt"
	"net"
	"syscall"
)

func unixPeerGID(connection *net.UnixConn) (uint32, error) {
	rawConnection, err := connection.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("get Unix socket descriptor: %w", err)
	}

	var credentials *syscall.Ucred
	var socketErr error
	if err := rawConnection.Control(func(fd uintptr) {
		credentials, socketErr = syscall.GetsockoptUcred(
			int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED,
		)
	}); err != nil {
		return 0, fmt.Errorf("read Unix peer credentials: %w", err)
	}
	if socketErr != nil {
		return 0, fmt.Errorf("read Unix peer credentials: %w", socketErr)
	}
	if credentials == nil {
		return 0, fmt.Errorf("read Unix peer credentials: empty credential record")
	}
	return credentials.Gid, nil
}
