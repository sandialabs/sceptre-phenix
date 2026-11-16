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
	case noReplaceUnsupported(err):
		return linkRename(src, dst)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}

// noReplaceUnsupported tells if err from Renameat2 means that the kernel or
// the filesystem cannot rename with RENAME_NOREPLACE. Some overlay and network
// filesystems give EOPNOTSUPP.
func noReplaceUnsupported(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP)
}
