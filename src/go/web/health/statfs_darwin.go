package health

import "syscall"

// deviceOf is the device of a file.
func deviceOf(st *syscall.Stat_t) uint64 {
	return uint64(st.Dev) //nolint:gosec // device numbers are not negative
}
