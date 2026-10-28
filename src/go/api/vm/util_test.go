package vm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
	"phenix/util/mm"
)

// A copy that ends while a slow subscriber has not taken its progress ends cleanly, closing the
// subscription only once progress stops being sent on it.
func TestCopyEndsWhileProgressIsPending(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 64<<10), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", src, err)
	}

	for i := range 50 {
		c := newCopier(time.Millisecond)
		s := c.subscribe()
		read := make(chan struct{})

		go func() {
			defer close(read)

			for range s {
				time.Sleep(2 * time.Millisecond)
			}
		}()

		if err := c.copy(context.Background(), src, filepath.Join(dir, "dst")); err != nil {
			t.Fatalf("copier.copy(src, dst) error = %v (copy %d of 50)", err, i+1)
		}

		select {
		case <-read:
		case <-time.After(2 * time.Second):
			t.Fatalf("copier.copy(src, dst) left its subscription open (copy %d of 50)", i+1)
		}
	}
}

// A new image takes the time in place of any earlier one, and keeps a qcow2 or qcow extension, as the copy it names
// keeps the format of the image.
func TestNewDiskName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 31, 12, 30, 45, 0, time.UTC)

	tests := map[string]struct {
		base string
		want string
	}{
		"qc2":             {base: "kali.qc2", want: "kali_20261031123045.qc2"},
		"qcow2 in folder": {base: "win/win10.qcow2", want: "win/win10_20261031123045.qcow2"},
		"qcow version 1":  {base: "old.qcow", want: "old_20261031123045.qcow"},
		"earlier time":    {base: "kali_20191117102805.qcow", want: "kali_20261031123045.qcow"},
		"raw":             {base: "kali.img", want: "kali_20261031123045.img.qc2"},
		"no extension":    {base: "kali", want: "kali_20261031123045.qc2"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := newDiskName(tt.base, now); got != tt.want {
				t.Errorf("newDiskName(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

// A new image keeps the folder and the text around an earlier time; only the time itself is replaced.
func TestNewDiskNameKeepsTextAroundEarlierTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 31, 12, 30, 45, 0, time.UTC)

	tests := map[string]struct {
		base string
		want string
	}{
		"text after the time": {base: "web_20191117102805_v2.qc2", want: "web_20261031123045_v2.qc2"},
		"time in the folder":  {base: "bak_20191117102805/kali.qc2", want: "bak_20191117102805/kali_20261031123045.qc2"},
		"two earlier times":   {base: "a_20191117102805_20201117102805.qc2", want: "a_20191117102805_20261031123045.qc2"},
		"15 digits":           {base: "kali_201911171028059.qc2", want: "kali_201911171028059_20261031123045.qc2"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := newDiskName(tt.base, now); got != tt.want {
				t.Errorf("newDiskName(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

// utilShellMM answers each mesh shell command with resp and err and records the commands; other
// mm.MM methods panic through the nil embedded interface.
type utilShellMM struct {
	mm.MM

	resp string
	err  error

	commands []string
}

func (m *utilShellMM) MeshShellResponse(_ context.Context, host, cmd string) (string, error) {
	m.commands = append(m.commands, host+": "+cmd)

	return m.resp, m.err
}

// utilUseShell makes fake answer for minimega until the test ends.
func utilUseShell(t *testing.T, fake *utilShellMM) *utilShellMM {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// utilChain is qemu-img's JSON backing chain of files, the image first.
func utilChain(files ...string) string {
	entries := make([]string, 0, len(files))

	for i, f := range files {
		backing := ""
		if i+1 < len(files) {
			backing = files[i+1]
		}

		entries = append(entries, `{"filename":"`+f+`","backing-filename":"`+backing+`"}`)
	}

	return "[" + strings.Join(entries, ",") + "]"
}

// errUtilShell is the error the fake minimega gives for a failed shell command.
var errUtilShell = errors.New("qemu-img failed")

//nolint:paralleltest // replaces minimega
func TestGetImageBackingChain(t *testing.T) {
	tests := map[string]struct {
		resp    string
		err     error
		want    []qemuBackingChain
		wantErr error // nil for none; errUtilAny for any
	}{
		"image with a backing file": {
			resp: utilChain("/phenix/images/snap.qc2", "/phenix/images/base.qc2"),
			want: []qemuBackingChain{
				{Filename: "/phenix/images/snap.qc2", BackingFile: "/phenix/images/base.qc2"},
				{Filename: "/phenix/images/base.qc2"},
			},
		},
		"empty chain":       {resp: "[]", want: []qemuBackingChain{}},
		"failed command":    {err: errUtilShell, wantErr: errUtilShell},
		"output is no JSON": {resp: "qemu-img: Could not open", wantErr: errUtilAny},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := utilUseShell(t, &utilShellMM{resp: tc.resp, err: tc.err})

			got, err := getImageBackingChain(t.Context(), "/phenix/images/snap.qc2")

			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("getImageBackingChain(snap.qc2) error = %v, want nil", err)
			case errors.Is(tc.wantErr, errUtilAny) && err == nil:
				t.Fatal("getImageBackingChain(snap.qc2) error = nil, want an error")
			case tc.wantErr != nil && !errors.Is(tc.wantErr, errUtilAny) && !errors.Is(err, tc.wantErr):
				t.Fatalf("getImageBackingChain(snap.qc2) error = %v, want %v", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("getImageBackingChain(snap.qc2) mismatch (-want +got):\n%s", diff)
			}

			// on the head node
			want := []string{": qemu-img info --backing-chain /phenix/images/snap.qc2 --output json"}
			if diff := cmp.Diff(want, fake.commands); diff != "" {
				t.Errorf("getImageBackingChain(snap.qc2) shell commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// errUtilAny is the wanted error of a case that expects an error of any kind.
var errUtilAny = errors.New("any error")

// The snapshots of an image are the files of its backing chain from the one above the base image to the image
// itself, the order they are rebased in.
//
//nolint:paralleltest // replaces minimega
func TestGetImageSnapshots(t *testing.T) {
	tests := map[string]struct {
		resp    string
		err     error
		want    []string
		wantErr bool
	}{
		"base image only": {resp: utilChain("/i/base.qc2"), want: nil},
		"empty chain":     {resp: "[]", want: nil},
		"one snapshot":    {resp: utilChain("/i/s1.qc2", "/i/base.qc2"), want: []string{"/i/s1.qc2"}},
		"snapshots of snapshots": {
			resp: utilChain("/i/s3.qc2", "/i/s2.qc2", "/i/s1.qc2", "/i/base.qc2"),
			want: []string{"/i/s1.qc2", "/i/s2.qc2", "/i/s3.qc2"},
		},
		"failed command": {err: errUtilShell, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			utilUseShell(t, &utilShellMM{resp: tc.resp, err: tc.err})

			got, err := getImageSnapshots(t.Context(), "/i/s3.qc2")
			if (err != nil) != tc.wantErr {
				t.Fatalf("getImageSnapshots(s3.qc2) error = %v, want error %t", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("getImageSnapshots(s3.qc2) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The backing image of a disk is the base of its backing chain, or the file a base that is a symlink names. A disk
// named without a folder is one of phenix's images.
//
//nolint:paralleltest // replaces minimega
func TestGetBackingImage(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.qc2")
	link := filepath.Join(dir, "link.qc2")

	if err := os.WriteFile(base, []byte("qcow"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", base, err)
	}

	if err := os.Symlink("/elsewhere/real.qc2", link); err != nil {
		t.Fatalf("os.Symlink(%s) error = %v", link, err)
	}

	tests := map[string]struct {
		disk    string
		resp    string
		err     error
		want    string
		wantCmd string // the path qemu-img reads
		wantErr bool
	}{
		"absolute path": {
			disk: "/data/snap.qc2", resp: utilChain("/data/snap.qc2", base), want: base, wantCmd: "/data/snap.qc2",
		},
		"image name": {
			disk: "snap.qc2", resp: utilChain(common.PhenixBase+"/images/snap.qc2", base), want: base,
			wantCmd: common.PhenixBase + "/images/snap.qc2",
		},
		"base is a symlink": {
			disk: "/data/snap.qc2", resp: utilChain("/data/snap.qc2", link), want: "/elsewhere/real.qc2",
			wantCmd: "/data/snap.qc2",
		},
		"base is missing": {
			disk: "/data/snap.qc2", resp: utilChain("/data/snap.qc2", filepath.Join(dir, "gone.qc2")), wantErr: true,
			wantCmd: "/data/snap.qc2",
		},
		"failed command": {disk: "/data/snap.qc2", err: errUtilShell, wantErr: true, wantCmd: "/data/snap.qc2"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := utilUseShell(t, &utilShellMM{resp: tc.resp, err: tc.err})

			got, err := getBackingImage(t.Context(), tc.disk)
			if (err != nil) != tc.wantErr {
				t.Fatalf("getBackingImage(%q) error = %v, want error %t", tc.disk, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("getBackingImage(%q) = %q, want %q", tc.disk, got, tc.want)
			}

			want := []string{": qemu-img info --backing-chain " + tc.wantCmd + " --output json"}
			if diff := cmp.Diff(want, fake.commands); diff != "" {
				t.Errorf("getBackingImage(%q) shell commands mismatch (-want +got):\n%s", tc.disk, diff)
			}
		})
	}
}

// An empty backing chain is an error, not a panic.
//
//nolint:paralleltest // replaces minimega
func TestGetBackingImageEmptyChain(t *testing.T) {
	utilUseShell(t, &utilShellMM{resp: "[]"})

	if _, err := getBackingImage(t.Context(), "/data/snap.qc2"); err == nil {
		t.Error("getBackingImage(snap.qc2) with an empty chain error = nil, want an error")
	}
}

// utilStoreExperiment stores the experiment exp, whose VM vm1 boots image, in a new store for the test.
func utilStoreExperiment(t *testing.T, image string) {
	t.Helper()

	storetest.UseBolt(t)

	node := map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": "vm1"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": image}}},
		"network":  map[string]any{"interfaces": []any{}},
	}

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"baseDir":        t.TempDir(),
			"topology":       map[string]any{"nodes": []any{node}},
		},
	})
	if err != nil {
		t.Fatalf("store.Create(experiment/exp) error = %v", err)
	}
}

// A VM's image is its first drive's; an experiment or VM that does not exist is an error.
//
//nolint:paralleltest // replaces the store
func TestGetVMImage(t *testing.T) {
	utilStoreExperiment(t, "win/win10.qcow2")

	tests := map[string]struct {
		exp, vm string
		want    string
		wantErr bool
	}{
		"VM":            {exp: "exp", vm: "vm1", want: "win/win10.qcow2"},
		"no such VM":    {exp: "exp", vm: "vm2", wantErr: true},
		"no such exp":   {exp: "other", vm: "vm1", wantErr: true},
		"no experiment": {exp: "", vm: "vm1", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := getVMImage(tc.exp, tc.vm)
			if (err != nil) != tc.wantErr {
				t.Fatalf("getVMImage(%q, %q) error = %v, want error %t", tc.exp, tc.vm, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("getVMImage(%q, %q) = %q, want %q", tc.exp, tc.vm, got, tc.want)
			}
		})
	}
}

// A new disk is named after the VM's image and the time now.
//
//nolint:paralleltest // replaces the store
func TestGetNewDiskName(t *testing.T) {
	utilStoreExperiment(t, "win/win10_20191117102805.qcow2")

	before := time.Now().Truncate(time.Second)

	got, err := GetNewDiskName("exp", "vm1")
	if err != nil {
		t.Fatalf("GetNewDiskName(exp, vm1) error = %v", err)
	}

	after := time.Now()

	match := regexp.MustCompile(`^win/win10_(\d{14})\.qcow2$`).FindStringSubmatch(got)
	if match == nil {
		t.Fatalf("GetNewDiskName(exp, vm1) = %q, want win/win10_<time>.qcow2", got)
	}

	stamp, err := time.ParseInLocation("20060102150405", match[1], time.Now().Location())
	if err != nil || stamp.Before(before) || stamp.After(after) {
		t.Errorf("GetNewDiskName(exp, vm1) time = %s (error %v), want between %s and %s", match[1], err, before, after)
	}

	if got, err := GetNewDiskName("exp", "vm2"); err == nil {
		t.Errorf("GetNewDiskName(exp, vm2) = %q, want an error for a VM the topology lacks", got)
	}
}

// utilWriteFile writes data to a new file in dir and returns its path.
func utilWriteFile(t *testing.T, dir string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, "src")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}

	return path
}

// A copy writes the source's bytes to the destination, reporting progress to each subscriber as a fraction that
// never decreases; it ends every subscription, whether or not it succeeds. A fast copy can end before the first
// report, so the test does not require one.
func TestCopy(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx      context.Context //nolint:containedctx // the context of the copy
		noSource bool
		empty    bool   // the source has no bytes
		dst      string // relative to the test's folder
		wantErr  error
	}{
		"copies the source":          {ctx: context.Background(), dst: "dst"},
		"copies an empty source":     {ctx: context.Background(), empty: true, dst: "dst"},
		"missing source":             {ctx: context.Background(), noSource: true, dst: "dst", wantErr: os.ErrNotExist},
		"destination folder missing": {ctx: context.Background(), dst: "missing/dst", wantErr: os.ErrNotExist},
		"canceled":                   {ctx: canceled, dst: "dst", wantErr: context.Canceled},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			dst := filepath.Join(dir, tc.dst)

			want := data
			if tc.empty {
				want = []byte{}
			}

			src := filepath.Join(dir, "src")
			if !tc.noSource {
				src = utilWriteFile(t, dir, want)
			}

			c := newCopier(time.Nanosecond)
			subs := []chan float64{c.subscribe(), c.subscribe()}
			progress := make([][]float64, len(subs))

			var wg sync.WaitGroup

			for i, s := range subs {
				wg.Add(1)

				go func() {
					defer wg.Done()

					for p := range s {
						progress[i] = append(progress[i], p)
					}
				}()
			}

			err := c.copy(tc.ctx, src, dst)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("copier.copy(%s, %s) error = %v, want %v", src, dst, err, tc.wantErr)
			}

			wg.Wait() // each subscription is closed

			for i, p := range progress {
				for j := range p {
					if p[j] < 0 || p[j] > 1 || math.IsNaN(p[j]) || (j > 0 && p[j] < p[j-1]) {
						t.Errorf("copier.copy(%s, %s) subscriber %d progress = %v, want fractions that never decrease", src, dst, i, p)

						break
					}
				}
			}

			if tc.wantErr != nil {
				return
			}

			if got, err := os.ReadFile(dst); err != nil || !bytes.Equal(got, want) {
				t.Errorf("copier.copy(%s, %s) wrote %d bytes (read error %v), want the %d source bytes", src, dst, len(got), err, len(want))
			}
		})
	}
}

// copyFraction is the copied share of the source, and 1 for an empty source, never NaN.
func TestCopyFraction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		n, size int64
		want    float64
	}{
		"nothing copied": {n: 0, size: 4, want: 0},
		"half copied":    {n: 2, size: 4, want: 0.5},
		"all copied":     {n: 4, size: 4, want: 1},
		"empty source":   {n: 0, size: 0, want: 1},
		"unknown size":   {n: 3, size: -1, want: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := copyFraction(tc.n, tc.size); got != tc.want {
				t.Errorf("copyFraction(%d, %d) = %v, want %v", tc.n, tc.size, got, tc.want)
			}
		})
	}
}

// done ends every subscription of a copier that never copies.
func TestCopierDone(t *testing.T) {
	t.Parallel()

	c := newCopier(time.Second)
	subs := []chan float64{c.subscribe(), c.subscribe()}

	c.done()

	for i, s := range subs {
		if _, open := <-s; open {
			t.Errorf("subscription %d of %d is open after done(), want closed", i+1, len(subs))
		}
	}
}

// utilShortWriter writes at most n bytes in all, then fails.
type utilShortWriter struct {
	n int
}

var errUtilShortWrite = errors.New("disk full")

func (w *utilShortWriter) Write(b []byte) (int, error) {
	if len(b) <= w.n {
		w.n -= len(b)

		return len(b), nil
	}

	n := w.n
	w.n = 0

	return n, errUtilShortWrite
}

// A progress writer counts the bytes its writer took, even from a failed write, and passes on the writer's error.
func TestProgressWriter(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		limit   int
		writes  []string
		wantN   int64
		wantErr error
	}{
		"no writes":    {limit: 10, wantN: 0},
		"empty write":  {limit: 10, writes: []string{""}, wantN: 0},
		"all written":  {limit: 10, writes: []string{"abc", "defg"}, wantN: 7},
		"short write":  {limit: 5, writes: []string{"abc", "defg"}, wantN: 5, wantErr: errUtilShortWrite},
		"exactly full": {limit: 7, writes: []string{"abc", "defg"}, wantN: 7},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pw := newProgressWriter(&utilShortWriter{n: tc.limit})

			var err error

			for _, w := range tc.writes {
				if _, err = pw.Write([]byte(w)); err != nil {
					break
				}
			}

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("progressWriter.Write(%q) error = %v, want %v", tc.writes, err, tc.wantErr)
			}

			if got := pw.N(); got != tc.wantN {
				t.Errorf("progressWriter.N() after %q = %d, want %d", tc.writes, got, tc.wantN)
			}
		})
	}
}

