//go:build linux || darwin

package health

import (
	"fmt"
	"os"
	"syscall"
)

// Statfs measures the filesystem holding path. The field types of
// [syscall.Statfs_t] and [syscall.Stat_t] differ between Linux and macOS, so every
// field is converted.
func Statfs(path string) (DiskStat, error) {
	var fs syscall.Statfs_t

	if err := syscall.Statfs(path, &fs); err != nil {
		return DiskStat{}, fmt.Errorf("statfs: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return DiskStat{}, fmt.Errorf("stat: %w", err)
	}

	var device uint64

	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		device = deviceOf(st)
	}

	bsize := uint64(fs.Bsize) //nolint:gosec // int64 on Linux, uint32 on macOS

	return DiskStat{
		Device: device,
		Total:  fs.Blocks * bsize,
		Avail:  fs.Bavail * bsize,
	}, nil
}
