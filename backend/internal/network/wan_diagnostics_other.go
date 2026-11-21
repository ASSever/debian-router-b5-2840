//go:build !linux

package network

import (
	"context"
	"errors"
)

func RunWANDiagnostic(context.Context) (WANDiagnostic, error) {
	return WANDiagnostic{}, errors.New("WAN diagnostics require Linux")
}
