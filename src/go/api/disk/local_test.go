package disk

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/disk/disktest"
)

// useFakeQemuImg puts the fake qemu-img first on PATH, clears the cache, and returns its call counter.
func useFakeQemuImg(t *testing.T) func() int {
	t.Helper()

	calls := disktest.UseFakeQemuImg(t)

	ClearCache()
	t.Cleanup(ClearCache)

	return calls
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func TestGetImageInspectsOnlyChangedImages(t *testing.T) {
	calls := useFakeQemuImg(t)
	path := filepath.Join(t.TempDir(), "a.qc2")
	writeFile(t, path, "one")

	want := Details{
		Kind: VMImage, Name: "a.qc2", FullPath: path, OutsideFilesDir: true, ReadOnly: true,
		Size: "2.0 KiB", VirtualSize: "1.0 GiB", BackingImages: []string{}, Format: "qcow2",
	}

	for range 3 {
		got, err := GetImage(path)
		if err != nil {
			t.Fatalf("GetImage(%s) error = %v", path, err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetImage(%s) mismatch (-want +got):\n%s", path, diff)
		}
	}

	if got := calls(); got != 1 {
		t.Errorf("qemu-img runs for GetImage(%s) of an unchanged image three times = %d, want 1", path, got)
	}

	writeFile(t, path, "changed")

	if _, err := GetImage(path); err != nil {
		t.Fatalf("GetImage(%s) error = %v", path, err)
	}

	if got := calls(); got != 2 {
		t.Errorf("qemu-img runs once the image changed = %d, want 2", got)
	}

	ClearCache()

	if _, err := GetImage(path); err != nil {
		t.Fatalf("GetImage(%s) error = %v", path, err)
	}

	if got := calls(); got != 3 {
		t.Errorf("qemu-img runs after ClearCache() = %d, want 3", got)
	}
}

// A running VM holds a lock on the images it has open (QEMU takes an open
// file description lock), which is how minimega tells an image is in use.
func TestGetImageReportsLockedImagesInUse(t *testing.T) {
	if _, err := os.ReadFile("/proc/locks"); err != nil {
		t.Skipf("the kernel's file locks cannot be read: %v", err)
	}

	// F_OFD_SETLK, the same on every Linux architecture; package syscall
	// does not name it
	const setOFDLock = 37

	write := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0, Pid: 0}

	tests := map[string]func(fd int) error{
		"flock":             func(fd int) error { return syscall.Flock(fd, syscall.LOCK_EX) },
		"POSIX record lock": func(fd int) error { return syscall.FcntlFlock(uintptr(fd), syscall.F_SETLK, &write) },
		"open file lock":    func(fd int) error { return syscall.FcntlFlock(uintptr(fd), setOFDLock, &write) },
	}

	for name, lock := range tests {
		t.Run(name, func(t *testing.T) {
			useFakeQemuImg(t)

			path := filepath.Join(t.TempDir(), "a.qc2")
			writeFile(t, path, "x")

			got, err := GetImage(path)
			if err != nil {
				t.Fatalf("GetImage(unlocked image) error = %v", err)
			}

			if got.InUse {
				t.Errorf("GetImage(unlocked image).InUse = true, want false")
			}

			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatalf("os.OpenFile(%s) error = %v", path, err)
			}

			defer f.Close()

			if err := lock(int(f.Fd())); err != nil {
				t.Fatalf("taking a %s on %s: %v", name, path, err)
			}

			got, err = GetImage(path)
			if err != nil {
				t.Fatalf("GetImage(image under a %s) error = %v", name, err)
			}

			if !got.InUse {
				t.Errorf("GetImage(image under a %s).InUse = false, want true", name)
			}
		})
	}
}

func TestGetImageFindsBackingImageOnceItAppears(t *testing.T) {
	calls := useFakeQemuImg(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "over.qc2")
	writeFile(t, path, "x")
	writeFile(t, path+".backing", "base.qc2")

	got, err := GetImage(path)
	if err != nil {
		t.Fatalf("GetImage(image with its backing image missing) error = %v", err)
	}

	if got.Name != "over.qc2" {
		t.Errorf("GetImage(image with its backing image missing).Name = %q, want %q", got.Name, "over.qc2")
	}

	if diff := cmp.Diff([]string(nil), got.BackingImages, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("GetImage(image with its backing image missing).BackingImages mismatch (-want +got):\n%s", diff)
	}

	if n := calls(); n != 2 {
		t.Errorf("qemu-img runs = %d, want 2 (the chain, then the image alone)", n)
	}

	writeFile(t, filepath.Join(dir, "base.qc2"), "y")

	got, err = GetImage(path)
	if err != nil {
		t.Fatalf("GetImage(image once its backing image exists) error = %v", err)
	}

	want := []string{filepath.Join(dir, "base.qc2")}
	if diff := cmp.Diff(want, got.BackingImages); diff != "" {
		t.Errorf("GetImage(image once its backing image exists).BackingImages mismatch (-want +got):\n%s", diff)
	}
}

