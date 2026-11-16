package util

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileChecksum(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")

	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	got, err := FileChecksum(path, "a.txt")
	if err != nil {
		t.Fatalf("FileChecksum(%s) error = %v", path, err)
	}

	sum := sha256.Sum256([]byte("abc"))
	if got.SHA256 != hex.EncodeToString(sum[:]) || got.Path != "a.txt" || got.Size != 3 || got.ETag == "" {
		t.Errorf("FileChecksum(%s) = %+v, want the SHA-256 of abc, path a.txt, size 3 and an ETag", path, got)
	}

	if _, err := FileChecksum(dir, "dir"); !errors.Is(err, ErrNotRegular) {
		t.Errorf("FileChecksum(a folder) error = %v, want %v", err, ErrNotRegular)
	}

	if _, err := FileChecksum(filepath.Join(dir, "none"), "none"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FileChecksum(a missing file) error = %v, want %v", err, os.ErrNotExist)
	}
}
