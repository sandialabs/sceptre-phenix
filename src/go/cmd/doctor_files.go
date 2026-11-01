package cmd

import (
	"errors"
	"io"
	"os"
)

// osReadAt reads at most n bytes of path from offset off, through a
// read-only file that takes no lock, so a running phenix ui is not stalled.
func osReadAt(path string, off int64, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	buf := make([]byte, n)

	read, err := f.ReadAt(buf, off)
	if errors.Is(err, io.EOF) {
		err = nil
	}

	return buf[:read], err
}
