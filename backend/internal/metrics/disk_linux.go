//go:build linux

package metrics

import "golang.org/x/sys/unix"

func diskSpace(path string) *Space {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil || stats.Bfree > stats.Blocks {
		return nil
	}
	size := uint64(stats.Bsize)
	return &Space{Total: stats.Blocks * size, Used: (stats.Blocks - stats.Bfree) * size}
}
