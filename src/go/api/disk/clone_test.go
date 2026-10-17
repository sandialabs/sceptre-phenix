package disk

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// leftovers returns the names in dir other than the given ones.
func leftovers(t *testing.T, dir string, keep ...string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%s) error = %v", dir, err)
	}

	var names []string

outer:
	for _, e := range entries {
		for _, k := range keep {
			if e.Name() == k {
				continue outer
			}
		}

		names = append(names, e.Name())
	}

	return names
}

// A finished clone leaves the copy and no temporary file, having reported
// every byte.
func TestCloneFile(t *testing.T) {
	t.Parallel()

	var (
		dir      = t.TempDir()
		src, dst = filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")
		content  = make([]byte, cloneChunk+12345)
		copied   atomic.Int64
	)

	for i := range content {
		content[i] = byte(i)
	}

	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", src, err)
	}

	if err := cloneFile(t.Context(), src, dst, func(n int64) { copied.Add(n) }); err != nil {
		t.Fatalf("cloneFile(a.qc2, b.qc2) error = %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", dst, err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("cloneFile(a.qc2, b.qc2) wrote %d bytes that differ from the %d of the source", len(got), len(content))
	}

	if copied.Load() != int64(len(content)) {
		t.Errorf("cloneFile(a.qc2, b.qc2) reported %d bytes copied, want %d", copied.Load(), len(content))
	}

	if left := leftovers(t, dir, "a.qc2", "b.qc2"); len(left) != 0 {
		t.Errorf("cloneFile(a.qc2, b.qc2) left %v behind, want nothing", left)
	}
}

// A clone canceled part way leaves neither the copy nor its temporary file.
func TestCloneFileCanceled(t *testing.T) {
	t.Parallel()

	var (
		dir      = t.TempDir()
		src, dst = filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")
	)

	// sparse, so the test writes little to the disk
	f, err := os.Create(src)
	if err != nil {
		t.Fatalf("os.Create(%s) error = %v", src, err)
	}

	if err := f.Truncate(8 * cloneChunk); err != nil {
		t.Fatalf("Truncate(%d) error = %v", 8*cloneChunk, err)
	}

	_ = f.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = cloneFile(ctx, src, dst, func(int64) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cloneFile(a.qc2, b.qc2) canceled after its first chunk: error = %v, want %v", err, context.Canceled)
	}

	if left := leftovers(t, dir, "a.qc2"); len(left) != 0 {
		t.Errorf("canceled cloneFile(a.qc2, b.qc2) left %v behind, want nothing", left)
	}
}

// An image created at the destination while the clone runs is kept, and the
// clone fails with ErrExists, removing its temporary file.
func TestCloneFileDestinationAppears(t *testing.T) {
	t.Parallel()

	var (
		dir      = t.TempDir()
		src, dst = filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")
	)

	writeFile(t, src, "source")

	var once atomic.Bool

	err := cloneFile(t.Context(), src, dst, func(int64) {
		if once.CompareAndSwap(false, true) {
			writeFile(t, dst, "someone else's")
		}
	})
	if !errors.Is(err, ErrExists) {
		t.Errorf("cloneFile(a.qc2, b.qc2 made while it runs) error = %v, want %v", err, ErrExists)
	}

	want := map[string]string{"a.qc2": "source", "b.qc2": "someone else's"}
	if diff := cmp.Diff(want, dirFiles(t, dir)); diff != "" {
		t.Errorf("files after cloneFile(a.qc2, b.qc2 made while it runs) mismatch (-want +got):\n%s", diff)
	}
}

