package disk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/disk/disktest"
	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
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
func TestImageExtension(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"kali.qcow2":      ".qcow2",
		"kali.qc2":        ".qc2",
		"old.qcow":        ".qcow",
		"win/old.qcow":    ".qcow",
		"snap.hdd":        ".hdd",
		"kali_rootfs.tgz": "_rootfs.tgz",
		"tools.iso":       ".iso",
		"old.qcow.bak":    "",
		"old.QCOW":        "",
		"oldqcow":         "",
		"notes.tgz":       "",
		"kali.img":        "",
		"":                "",
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := ImageExtension(name); got != want {
				t.Errorf("ImageExtension(%q) = %q, want %q", name, got, want)
			}

			if got := knownImage(name); got != (want != "") {
				t.Errorf("knownImage(%q) = %t, want %t", name, got, want != "")
			}
		})
	}
}

// localFakeQemuImg puts first on PATH, until the test ends, a qemu-img that runs script.
func localFakeQemuImg(t *testing.T, script string) {
	t.Helper()

	fake := fakeQemuImg(t, script)
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// LocalInspection reports whether qemu-img is on PATH.
func TestLocalInspection(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if LocalInspection() {
		t.Errorf("LocalInspection() without qemu-img = true, want false")
	}

	localFakeQemuImg(t, "exit 0\n")

	if !LocalInspection() {
		t.Errorf("LocalInspection() with qemu-img = false, want true")
	}
}

// runQemuImgInfo runs qemu-img info on the image, sharing it with running VMs, and says why when it fails.
func TestRunQemuImgInfo(t *testing.T) {
	tests := map[string]struct {
		// script is the fake qemu-img, or "" for none
		script   string
		chain    bool
		canceled bool
		want     string
		wantErr  string
	}{
		"the image alone":    {script: `echo "$*"`, want: "info -U --output=json /f/a.qc2\n"},
		"the backing chain":  {script: `echo "$*"`, chain: true, want: "info -U --output=json --backing-chain /f/a.qc2\n"},
		"no qemu-img":        {wantErr: errNoQemuImg.Error()},
		"a failed run":       {script: "echo ' could not open ' >&2; exit 1", wantErr: "qemu-img info /f/a.qc2: could not open"},
		"a canceled context": {script: "exit 0", canceled: true, wantErr: "qemu-img info /f/a.qc2: context canceled"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())

			if tt.script != "" {
				localFakeQemuImg(t, tt.script+"\n")
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tt.canceled {
				cancel()
			}

			out, err := runQemuImgInfo(ctx, "/f/a.qc2", tt.chain)
			if got := fmt.Sprint(err); (err != nil || tt.wantErr != "") && got != tt.wantErr {
				t.Errorf("runQemuImgInfo(%s) error = %v, want %q", name, err, tt.wantErr)
			}

			if string(out) != tt.want {
				t.Errorf("runQemuImgInfo(%s) = %q, want %q", name, out, tt.want)
			}
		})
	}
}

// inspectChain reads the backing chain, and falls back to the image alone, reported incomplete, when qemu-img cannot
// read the chain or reports none.
func TestInspectChain(t *testing.T) {
	const (
		image = `{"filename":"/f/a.qc2","format":"qcow2","virtual-size":1024,"actual-size":512}`
		base  = `{"filename":"/f/base.qc2","format":"qcow2"}`
	)

	tests := map[string]struct {
		// chain and alone are what the fake qemu-img does for the chain and for the image alone
		chain, alone string
		want         []qemuImage
		complete     bool
		wantErr      string
	}{
		"a whole chain": {
			chain: "echo '[" + image + "," + base + "]'", alone: "exit 1",
			want: []qemuImage{
				{Filename: "/f/a.qc2", Format: "qcow2", VirtualSize: 1024, ActualSize: 512},
				{Filename: "/f/base.qc2", Format: "qcow2"},
			},
			complete: true,
		},
		"a chain qemu-img cannot read": {
			chain: "echo 'no backing file' >&2; exit 1", alone: "echo '" + image + "'",
			want: []qemuImage{{Filename: "/f/a.qc2", Format: "qcow2", VirtualSize: 1024, ActualSize: 512}},
		},
		"an empty chain": {
			chain: "echo '[]'", alone: "echo '" + image + "'",
			want: []qemuImage{{Filename: "/f/a.qc2", Format: "qcow2", VirtualSize: 1024, ActualSize: 512}},
		},
		"a chain that is not JSON": {
			chain: "echo 'not json'", alone: "echo '" + image + "'",
			wantErr: "parsing qemu-img info for /f/a.qc2: invalid character 'o' in literal null (expecting 'u')",
		},
		"an image qemu-img cannot read": {
			chain: "exit 1", alone: "echo 'could not open' >&2; exit 1", wantErr: "qemu-img info /f/a.qc2: could not open",
		},
		"an image that is not JSON": {
			chain: "exit 1", alone: "echo '['",
			wantErr: "parsing qemu-img info for /f/a.qc2: unexpected end of JSON input",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			localFakeQemuImg(t, "case \"$*\" in\n*--backing-chain*) "+tt.chain+" ;;\n*) "+tt.alone+" ;;\nesac\n")

			got, complete, err := inspectChain("/f/a.qc2")
			if fmt.Sprint(err) != tt.wantErr && (err != nil || tt.wantErr != "") {
				t.Errorf("inspectChain(%s) error = %v, want %q", name, err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" || complete != tt.complete {
				t.Errorf("inspectChain(%s) = _, %t, want %t; mismatch (-want +got):\n%s", name, complete, tt.complete, diff)
			}
		})
	}
}

