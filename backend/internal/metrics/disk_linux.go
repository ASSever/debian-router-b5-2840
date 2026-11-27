//go:build linux

package metrics

import (
	"github.com/ASSever/debian-router-b5-2840/backend/internal/hostmetrics"
	"golang.org/x/sys/unix"
)

func diskSpace(path string) *hostmetrics.Space {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil || stats.Bfree > stats.Blocks {
		return nil
	}
	size := uint64(stats.Bsize)
	return &hostmetrics.Space{Total: stats.Blocks * size, Used: (stats.Blocks - stats.Bfree) * size}
}