func TestWatchDebouncesImageChanges(t *testing.T) {
	t.Parallel()

	const debounce = 100 * time.Millisecond

	dir := t.TempDir()
	changes := make(chan struct{}, 10)

	if err := Watch(t.Context(), dir, debounce, func() { changes <- struct{}{} }); err != nil {
		t.Fatalf("Watch(%s) error = %v", dir, err)
	}

	for i := range 5 {
		writeFile(t, filepath.Join(dir, "upload.qc2"), string(rune('a'+i)))
	}

	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch() reported no change within 5s of writing an image")
	}

	writeFile(t, filepath.Join(dir, "notes.txt"), "not an image")

	// a further report would come one debounce period after the event behind
	// it; the second period leaves room for the timer and scheduling to lag
	select {
	case <-changes:
		t.Error("Watch() reported the burst of writes more than once, or a file that is not an image")
	case <-time.After(2 * debounce):
	}
}

// genLocalBytes draws sizes of every magnitude, and either side of each unit.
func genLocalBytes() *rapid.Generator[int64] {
	return rapid.OneOf(
		rapid.Custom(func(t *rapid.T) int64 {
			shift := rapid.IntRange(0, 62).Draw(t, "shift")

			return rapid.Int64Range(0, 1<<shift).Draw(t, "under")
		}),
		rapid.Custom(func(t *rapid.T) int64 {
			unit := int64(1) << (10 * rapid.IntRange(1, 6).Draw(t, "unit"))

			return unit + rapid.Int64Range(-2, 2).Draw(t, "off")
		}),
	)
}

// localNamedBytes returns the bytes humanReadableBytes(b) names, and the power
// of 1024 of the unit it names them in.
func localNamedBytes(t *rapid.T, b int64) (float64, int) {
	got := humanReadableBytes(b)

	if n, ok := strings.CutSuffix(got, " B"); ok {
		v, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			t.Fatalf("humanReadableBytes(%d) = %q, not a count of bytes", b, got)
		}

		return float64(v), 0
	}

	n, unit, ok := strings.Cut(got, " ")
	exp := strings.Index("KMGTPE", strings.TrimSuffix(unit, "iB"))

	v, err := strconv.ParseFloat(n, 64)
	if !ok || exp < 0 || len(unit) != len("KiB") || err != nil || n != strconv.FormatFloat(v, 'f', 1, 64) {
		t.Fatalf("humanReadableBytes(%d) = %q, not a size with one decimal and a binary unit", b, got)
	}

	return v * math.Pow(1024, float64(exp+1)), exp + 1
}

// humanReadableBytes names a size to within half its last digit, in the
// largest unit the size reaches, and names larger sizes no smaller.
func TestHumanReadableBytesProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		a, b := genLocalBytes().Draw(t, "a"), genLocalBytes().Draw(t, "b")
		if a > b {
			a, b = b, a
		}

		named, unit := localNamedBytes(t, b)

		if math.Abs(named-float64(b)) > 0.05*math.Pow(1024, float64(unit))*(1+1e-9) {
			t.Fatalf("humanReadableBytes(%d) = %q, off by more than half its last digit", b, humanReadableBytes(b))
		}

		if (b>>(10*unit) == 0 && unit > 0) || (unit < 6 && b>>(10*(unit+1)) > 0) {
			t.Fatalf("humanReadableBytes(%d) = %q, want the largest unit %d reaches", b, humanReadableBytes(b), b)
		}

		if namedA, _ := localNamedBytes(t, a); namedA > named {
			t.Fatalf("humanReadableBytes(%d) = %q, larger than humanReadableBytes(%d) = %q",
				a, humanReadableBytes(a), b, humanReadableBytes(b))
		}
	})
}

// The disk list and phenix doctor find VM images by a qcow2, qcow or .hdd extension, container images by _rootfs.tgz
// and ISO images by .iso, all of them case sensitive.
func TestKnownImage(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"kali.qcow2":      true,
		"kali.qc2":        true,
		"old.qcow":        true,
		"win/old.qcow":    true,
		"snap.hdd":        true,
		"kali_rootfs.tgz": true,
		"tools.iso":       true,
		"old.qcow.bak":    false,
		"old.QCOW":        false,
		"oldqcow":         false,
		"notes.tgz":       false,
		"kali.img":        false,
	}

	for name, want := range tests {
		if got := knownImage(name); got != want {
			t.Errorf("knownImage(%q) = %t, want %t", name, got, want)
		}
	}
}