// A chain that names a file phenix cannot stat is not kept, and fails the inspection.
func TestCachedChainOfAFileThatIsGone(t *testing.T) {
	ClearCache()
	t.Cleanup(ClearCache)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.qc2")
	writeFile(t, path, "a")

	localFakeQemuImg(t, `echo '[{"filename":"`+path+`","format":"qcow2"},{"filename":"gone.qc2","format":"qcow2"}]'`+"\n")

	if _, err := cachedChain(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("cachedChain(a.qc2 backed by a missing file) error = %v, want %v", err, fs.ErrNotExist)
	}

	cacheMu.Lock()
	_, kept := imageCache[path]
	cacheMu.Unlock()

	if kept {
		t.Errorf("cachedChain(a.qc2 backed by a missing file) kept the chain, want it not kept")
	}
}

// addBreak keeps a chain that qemu cannot open with a stamp of each file it read, and of the folder of a missing
// file, but not when it cannot stamp one, or when qemu can open the chain.
func TestAddBreak(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		// write writes the image at path in dir, and returns the paths that the stamps watch
		write   func(t *testing.T, dir, path string) []string
		want    bool
		problem diskchain.Kind
	}{
		"a whole chain": {
			write: func(t *testing.T, _, path string) []string {
				t.Helper()
				dct.WriteQcow2(t, path, dct.Qcow2{})

				return nil
			},
		},
		"a missing backing file": {
			write: func(t *testing.T, dir, path string) []string {
				t.Helper()
				dct.WriteQcow2(t, path, dct.Qcow2{Backing: "base.qc2"})

				return []string{dir}
			},
			want: true, problem: diskchain.Missing,
		},
		"a missing backing file in a missing folder": {
			write: func(t *testing.T, _, path string) []string {
				t.Helper()
				dct.WriteQcow2(t, path, dct.Qcow2{Backing: "gone/base.qc2"})

				return nil
			},
		},
		"a refused backing file": {
			write: func(t *testing.T, dir, path string) []string {
				t.Helper()
				dct.WriteQcow2(t, path, dct.Qcow2{Backing: "base.qc2"})
				dct.WriteQcow2(t, filepath.Join(dir, "base.qc2"), dct.Qcow2{Encryption: 2})

				return []string{filepath.Join(dir, "base.qc2")}
			},
			want: true, problem: diskchain.LUKSEncrypted,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "a.qc2")
			watched := tt.write(t, dir, path)

			var e cacheEntry
			if got := e.addBreak(path); got != tt.want {
				t.Errorf("addBreak(%s) = %t, want %t", name, got, tt.want)
			}

			if !tt.want {
				return
			}

			stamped := make([]string, 0, len(e.stamps))
			for _, s := range e.stamps {
				stamped = append(stamped, s.path)
			}

			if diff := cmp.Diff(watched, stamped); diff != "" || e.broken.Problem != tt.problem {
				t.Errorf("addBreak(%s) problem = %v, want %v; stamps mismatch (-want +got):\n%s", name, e.broken.Problem, tt.problem, diff)
			}
		})
	}
}

