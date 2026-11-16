package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"phenix/util/testenv"
)

// When linkRename cannot remove the source after it links the destination,
// it removes the destination again and returns the error.
func TestLinkRenameRollsBackWhenItCannotRemoveTheSource(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		testenv.Skipf(t, "root removes files from a read-only directory")
	}

	srcDir, dstDir := t.TempDir(), t.TempDir()
	src, dst := filepath.Join(srcDir, "a.qc2"), filepath.Join(dstDir, "b.qc2")

	writeFile(t, src, "a")

	if err := os.Chmod(srcDir, 0o500); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", srcDir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(srcDir, 0o700) })

	if err := linkRename(src, dst); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("linkRename(a.qc2 in a read-only directory, b.qc2) error = %v, want %v", err, fs.ErrPermission)
	}

	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("os.Lstat(b.qc2) after the failed linkRename error = %v, want %v", err, fs.ErrNotExist)
	}

	if got, err := os.ReadFile(src); err != nil || string(got) != "a" {
		t.Errorf("os.ReadFile(a.qc2) after the failed linkRename = %q, %v, want %q, nil", got, err, "a")
	}
}
