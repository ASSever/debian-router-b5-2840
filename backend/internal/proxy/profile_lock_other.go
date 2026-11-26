//go:build !linux

package proxy

import "context"

func lockProfiles(context.Context, string) (func(), error) { return func() {}, nil }
func lockFile(context.Context, string) (func(), error)     { return func() {}, nil }
