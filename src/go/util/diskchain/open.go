package diskchain

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"phenix/util/file"
)

var (
	// ErrNotRegular is returned by OpenRegular for a path that is not a regular file.
	ErrNotRegular = errors.New("not a regular file")

	// ErrSystemDir is returned by OpenRegular for a path in one of the SystemDirs.
	ErrSystemDir = errors.New("in a system directory")

	// systemDirs hold files that are not disk images, and that can block or have side effects when opened.
	systemDirs = []string{"/proc", "/sys", "/dev", "/run"} //nolint:gochecknoglobals // constant
)

// InSystemDir reports whether path, compared as text, is in a folder that holds no disk images and whose files can
// block or have side effects when opened: /proc, /sys, /dev or /run.
func InSystemDir(path string) bool {
	for _, dir := range systemDirs {
		if file.WithinDir(dir, path) {
			return true
		}
	}

	return false
}

// OpenRegular opens path for reading when it is a regular file outside the system directories (see InSystemDir),
// without blocking on a FIFO or device put in its place. The error wraps ErrSystemDir, ErrNotRegular, or the error
// opening the file, such as [fs.ErrNotExist].
func OpenRegular(path string) (*os.File, error) {
	if InSystemDir(path) {
		return nil, fmt.Errorf("opening %s: %w", path, ErrSystemDir)
	}

	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // G703 the image a topology names
	if err != nil {
		return nil, fmt.Errorf("opening disk image: %w", err)
	}

	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("opening %s: %w", path, ErrNotRegular)
	}

	if err != nil {
		_ = f.Close()

		return nil, err
	}

	return f, nil
}
