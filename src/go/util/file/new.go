package file

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxNewSuffix bounds the suffixes WriteNew tries for a name that is taken.
const maxNewSuffix = 100

// newFileMode is the mode of a file that WriteNew makes.
const newFileMode = 0o600

// ErrExists is returned by WriteNew when the name and each suffixed name are taken.
var ErrExists = errors.New("file exists")

// WriteNew writes data to a new file at name (a path relative to dir) in the local folder dir. It never replaces a
// file: when the name is taken, it adds -1, -2 and so on before the extension. It returns the name that it wrote.
// The folder of the file must exist.
func WriteNew(dir, name string, data []byte) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for n := range maxNewSuffix {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}

		err := writeExclusive(filepath.Join(dir, candidate), data)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return "", fmt.Errorf("writing %s: %w", candidate, err)
		}

		return candidate, nil
	}

	return "", fmt.Errorf("%w: %s and %d other names", ErrExists, name, maxNewSuffix-1)
}

// writeExclusive writes data to a file at local that it creates, and fails with [fs.ErrExist] when one is there. It
// removes a file that it could not write in full.
func writeExclusive(local string, data []byte) error {
	f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, newFileMode)
	if err != nil {
		return err //nolint:wrapcheck // the caller wraps it
	}

	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(local)

		return err //nolint:wrapcheck // the caller wraps it
	}

	return nil
}
