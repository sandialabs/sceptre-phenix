package disk

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
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

// A finished clone leaves the copy, with the source's permissions, and no temporary file, having reported every byte.
func TestCloneFile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		size     int
		progress bool
	}{
		"more than a chunk":    {size: cloneChunk + 12345, progress: true},
		"an empty image":       {size: 0, progress: true},
		"no progress reported": {size: 100, progress: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				dir      = t.TempDir()
				src, dst = filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")
				content  = make([]byte, tt.size)
				copied   atomic.Int64
				progress func(int64)
			)

			for i := range content {
				content[i] = byte(i)
			}

			if err := os.WriteFile(src, content, 0o640); err != nil {
				t.Fatalf("os.WriteFile(%s) error = %v", src, err)
			}

			if tt.progress {
				progress = func(n int64) { copied.Add(n) }
			}

			if err := cloneFile(t.Context(), src, dst, progress); err != nil {
				t.Fatalf("cloneFile(a.qc2, b.qc2) error = %v", err)
			}

			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("os.ReadFile(%s) error = %v", dst, err)
			}

			if !bytes.Equal(got, content) {
				t.Errorf("cloneFile(a.qc2, b.qc2) wrote %d bytes that differ from the %d of the source", len(got), len(content))
			}

			if want := int64(len(content)); tt.progress && copied.Load() != want {
				t.Errorf("cloneFile(a.qc2, b.qc2) reported %d bytes copied, want %d", copied.Load(), want)
			}

			if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0o640 {
				t.Errorf("os.Stat(b.qc2) = %v, %v; want mode 0640", info, err)
			}

			if left := leftovers(t, dir, "a.qc2", "b.qc2"); len(left) != 0 {
				t.Errorf("cloneFile(a.qc2, b.qc2) left %v behind, want nothing", left)
			}
		})
	}
}

// A clone that cannot start fails before it writes anything, and leaves an existing destination as it was.
func TestCloneFileRefuses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src, dst string
		want     error
	}{
		"a missing source":             {src: "gone.qc2", dst: "b.qc2", want: fs.ErrNotExist},
		"a folder as the source":       {src: "sub", dst: "b.qc2", want: ErrNotManaged},
		"an existing destination":      {src: "a.qc2", dst: "taken.qc2", want: ErrExists},
		"a destination with no folder": {src: "a.qc2", dst: "gone/b.qc2", want: fs.ErrNotExist},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "a.qc2"), "source")
			writeFile(t, filepath.Join(dir, "taken.qc2"), "taken")

			if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
				t.Fatalf("os.Mkdir(sub) error = %v", err)
			}

			err := cloneFile(t.Context(), filepath.Join(dir, tt.src), filepath.Join(dir, tt.dst), nil)
			if !errors.Is(err, tt.want) {
				t.Errorf("cloneFile(%s, %s) error = %v, want %v", tt.src, tt.dst, err, tt.want)
			}

			if left := leftovers(t, dir, "a.qc2", "taken.qc2", "sub"); len(left) != 0 {
				t.Errorf("cloneFile(%s, %s) left %v behind, want nothing", tt.src, tt.dst, left)
			}

			if got, err := os.ReadFile(filepath.Join(dir, "taken.qc2")); string(got) != "taken" {
				t.Errorf("os.ReadFile(taken.qc2) after cloneFile(%s, %s) = %q, %v; want %q", tt.src, tt.dst, got, err, "taken")
			}
		})
	}
}

// A clone whose context ends part way leaves neither the copy nor its temporary file. A cancellation is returned as
// it is, so the caller tells it apart; a deadline names the clone.
func TestCloneFileContextEnds(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ctx     func(t *testing.T) (context.Context, func(int64))
		want    error
		wrapped bool
	}{
		"canceled after the first chunk": {
			ctx: func(t *testing.T) (context.Context, func(int64)) {
				t.Helper()

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				return ctx, func(int64) { cancel() }
			},
			want: context.Canceled,
		},
		"past its deadline": {
			ctx: func(t *testing.T) (context.Context, func(int64)) {
				t.Helper()

				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)

				return ctx, nil
			},
			want:    context.DeadlineExceeded,
			wrapped: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
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

			ctx, progress := tt.ctx(t)

			err = cloneFile(ctx, src, dst, progress)
			if !errors.Is(err, tt.want) || (err != tt.want) != tt.wrapped { //nolint:errorlint // checks the error is not wrapped
				t.Errorf("cloneFile(a.qc2, b.qc2) error = %v, want %v, wrapped: %t", err, tt.want, tt.wrapped)
			}

			if left := leftovers(t, dir, "a.qc2"); len(left) != 0 {
				t.Errorf("cloneFile(a.qc2, b.qc2) %s left %v behind, want nothing", name, left)
			}
		})
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

// cloneChecker is a writer that checks it receives want from its start.
type cloneChecker struct {
	want []byte
	off  int
	bad  bool
	sent []int
}

func (c *cloneChecker) Write(b []byte) (int, error) {
	if !bytes.Equal(b, c.want[c.off:min(c.off+len(b), len(c.want))]) {
		c.bad = true
	}

	c.off += len(b)
	c.sent = append(c.sent, len(b))

	return len(b), nil
}