// Only the hidden temporary files of clones are removed, in the folders the
// disk list looks in.
func TestRemoveStalePartials(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, p := range []string{
		"ubuntu.qc2",
		".ubuntu.qc2.partial-0123456789abcdef",         // removed
		"lab/.win.qcow2.partial-fedcba9876543210",      // removed
		"ubuntu.qc2.partial-0123456789abcdef",          // not hidden
		".ubuntu.qc2.partial-0123",                     // too short
		".notes",                                       // not a clone's
		".hidden/.x.qc2.partial-0123456789abcdef",      // a folder clones never write to
		"exp/files/.y.qc2.partial-0123456789abcdef",    // ditto
		"lab/.win.qcow2.partial-FEDCBA9876543210.keep", // not the pattern
	} {
		path := filepath.Join(dir, p)

		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(p), err)
		}

		writeFile(t, path, "x")
	}

	// a clone started since keeps its file
	running := filepath.Join(dir, ".new.qc2.partial-00112233445566ff")
	writeFile(t, running, "x")

	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(running, future, future); err != nil {
		t.Fatalf("os.Chtimes(%s) error = %v", running, err)
	}

	removed, err := RemoveStalePartials(dir, time.Now().Add(time.Minute))
	if err != nil {
		t.Errorf("RemoveStalePartials(dir, in a minute) error = %v, want nil", err)
	}

	if removed != 2 {
		t.Errorf("RemoveStalePartials(dir, in a minute) = %d, want 2", removed)
	}

	want := map[string]bool{
		".ubuntu.qc2.partial-0123456789abcdef":    false,
		"lab/.win.qcow2.partial-fedcba9876543210": false,
		"ubuntu.qc2.partial-0123456789abcdef":     true,
		".ubuntu.qc2.partial-0123":                true,
		".hidden/.x.qc2.partial-0123456789abcdef": true,
		".new.qc2.partial-00112233445566ff":       true,
		"ubuntu.qc2":                              true,
	}
	got := make(map[string]bool, len(want))

	for p := range want {
		_, err := os.Stat(filepath.Join(dir, p))
		got[p] = err == nil
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RemoveStalePartials(dir, in a minute) files kept mismatch (-want +got):\n%s", diff)
	}

	if n, err := RemoveStalePartials(filepath.Join(dir, "missing"), time.Now()); n != 0 || err != nil {
		t.Errorf("RemoveStalePartials(missing folder, now) = %d, %v; want 0, nil", n, err)
	}
}

// fakeCloner is a DiskFiles that clones without context or progress, as
// DiskFiles implementations other than MMDiskFiles may.
type fakeCloner struct {
	DiskFiles

	cloned [][2]string
}

func (f *fakeCloner) CloneDisk(src, dst string) error {
	f.cloned = append(f.cloned, [2]string{src, dst})

	return nil
}

// CloneDiskContext clones through a DiskFiles that cannot cancel as it
// clones otherwise.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestCloneDiskContextFallsBack(t *testing.T) {
	dir := useFilesDir(t)

	writeFile(t, filepath.Join(dir, "a.qc2"), "a")

	fake := new(fakeCloner)

	original := DefaultDiskFiles
	DefaultDiskFiles = fake

	t.Cleanup(func() { DefaultDiskFiles = original })

	if err := CloneDiskContext(t.Context(), "a.qc2", "b.qc2", nil); err != nil {
		t.Errorf("CloneDiskContext(a.qc2, b.qc2) error = %v, want nil", err)
	}

	want := [][2]string{{filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")}}
	if diff := cmp.Diff(want, fake.cloned); diff != "" {
		t.Errorf("CloneDiskContext(a.qc2, b.qc2) CloneDisk calls mismatch (-want +got):\n%s", diff)
	}

	if err := CloneDiskContext(t.Context(), "a.qc2", "a.qc2", nil); !errors.Is(err, ErrExists) {
		t.Errorf("CloneDiskContext(a.qc2, existing a.qc2) error = %v, want %v", err, ErrExists)
	}

	if err := CloneDiskContext(t.Context(), "missing.qc2", "c.qc2", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CloneDiskContext(missing.qc2, c.qc2) error = %v, want %v", err, fs.ErrNotExist)
	}
}
