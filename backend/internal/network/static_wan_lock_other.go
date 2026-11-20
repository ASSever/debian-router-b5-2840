//go:build !linux

package network

import (
	"os"
	"sync"
)

var staticWANTestLock sync.Mutex

func withFileLock(path string, operation func() error) error {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	staticWANTestLock.Lock()
	defer staticWANTestLock.Unlock()
	return operation()
}