// Concurrent writes and reads of the count all add up.
func TestProgressWriterConcurrent(t *testing.T) {
	t.Parallel()

	const (
		writers = 8
		writes  = 100
	)

	pw := newProgressWriter(io.Discard)

	var wg sync.WaitGroup

	for range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range writes {
				_, _ = pw.Write([]byte("ab"))
				_ = pw.N()
			}
		}()
	}

	wg.Wait()

	if got, want := pw.N(), int64(writers*writes*2); got != want {
		t.Errorf("progressWriter.N() after %d concurrent writes of 2 bytes = %d, want %d", writers*writes, got, want)
	}
}

// A cancelable reader reads from its reader until its context is done, then gives the context's error.
func TestCancelableReader(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cr := newCancelableReader(ctx, strings.NewReader("abcdef"))
	buf := make([]byte, 3)

	if n, err := cr.Read(buf); n != 3 || err != nil || string(buf) != "abc" {
		t.Errorf("cancelableReader.Read() = %d, %v (%q), want 3, nil (abc)", n, err, buf)
	}

	cancel()

	if n, err := cr.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelableReader.Read() after cancel = %d, %v; want 0, %v", n, err, context.Canceled)
	}
}

// A timestamp is the date, hour and minute, so the memory dumps and captures it names a minute apart do not
// overwrite each other.
func TestGetTimestampHasMinutes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		now  time.Time
		want string
	}{
		"minute 50":   {now: time.Date(2026, 10, 31, 11, 50, 59, 0, time.UTC), want: "20261031_1150"},
		"minute 5":    {now: time.Date(2026, 1, 2, 23, 5, 0, 0, time.UTC), want: "20260102_2305"},
		"on the hour": {now: time.Date(2026, 10, 31, 9, 0, 30, 0, time.UTC), want: "20261031_0900"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, getTimestamp(tt.now)); diff != "" {
				t.Errorf("getTimestamp(%s) mismatch (-want +got):\n%s", tt.now, diff)
			}
		})
	}
}