// copyChunks copies every byte in order, a full chunk at a time but for a
// shorter last one, however the source splits its reads, and reports each
// chunk it writes and nothing else.
func TestCopyChunksProperties(t *testing.T) {
	t.Parallel()

	src := make([]byte, 2*cloneChunk)
	for i := range src {
		src[i] = byte(i % 251)
	}

	rapid.Check(t, func(t *rapid.T) {
		size := rapid.OneOf(
			rapid.IntRange(0, 1000),
			rapid.SampledFrom([]int{cloneChunk - 1, cloneChunk, cloneChunk + 1, 2 * cloneChunk}),
		).Draw(t, "size")

		var in io.Reader = bytes.NewReader(src[:size])
		if rapid.Bool().Draw(t, "short reads") {
			in = iotest.HalfReader(in)
		}

		var (
			out      = &cloneChecker{want: src[:size], off: 0, bad: false, sent: nil}
			reported []int
		)

		if err := copyChunks(context.Background(), out, in, func(n int64) { reported = append(reported, int(n)) }); err != nil {
			t.Fatalf("copyChunks(%d bytes) error = %v", size, err)
		}

		if out.off != size || out.bad {
			t.Fatalf("copyChunks(%d bytes) wrote %d bytes, some wrong: %t; want the source bytes", size, out.off, out.bad)
		}

		want := make([]int, 0, size/cloneChunk+1)
		for left := size; left > 0; left -= cloneChunk {
			want = append(want, min(left, cloneChunk))
		}

		if diff := cmp.Diff(want, out.sent, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("copyChunks(%d bytes) writes mismatch (-want +got):\n%s", size, diff)
		}

		if diff := cmp.Diff(want, reported, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("copyChunks(%d bytes) progress mismatch (-want +got):\n%s", size, diff)
		}
	})
}

// cloneFailingWriter fails every write with err.
type cloneFailingWriter struct{ err error }

func (w cloneFailingWriter) Write([]byte) (int, error) { return 0, w.err }

// copyChunks stops at the first failed read or write, naming which failed, and at once when ctx is done.
func TestCopyChunksErrors(t *testing.T) {
	t.Parallel()

	errDisk := errors.New("disk failed")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx     context.Context //nolint:containedctx // the context of the copy
		out     io.Writer
		in      io.Reader
		want    error
		message string
	}{
		"a failed write": {
			ctx: context.Background(), out: cloneFailingWriter{err: errDisk}, in: bytes.NewReader([]byte("abc")),
			want: errDisk, message: "writing: disk failed",
		},
		"a failed read": {
			ctx: context.Background(), out: io.Discard, in: iotest.ErrReader(errDisk),
			want: errDisk, message: "reading: disk failed",
		},
		"a done context": {
			ctx: canceled, out: cloneFailingWriter{err: errDisk}, in: bytes.NewReader([]byte("abc")),
			want: context.Canceled, message: context.Canceled.Error(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var reported int64

			err := copyChunks(tt.ctx, tt.out, tt.in, func(n int64) { reported += n })
			if !errors.Is(err, tt.want) || err.Error() != tt.message {
				t.Errorf("copyChunks() error = %v, want %q wrapping %v", err, tt.message, tt.want)
			}

			if reported != 0 {
				t.Errorf("copyChunks() reported %d bytes copied, want 0", reported)
			}
		})
	}
}

// RemoveStalePartials fails for a files directory it cannot read, and stops at a partial file it cannot remove,
// counting those it removed; a folder under it that it cannot read holds nothing it removes.
func TestRemoveStalePartialsErrors(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads and writes folders whatever their permissions")
	}

	const partial = ".a.qc2.partial-0123456789abcdef"

	tests := map[string]struct {
		// locked is the folder, relative to the files directory, made unreadable (0) or read-only (0o500)
		locked  string
		mode    os.FileMode
		want    int
		wantErr error
	}{
		"an unreadable files directory": {locked: ".", mode: 0, want: 0, wantErr: fs.ErrPermission},
		"an unreadable folder":          {locked: "lab", mode: 0, want: 1},
		"a read-only folder":            {locked: "lab", mode: 0o500, want: 1, wantErr: fs.ErrPermission},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeImages(t, filepath.Join(dir, partial), filepath.Join(dir, "lab", partial))

			locked := filepath.Join(dir, tt.locked)
			if err := os.Chmod(locked, tt.mode); err != nil {
				t.Fatalf("os.Chmod(%s, %o) error = %v", tt.locked, tt.mode, err)
			}

			t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

			removed, err := RemoveStalePartials(dir, time.Now().Add(time.Minute))
			if removed != tt.want || !errors.Is(err, tt.wantErr) || (err == nil) != (tt.wantErr == nil) {
				t.Errorf("RemoveStalePartials(%s) = %d, %v; want %d, %v", name, removed, err, tt.want, tt.wantErr)
			}
		})
	}
}

// A files directory path that runs through a file fails, rather than count as a missing folder.
func TestRemoveStalePartialsThroughAFile(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")

	removed, err := RemoveStalePartials(filepath.Join(file, "images"), time.Now())
	if removed != 0 || !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("RemoveStalePartials(file/images) = %d, %v; want 0, %v", removed, err, syscall.ENOTDIR)
	}
}
