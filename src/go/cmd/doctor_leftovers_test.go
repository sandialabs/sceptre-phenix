package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSized(t *testing.T, path string, size int) { //nolint:unparam // a helper for files of any size
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}
