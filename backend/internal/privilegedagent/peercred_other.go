//go:build !linux

package privilegedagent

import (
	"errors"
	"net"
)

func unixPeerGID(_ *net.UnixConn) (uint32, error) {
	return 0, errors.New("Unix peer credential checks require Linux")
}
