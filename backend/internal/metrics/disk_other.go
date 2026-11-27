//go:build !linux

package metrics

func diskSpace(string) *Space { return nil }
