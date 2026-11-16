//go:build !linux && !openbsd && !dragonfly && !solaris && !darwin && !freebsd && !netbsd

package util

import "io/fs"

// fileChangeTime is 0 where phenix cannot read the inode change time; the
// other parts of the ETag still change with the file.
func fileChangeTime(fs.FileInfo) int64 {
	return 0
}
