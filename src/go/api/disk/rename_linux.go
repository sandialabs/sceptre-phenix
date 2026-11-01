package disk

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames src to dst unless dst exists, in one step when the
// filesystem can.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		// the filesystem or kernel does not support RENAME_NOREPLACE
		return linkRename(src, dst)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
