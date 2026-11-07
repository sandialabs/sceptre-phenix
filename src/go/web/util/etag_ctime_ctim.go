//go:build linux || openbsd || dragonfly || solaris

package util

import (
	"io/fs"
	"syscall"
)

// fileChangeTime is the inode change time of the file in nanoseconds, or 0 if
// the file system reports none.
func fileChangeTime(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ctim.Nano()
	}

	return 0
}
