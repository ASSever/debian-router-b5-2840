package network

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestIPv6DropInIsReadableUnderAgentUmask(t *testing.T) {
	root := t.TempDir()
	previousMask := syscall.Umask(0077)
	defer syscall.Umask(previousMask)

	directory := filepath.Join(root, "10-wan.network.d")
	path := filepath.Join(directory, "50-router-ipv6.conf")
	content := renderWANIPv6(RoutedIPv6{"fd26:1::2/64", "fe80::1", "fd26:2::/64"})
	if changed, err := syncIPv6File(path, content); err != nil || !changed {
		t.Fatalf("syncIPv6File() = %t, %v", changed, err)
	}
	for name, want := range map[string]os.FileMode{directory: 0755, path: 0644} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", name, got, want)
		}
	}
}