// chainPath keeps an absolute name, and joins a relative one onto the image's folder.
func TestChainPath(t *testing.T) {
	t.Parallel()

	tests := map[[2]string]string{
		{"/f/win/a.qc2", "/g/base.qc2"}: "/g/base.qc2",
		{"/f/win/a.qc2", "base.qc2"}:    "/f/win/base.qc2",
		{"/f/win/a.qc2", "../base.qc2"}: "/f/base.qc2",
		{"a.qc2", "base.qc2"}:           "base.qc2",
	}

	for in, want := range tests {
		if got := chainPath(in[0], in[1]); got != want {
			t.Errorf("chainPath(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// A stamp is current until its file is written or removed.
func TestStampsCurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.qc2")
	writeFile(t, path, "one")

	if _, err := stat(filepath.Join(dir, "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat(gone) error = %v, want %v", err, fs.ErrNotExist)
	}

	stamp, err := stat(path)
	if err != nil {
		t.Fatalf("stat(a.qc2) error = %v", err)
	}

	if stamp.path != path || stamp.size != 3 || stamp.inode == 0 || stamp.modTime.IsZero() {
		t.Errorf("stat(a.qc2) = %+v, want its path, size 3, inode and time", stamp)
	}

	if !stampsCurrent(nil) || !stampsCurrent([]fileStamp{stamp}) {
		t.Errorf("stampsCurrent(no stamps, or an unchanged file) = false, want true")
	}

	writeFile(t, path, "changed")

	if stampsCurrent([]fileStamp{stamp}) {
		t.Errorf("stampsCurrent(a written file) = true, want false")
	}

	if stampsCurrent([]fileStamp{{path: filepath.Join(dir, "gone")}}) {
		t.Errorf("stampsCurrent(a missing file) = true, want false")
	}
}

// lockedInodes reads the kernel's file locks where it has them, and fails where it does not.
func TestLockedInodes(t *testing.T) {
	t.Parallel()

	_, statErr := os.Stat("/proc/locks")

	locked, err := lockedInodes()

	switch {
	case statErr != nil && (!errors.Is(err, fs.ErrNotExist) || locked != nil):
		t.Errorf("lockedInodes() without /proc/locks = %v, %v; want nil, %v", locked, err, fs.ErrNotExist)
	case statErr == nil && (err != nil || locked == nil):
		t.Errorf("lockedInodes() = %v, %v; want the locked inodes", locked, err)
	}
}

// chainDetails gives the kind of each image by its format or name, its sizes, whether it is in use, the images below
// it, and the break of the chain on the image itself.
func TestChainDetails(t *testing.T) {
	t.Parallel()

	entry := cacheEntry{
		chain: []qemuImage{
			{Filename: "a.qc2", Format: "qcow2", VirtualSize: 1 << 30, ActualSize: 2048},
			{Filename: "kali_rootfs.tgz", Format: "raw", VirtualSize: 10, ActualSize: 10},
			{Filename: "win.hdd", Format: "raw"},
			{Filename: "tools.iso", Format: "raw"},
			{Filename: "base.img", Format: "vdi"},
		},
		stamps:  []fileStamp{{inode: 1}, {inode: 2}, {inode: 3}, {inode: 4}, {inode: 5}},
		paths:   []string{"/f/a.qc2", "/f/kali_rootfs.tgz", "/f/win.hdd", "/f/tools.iso", "/f/base.img"},
		headers: make([]imageHeader, 5),
		broken:  diskchain.Result{Members: []string{"/f/a.qc2"}, Problem: diskchain.Missing, At: "/f/gone.qc2"},
	}

	want := []Details{
		{
			Kind: VMImage, Name: "a.qc2", FullPath: "/f/a.qc2", Size: "2.0 KiB", VirtualSize: "1.0 GiB",
			BackingImages: []string{"/f/kali_rootfs.tgz", "/f/win.hdd", "/f/tools.iso", "/f/base.img"}, InUse: true,
			BrokenChain: true, ChainProblem: "backing file /f/gone.qc2 is missing", MissingBacking: "/f/gone.qc2",
			Format: "qcow2",
		},
		{
			Kind: ContainerImage, Name: "kali_rootfs.tgz", FullPath: "/f/kali_rootfs.tgz", Size: "10 B", VirtualSize: "10 B",
			BackingImages: []string{}, Format: "raw",
		},
		{
			Kind: VMImage, Name: "win.hdd", FullPath: "/f/win.hdd", Size: "0 B", VirtualSize: "0 B",
			BackingImages: []string{}, InUse: true, Format: "raw",
		},
		{
			Kind: ISOImage, Name: "tools.iso", FullPath: "/f/tools.iso", Size: "0 B", VirtualSize: "0 B",
			BackingImages: []string{}, Format: "raw",
		},
		{
			Kind: UNKNOWN, Name: "base.img", FullPath: "/f/base.img", Size: "0 B", VirtualSize: "0 B",
			BackingImages: []string{}, Format: "vdi", FormatIssue: uncheckedIssue("/f/base.img", "/f/base.img", "vdi"),
		},
	}

	if diff := cmp.Diff(want, chainDetails(entry, map[uint64]bool{1: true, 3: true})); diff != "" {
		t.Errorf("chainDetails() mismatch (-want +got):\n%s", diff)
	}

	if got := chainDetails(cacheEntry{}, nil); got == nil || len(got) != 0 {
		t.Errorf("chainDetails(no chain) = %#v, want an empty list", got)
	}
}
