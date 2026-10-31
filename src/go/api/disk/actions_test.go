package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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
				t.Errorf("%s(a.qc2, existing b.qc2) error = %v, want %v", name, err, tc.refusal)
			}

			want := map[string]string{"a.qc2": "a", "b.qc2": "b"}
			if diff := cmp.Diff(want, dirFiles(t, dir)); diff != "" {
				t.Errorf("files after %s(a.qc2, existing b.qc2) mismatch (-want +got):\n%s", name, diff)
			}

			if err := tc.act(src, dst); err != nil {
				t.Fatalf("%s(a.qc2, c.qc2) error = %v", name, err)
			}

			want = map[string]string{"b.qc2": "b", "c.qc2": "a"}
			if !tc.removes {
				want["a.qc2"] = "a"
			}

			if diff := cmp.Diff(want, dirFiles(t, dir)); diff != "" {
				t.Errorf("files after %s(a.qc2, c.qc2) mismatch (-want +got):\n%s", name, diff)
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
		t.Fatalf("syscall.Mkfifo(%s) error = %v", fifo, err)
	}

	cloned := make(chan error, 1)

	go func() { cloned <- MMDiskFiles{}.CloneDisk(fifo, filepath.Join(dir, "b.qc2")) }()

	select {
	case err := <-cloned:
		if !errors.Is(err, ErrNotManaged) {
			t.Errorf("CloneDisk(FIFO, b.qc2) error = %v, want %v", err, ErrNotManaged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloneDisk(FIFO, b.qc2) blocked opening the FIFO")
	}

	if _, err := os.Lstat(filepath.Join(dir, "b.qc2")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("os.Lstat(b.qc2) after CloneDisk(FIFO, b.qc2) error = %v, want %v", err, fs.ErrNotExist)
	}
}

// dirFiles returns the contents of each file in dir, by name.
func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%s) error = %v", dir, err)
	}

	files := make(map[string]string, len(entries))

	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", e.Name(), err)
		}

		files[e.Name()] = string(b)
	}

	return files
}
