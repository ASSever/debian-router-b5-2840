package network

import (
	"errors"
	"os"
	"strings"
)

const RestoreNetworkHold = "var/lib/debian-router-agent/restore-network"

// RestoredNetworkHeld protects the running policy during a disk restore and
// until a later boot loads the restored networkd configuration.
func RestoredNetworkHeld() bool {
	if _, err := os.Lstat("/var/lib/debian-router-agent/restore-journal.json"); !errors.Is(err, os.ErrNotExist) {
		return true
	}
	saved, err := os.ReadFile("/" + RestoreNetworkHold)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	boot, err := currentBoot()
	return err != nil || strings.TrimSpace(string(saved)) == boot
}

func CurrentRestoreBoot() (string, error) { return currentBoot() }
