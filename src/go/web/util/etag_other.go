//go:build !unix

package util

import "io/fs"

// fileInode is 0 where the file system has no inode numbers; the size and
// modification time still change with the file.
func fileInode(fs.FileInfo) uint64 {
	return 0
}
