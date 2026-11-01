package health

import "syscall"

// deviceOf is the device of a file.
func deviceOf(st *syscall.Stat_t) uint64 {
	return uint64(st.Dev) //nolint:gosec // G115 device numbers are not negative
}

// blockSize is the filesystem's block size in bytes.
func blockSize(fs *syscall.Statfs_t) uint64 {
	return uint64(fs.Bsize)
}
