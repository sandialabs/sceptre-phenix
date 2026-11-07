//go:build linux

package cmd

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// filesystemType names the type of the filesystem that holds path, from the magic number statfs gives.
func filesystemType(path string) (string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", fmt.Errorf("statfs %s: %w", path, err)
	}

	return linuxFilesystemName(uint32(st.Type)), nil //nolint:gosec // G115 f_type is a 32-bit magic number
}
