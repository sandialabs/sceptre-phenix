package health

import "syscall"

// deviceOf is the device of a file.
func deviceOf(st *syscall.Stat_t) uint64 {
	return st.Dev
}
