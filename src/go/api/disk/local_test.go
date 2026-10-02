package disk

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"phenix/api/disk/disktest"
)

// useFakeQemuImg puts disktest's fake qemu-img first on PATH, with nothing
// cached from earlier inspections, and returns a function counting the times
// it has run.
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
		t.Fatal(err)
	}
}

func TestGetImageInspectsOnlyChangedImages(t *testing.T) {
	calls := useFakeQemuImg(t)
	path := filepath.Join(t.TempDir(), "a.qc2")
	writeFile(t, path, "one")

	for range 3 {
		got, err := GetImage(path)
		if err != nil {
			t.Fatal(err)
		}

		want := Details{
			Kind: VMImage, Name: "a.qc2", FullPath: path, OutsideFilesDir: true, ReadOnly: true,
			Size: "2.0 KiB", VirtualSize: "1.0 GiB", BackingImages: []string{},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetImage = %+v, want %+v", got, want)
		}
	}

	if got := calls(); got != 1 {
		t.Fatalf("unchanged image inspected %d times, want 1", got)
	}

	writeFile(t, path, "changed")

	if _, err := GetImage(path); err != nil {
		t.Fatal(err)
	}

	if got := calls(); got != 2 {
		t.Fatalf("changed image not inspected again (%d inspections)", got)
	}

	ClearCache()

	if _, err := GetImage(path); err != nil {
		t.Fatal(err)
	}

	if got := calls(); got != 3 {
		t.Fatalf("ClearCache did not force inspection (%d inspections)", got)
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

			if got, err := GetImage(path); err != nil || got.InUse {
				t.Fatalf("GetImage of an unlocked image = %+v, %v; want it not in use", got, err)
			}

			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}

			defer f.Close()

			if err := lock(int(f.Fd())); err != nil {
				t.Fatal(err)
			}

			if got, err := GetImage(path); err != nil || !got.InUse {
				t.Fatalf("GetImage of a locked image = %+v, %v; want it in use", got, err)
			}
		})
	}
}

func TestGetImageShowsImageWhoseBackingChainIsUnreadable(t *testing.T) {
	calls := useFakeQemuImg(t)

	path := filepath.Join(t.TempDir(), "broken.qc2")
	writeFile(t, path, "x")

	got, err := GetImage(path)
	if err != nil || got.Name != "broken.qc2" || len(got.BackingImages) != 0 {
		t.Fatalf("GetImage = %+v, %v; want the image alone", got, err)
	}

	if n := calls(); n != 2 {
		t.Fatalf("qemu-img ran %d times, want the chain then the image alone", n)
	}
}

func TestWatchDebouncesImageChanges(t *testing.T) {
	const debounce = 100 * time.Millisecond

	dir := t.TempDir()
	changes := make(chan struct{}, 10)

	if err := Watch(t.Context(), dir, debounce, func() { changes <- struct{}{} }); err != nil {
		t.Fatal(err)
	}

	for i := range 5 {
		writeFile(t, filepath.Join(dir, "upload.qc2"), string(rune('a'+i)))
	}

	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}

	writeFile(t, filepath.Join(dir, "notes.txt"), "not an image")

	// a further report would come one debounce period after the event behind
	// it; the second period leaves room for the timer and scheduling to lag
	select {
	case <-changes:
		t.Fatal("reported the burst of writes more than once, or a file that is not an image")
	case <-time.After(2 * debounce):
	}
}
