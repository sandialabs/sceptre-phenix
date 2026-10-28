package health

import "syscall"

// deviceOf is the device of a file.
func deviceOf(st *syscall.Stat_t) uint64 {
	return st.Dev
}

// blockSize is the filesystem's block size in bytes.
func blockSize(fs *syscall.Statfs_t) uint64 {
	return uint64(fs.Bsize) //nolint:gosec // G115 block sizes are positive
}
