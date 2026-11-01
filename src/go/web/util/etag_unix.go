//go:build unix

package util

import (
	"io/fs"
	"syscall"
)

// fileInode is the file's inode number, or 0 if the file system reports none.
func fileInode(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino) //nolint:unconvert // Ino is not uint64 on every platform
	}

	return 0
}
