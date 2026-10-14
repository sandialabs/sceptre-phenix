package disk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// leftovers returns the names in dir other than the given ones.
func leftovers(t *testing.T, dir string, keep ...string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	if err := cloneFile(t.Context(), src, dst, func(n int64) { copied.Add(n) }); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(content) {
		t.Fatalf("clone differs from its source (%v)", err)
	}

	if copied.Load() != int64(len(content)) {
		t.Errorf("progress reported %d bytes, want %d", copied.Load(), len(content))
	}

	if left := leftovers(t, dir, "a.qc2", "b.qc2"); len(left) != 0 {
		t.Errorf("clone left %v behind", left)
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
		t.Fatal(err)
	}

	if err := f.Truncate(8 * cloneChunk); err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = cloneFile(ctx, src, dst, func(int64) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cloneFile = %v, want context.Canceled", err)
	}

	if left := leftovers(t, dir, "a.qc2"); len(left) != 0 {
		t.Errorf("canceled clone left %v behind", left)
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
		t.Fatalf("cloneFile = %v, want ErrExists", err)
	}

	holds(t, dst, "someone else's")

	if left := leftovers(t, dir, "a.qc2", "b.qc2"); len(left) != 0 {
		t.Errorf("clone left %v behind", left)
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
			t.Fatal(err)
		}

		writeFile(t, path, "x")
	}

	// a clone started since keeps its file
	running := filepath.Join(dir, ".new.qc2.partial-00112233445566ff")
	writeFile(t, running, "x")

	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(running, future, future); err != nil {
		t.Fatal(err)
	}

	removed, err := RemoveStalePartials(dir, time.Now().Add(time.Minute))
	if err != nil || removed != 2 {
		t.Fatalf("RemoveStalePartials = %d, %v; want 2", removed, err)
	}

	for p, want := range map[string]bool{
		".ubuntu.qc2.partial-0123456789abcdef":    false,
		"lab/.win.qcow2.partial-fedcba9876543210": false,
		"ubuntu.qc2.partial-0123456789abcdef":     true,
		".ubuntu.qc2.partial-0123":                true,
		".hidden/.x.qc2.partial-0123456789abcdef": true,
		".new.qc2.partial-00112233445566ff":       true,
		"ubuntu.qc2":                              true,
	} {
		_, err := os.Stat(filepath.Join(dir, p))
		if exists := err == nil; exists != want {
			t.Errorf("%s exists: %t, want %t", p, exists, want)
		}
	}

	if n, err := RemoveStalePartials(filepath.Join(dir, "missing"), time.Now()); n != 0 || err != nil {
		t.Errorf("a missing files directory: %d, %v", n, err)
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
		t.Fatal(err)
	}

	if len(fake.cloned) != 1 || fake.cloned[0][1] != filepath.Join(dir, "b.qc2") {
		t.Errorf("cloned %v, want a.qc2 to b.qc2", fake.cloned)
	}

	if err := CloneDiskContext(t.Context(), "a.qc2", "a.qc2", nil); !errors.Is(err, ErrExists) {
		t.Errorf("onto an existing image: %v, want ErrExists", err)
	}

	if err := CloneDiskContext(t.Context(), "missing.qc2", "c.qc2", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing image: %v, want fs.ErrNotExist", err)
	}
}
