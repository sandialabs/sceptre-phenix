package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Cloning and renaming an image never replace a file, whether the kernel
// renames without replacing or phenix links and removes instead.
func TestCopiesAndRenamesNeverReplace(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		act     func(src, dst string) error
		removes bool
		refusal error
	}{
		"clone":             {act: MMDiskFiles{}.CloneDisk, removes: false, refusal: ErrExists},
		"rename":            {act: MMDiskFiles{}.RenameDisk, removes: true, refusal: ErrExists},
		"rename by linking": {act: linkRename, removes: true, refusal: fs.ErrExist},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			src, taken, dst := filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2"), filepath.Join(dir, "c.qc2")

			writeFile(t, src, "a")
			writeFile(t, taken, "b")

			if err := tc.act(src, taken); !errors.Is(err, tc.refusal) {
				t.Errorf("onto an existing file: %v, want %v", err, tc.refusal)
			}

			holds(t, src, "a")
			holds(t, taken, "b")

			if err := tc.act(src, dst); err != nil {
				t.Fatal(err)
			}

			holds(t, dst, "a")

			if _, err := os.Stat(src); tc.removes != errors.Is(err, fs.ErrNotExist) {
				t.Errorf("after the action, the source: %v", err)
			}
		})
	}
}

// Cloning refuses a FIFO put where an image was, rather than wait for a
// writer that never comes.
func TestCloneRefusesAFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fifo := filepath.Join(dir, "a.qc2")

	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	cloned := make(chan error, 1)

	go func() { cloned <- MMDiskFiles{}.CloneDisk(fifo, filepath.Join(dir, "b.qc2")) }()

	select {
	case err := <-cloned:
		if !errors.Is(err, ErrNotManaged) {
			t.Errorf("CloneDisk = %v, want ErrNotManaged", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloneDisk blocked opening a FIFO")
	}

	if _, err := os.Lstat(filepath.Join(dir, "b.qc2")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CloneDisk left a copy: %v", err)
	}
}

// holds fails the test unless the file at path holds content.
func holds(t *testing.T, path, content string) {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil || string(b) != content {
		t.Errorf("%s holds %q (%v), want %q", path, b, err, content)
	}
}
