package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	dct "phenix/util/diskchain/diskchaintest"
)

// recordingDisks is a DiskFiles that records each action it receives and
// answers GetImage from images; GetImages panics through the nil embedded
// interface.
type recordingDisks struct {
	DiskFiles

	images map[string]Details
	calls  []string
}

func (r *recordingDisks) GetImage(path string) (Details, error) {
	if d, ok := r.images[path]; ok {
		return d, nil
	}

	return Details{FullPath: path}, nil
}

func (r *recordingDisks) recordf(format string, args ...any) error {
	r.calls = append(r.calls, fmt.Sprintf(format, args...))

	return nil
}

func (r *recordingDisks) CommitDisk(path string) error { return r.recordf("commit %s", path) }

func (r *recordingDisks) SnapshotDisk(src, dst string) error {
	return r.recordf("snapshot %s %s", src, dst)
}

func (r *recordingDisks) RebaseDisk(src, dst string, unsafe bool) error {
	return r.recordf("rebase %s %q %t", src, dst, unsafe)
}

func (r *recordingDisks) ResizeDisk(src, size string) error {
	return r.recordf("resize %s %s", src, size)
}

func (r *recordingDisks) CloneDisk(src, dst string) error { return r.recordf("clone %s %s", src, dst) }

func (r *recordingDisks) RenameDisk(src, dst string) error {
	return r.recordf("rename %s %s", src, dst)
}

func (r *recordingDisks) DeleteDisk(src string) error { return r.recordf("delete %s", src) }

// useRecordingDisks makes a new recordingDisks DefaultDiskFiles until the
// test ends.
func useRecordingDisks(tb testing.TB) *recordingDisks {
	tb.Helper()

	fake := &recordingDisks{DiskFiles: nil, images: map[string]Details{}, calls: nil}

	original := DefaultDiskFiles
	DefaultDiskFiles = fake

	tb.Cleanup(func() { DefaultDiskFiles = original })

	return fake
}

// writeActionFiles writes in dir, the files directory, the files the action
// tests act on: images a.qc2 and b.qc2, $x.qc2 (a name minimega cannot
// take), a folder win, a plain file, a symlink to a.qc2, a dangling symlink,
// and an image in a skipped folder.
func writeActionFiles(tb testing.TB, dir string) {
	tb.Helper()

	for _, d := range []string{"win", "exp/files"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			tb.Fatalf("os.MkdirAll(%s) error = %v", d, err)
		}
	}

	for name, content := range map[string]string{
		"a.qc2": "aaaa", "b.qc2": "b", "$x.qc2": "x", "plain": "p", "win/c.qc2": "c", "exp/files/s.hdd": "s",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			tb.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
	}

	for name, target := range map[string]string{"link.qc2": "a.qc2", "dangling.qc2": "gone.qc2"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			tb.Fatalf("os.Symlink(%s, %s) error = %v", target, name, err)
		}
	}
}

// The package's disk actions pass DiskFiles the cleaned absolute paths of
// the images, relative or absolute, and refuse paths outside the files
// directory or in a skipped folder, a missing source, a folder, an existing
// target, a target in no folder, and names minimega cannot take.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestActionsCheckPaths(t *testing.T) {
	dir := useFilesDir(t)
	writeActionFiles(t, dir)

	a, b := filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2")
	n := filepath.Join(dir, "n.qc2")

	tests := []struct {
		name    string
		act     func() error
		wantErr error
		want    []string
	}{
		{"snapshot relative", func() error { return SnapshotDisk("a.qc2", "n.qc2") }, nil, []string{"snapshot " + a + " " + n}},
		{"snapshot absolute", func() error { return SnapshotDisk(a, n) }, nil, []string{"snapshot " + a + " " + n}},
		{
			"snapshot through a folder", func() error { return SnapshotDisk("win/../a.qc2", "win/./n.qc2") }, nil,
			[]string{"snapshot " + a + " " + filepath.Join(dir, "win", "n.qc2")},
		},
		{"snapshot from above", func() error { return SnapshotDisk("../a.qc2", "n.qc2") }, ErrNotManaged, nil},
		{"snapshot to above", func() error { return SnapshotDisk("a.qc2", "win/../../n.qc2") }, ErrNotManaged, nil},
		{"snapshot from nothing", func() error { return SnapshotDisk("", "n.qc2") }, ErrNotManaged, nil},
		{"snapshot from the files directory", func() error { return SnapshotDisk(dir, "n.qc2") }, ErrNotManaged, nil},
		{"snapshot from a skipped folder", func() error { return SnapshotDisk("exp/files/s.hdd", "n.qc2") }, ErrNotManaged, nil},
		{"snapshot to a skipped folder", func() error { return SnapshotDisk("a.qc2", "exp/files/n.qc2") }, ErrNotManaged, nil},
		{"snapshot from a folder", func() error { return SnapshotDisk("win", "n.qc2") }, ErrNotManaged, nil},
		{"snapshot from a missing image", func() error { return SnapshotDisk("gone.qc2", "n.qc2") }, fs.ErrNotExist, nil},
		{"snapshot from a dangling symlink", func() error { return SnapshotDisk("dangling.qc2", "n.qc2") }, fs.ErrNotExist, nil},
		{"snapshot onto an image", func() error { return SnapshotDisk("a.qc2", "b.qc2") }, ErrExists, nil},
		{"snapshot onto a dangling symlink", func() error { return SnapshotDisk("a.qc2", "dangling.qc2") }, ErrExists, nil},
		{"snapshot into a missing folder", func() error { return SnapshotDisk("a.qc2", "gone/n.qc2") }, fs.ErrNotExist, nil},
		{"snapshot into a file", func() error { return SnapshotDisk("a.qc2", "plain/n.qc2") }, ErrNotManaged, nil},
		{"snapshot under a file", func() error { return SnapshotDisk("a.qc2", "plain/sub/n.qc2") }, ErrNotManaged, nil},
		{"snapshot from an unsafe name", func() error { return SnapshotDisk("$x.qc2", "n.qc2") }, ErrUnsafeName, nil},
		{"snapshot to an unsafe name", func() error { return SnapshotDisk("a.qc2", "n,1.qc2") }, ErrUnsafeName, nil},
		{"snapshot through a symlink", func() error { return SnapshotDisk("link.qc2", "n.qc2") }, nil, []string{
			"snapshot " + filepath.Join(dir, "link.qc2") + " " + n,
		}},

		{"rebase onto an image", func() error { return RebaseDisk("a.qc2", "b.qc2", true) }, nil, []string{
			fmt.Sprintf("rebase %s %q true", a, b),
		}},
		{"rebase to independent", func() error { return RebaseDisk("a.qc2", "", false) }, nil, []string{
			fmt.Sprintf("rebase %s %q false", a, ""),
		}},
		{"rebase onto a missing image", func() error { return RebaseDisk("a.qc2", "gone.qc2", false) }, fs.ErrNotExist, nil},
		{"rebase onto an image above", func() error { return RebaseDisk("a.qc2", "../b.qc2", false) }, ErrNotManaged, nil},
		{"rebase onto an unsafe name", func() error { return RebaseDisk("a.qc2", "$x.qc2", false) }, ErrUnsafeName, nil},
		{"rebase a missing image", func() error { return RebaseDisk("gone.qc2", "b.qc2", false) }, fs.ErrNotExist, nil},

		{"resize", func() error { return ResizeDisk("a.qc2", "+1G") }, nil, []string{"resize " + a + " +1G"}},
		{"resize a missing image", func() error { return ResizeDisk("gone.qc2", "+1G") }, fs.ErrNotExist, nil},
		{"resize a folder", func() error { return ResizeDisk("win", "+1G") }, ErrNotManaged, nil},

		{"clone", func() error { return CloneDisk("a.qc2", "n.qc2") }, nil, []string{"clone " + a + " " + n}},
		{"clone an unsafe name", func() error { return CloneDisk("$x.qc2", "n.qc2") }, nil, []string{
			"clone " + filepath.Join(dir, "$x.qc2") + " " + n,
		}},
		{"clone to an unsafe name", func() error { return CloneDisk("a.qc2", "$n.qc2") }, ErrUnsafeName, nil},
		{"clone onto an image", func() error { return CloneDisk("a.qc2", "b.qc2") }, ErrExists, nil},
		{"clone a folder", func() error { return CloneDisk("win", "n.qc2") }, ErrNotManaged, nil},
		{"clone a missing image", func() error { return CloneDisk("gone.qc2", "n.qc2") }, fs.ErrNotExist, nil},
		{"clone from elsewhere", func() error { return CloneDisk("/etc/hosts", "n.qc2") }, ErrNotManaged, nil},

		{"rename", func() error { return RenameDisk("a.qc2", "n.qc2") }, nil, []string{"rename " + a + " " + n}},
		{"rename an unsafe name", func() error { return RenameDisk("$x.qc2", "n.qc2") }, nil, []string{
			"rename " + filepath.Join(dir, "$x.qc2") + " " + n,
		}},
		{"rename a dangling symlink", func() error { return RenameDisk("dangling.qc2", "n.qc2") }, nil, []string{
			"rename " + filepath.Join(dir, "dangling.qc2") + " " + n,
		}},
		{"rename to an unsafe name", func() error { return RenameDisk("a.qc2", "$n.qc2") }, ErrUnsafeName, nil},
		{"rename onto an image", func() error { return RenameDisk("a.qc2", "b.qc2") }, ErrExists, nil},
		{"rename a folder", func() error { return RenameDisk("win", "n.qc2") }, ErrNotManaged, nil},
		{"rename a missing image", func() error { return RenameDisk("gone.qc2", "n.qc2") }, fs.ErrNotExist, nil},
		{"rename from above", func() error { return RenameDisk("../a.qc2", "n.qc2") }, ErrNotManaged, nil},

		{"delete by force", func() error { return DeleteDisk("a.qc2", true) }, nil, []string{"delete " + a}},
		{"delete a folder", func() error { return DeleteDisk("win", true) }, ErrNotManaged, nil},
		{"delete a missing image", func() error { return DeleteDisk("gone.qc2", true) }, fs.ErrNotExist, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := useRecordingDisks(t)

			err := tc.act()

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s error = %v, want %v", tc.name, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, fake.calls); diff != "" {
				t.Errorf("%s DiskFiles calls mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// CommitDisk commits an image only into a backing image the disk actions act
// on, and refuses an image with no backing image or one qemu refuses.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestCommitDiskChecksTheBackingImage(t *testing.T) {
	dir := useFilesDir(t)
	writeActionFiles(t, dir)

	a, b, c := filepath.Join(dir, "a.qc2"), filepath.Join(dir, "b.qc2"), filepath.Join(dir, "win", "c.qc2")

	tests := []struct {
		name    string
		path    string
		details Details
		wantErr error
		fails   bool // with an error wrapping no sentinel
		want    []string
	}{
		{name: "backed", path: "a.qc2", details: Details{BackingImages: []string{b}}, want: []string{"commit " + a}},
		{name: "no backing image", path: "a.qc2", fails: true},
		{name: "backed from above", path: "a.qc2", details: Details{BackingImages: []string{"/etc/base.qc2"}}, wantErr: ErrNotManaged},
		{name: "backed from a skipped folder", path: "win/c.qc2", details: Details{
			BackingImages: []string{filepath.Join(dir, "exp", "files", "s.hdd")},
		}, wantErr: ErrNotManaged},
		{name: "refused", path: "a.qc2", details: Details{
			BackingImages: []string{b},
			FormatIssue:   &FormatIssue{Level: IssueError, Problem: "qemu refuses it"},
		}, fails: true},
		{name: "a missing image", path: "gone.qc2", wantErr: fs.ErrNotExist},
		{name: "an unsafe name", path: "$x.qc2", wantErr: ErrUnsafeName},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := useRecordingDisks(t)
			fake.images[a] = tc.details
			fake.images[c] = tc.details

			err := CommitDisk(tc.path)

			switch {
			case tc.fails:
				if err == nil {
					t.Errorf("CommitDisk(%s) error = nil, want an error", tc.path)
				}
			case !errors.Is(err, tc.wantErr):
				t.Errorf("CommitDisk(%s) error = %v, want %v", tc.path, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, fake.calls); diff != "" {
				t.Errorf("CommitDisk(%s) DiskFiles calls mismatch (-want +got):\n%s", tc.path, diff)
			}
		})
	}
}

// Committing, a safe rebase and resizing refuse an image whose chain qemu would read or write outside the files
// directory: a backing image there or above it, an external data file, a vmdk image, which can name any host file, or
// a backing file named by a protocol. A missing backing file inside is left for qemu to report.
// A commit reads the image and writes its backing image, a safe rebase reads both whole chains, and a resize writes
// only the image, so a chain may leave the files directory below those.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestChainActionsStayInFilesDirectory(t *testing.T) {
	dir := useFilesDir(t)
	outside := t.TempDir()
	at := func(name string) string { return filepath.Join(dir, name) }

	writeFile(t, outside+"/secret", "secret")
	writeFile(t, filepath.Dir(dir)+"/secret", "secret")
	writeFile(t, at("base.qc2"), "base")
	writeFile(t, at("extents.qc2"), "# Disk DescriptorFile\nRW 8 FLAT \"/etc/shadow\" 0\n")

	for name, q := range map[string]dct.Qcow2{
		"in.qc2":   {Backing: "base.qc2"},
		"off.qc2":  {Backing: outside + "/secret", BackingFormat: "raw"},
		"up.qc2":   {Backing: "../secret"},
		"mid.qc2":  {Backing: outside + "/secret"},
		"top.qc2":  {Backing: "mid.qc2"},
		"data.qc2": {DataFile: true},
		"vmdk.qc2": {Backing: "extents.qc2"},
		"lost.qc2": {Backing: outside + "/gone"},
		"gone.qc2": {Backing: "gone"},
		"nbd.qc2":  {Backing: "nbd://host/x"},
	} {
		dct.WriteQcow2(t, at(name), q)
	}

	rebase := func(src, dst string, unsafe bool) []string {
		return []string{fmt.Sprintf("rebase %s %q %t", at(src), dst, unsafe)}
	}

	tests := []struct {
		name    string
		act     func() error
		backing string // the backing image DiskFiles reports for a commit
		wantErr error
		want    []string
	}{
		{"commit into an image inside", func() error { return CommitDisk("top.qc2") }, "mid.qc2", nil, []string{"commit " + at("top.qc2")}},
		{"commit an image with an external data file", func() error { return CommitDisk("data.qc2") }, "base.qc2", ErrNotManaged, nil},
		{"commit into a vmdk image", func() error { return CommitDisk("vmdk.qc2") }, "extents.qc2", ErrNotManaged, nil},

		{"rebase off a chain inside", func() error { return RebaseDisk("in.qc2", "", false) }, "", nil, rebase("in.qc2", "", false)},
		{"rebase off an image outside", func() error { return RebaseDisk("off.qc2", "", false) }, "", ErrNotManaged, nil},
		{"rebase off an image above", func() error { return RebaseDisk("up.qc2", "", false) }, "", ErrNotManaged, nil},
		{"rebase off a chain leaving below", func() error { return RebaseDisk("top.qc2", "", false) }, "", ErrNotManaged, nil},
		{"rebase off a vmdk image", func() error { return RebaseDisk("vmdk.qc2", "", false) }, "", ErrNotManaged, nil},
		{"rebase onto a chain leaving", func() error { return RebaseDisk("in.qc2", "mid.qc2", false) }, "", ErrNotManaged, nil},
		{"rebase off a missing image outside", func() error { return RebaseDisk("lost.qc2", "", false) }, "", ErrNotManaged, nil},
		{"rebase off a network backing file", func() error { return RebaseDisk("nbd.qc2", "", false) }, "", ErrNotManaged, nil},
		{
			"rebase off a missing image inside", func() error { return RebaseDisk("gone.qc2", "", false) }, "", nil,
			rebase("gone.qc2", "", false),
		},
		{
			"rebase unsafe off an image outside", func() error { return RebaseDisk("off.qc2", "", true) }, "", nil,
			rebase("off.qc2", "", true),
		},

		{"resize an image backed from outside", func() error { return ResizeDisk("off.qc2", "+1G") }, "", nil, []string{
			"resize " + at("off.qc2") + " +1G",
		}},
		{"resize an image with an external data file", func() error { return ResizeDisk("data.qc2", "+1G") }, "", ErrNotManaged, nil},
		{"resize a vmdk image", func() error { return ResizeDisk("extents.qc2", "+1G") }, "", ErrNotManaged, nil},
		{"resize an image with a network backing file", func() error { return ResizeDisk("nbd.qc2", "+1G") }, "", nil, []string{
			"resize " + at("nbd.qc2") + " +1G",
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := useRecordingDisks(t)

			for _, name := range []string{"top.qc2", "data.qc2", "vmdk.qc2"} {
				fake.images[at(name)] = Details{BackingImages: []string{at(tc.backing)}}
			}

			if err := tc.act(); !errors.Is(err, tc.wantErr) {
				t.Errorf("%s error = %v, want %v", tc.name, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, fake.calls); diff != "" {
				t.Errorf("%s DiskFiles calls mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// CloneSize checks a clone as CloneDisk does and returns the size of the
// image it follows symlinks to, without asking DiskFiles to clone.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestCloneSize(t *testing.T) {
	dir := useFilesDir(t)
	writeActionFiles(t, dir)

	fake := useRecordingDisks(t)

	tests := []struct {
		src, dst string
		want     int64
		wantErr  error
	}{
		{src: "a.qc2", dst: "n.qc2", want: 4},
		{src: "link.qc2", dst: "win/n.qc2", want: 4},
		{src: filepath.Join(dir, "$x.qc2"), dst: "n.qc2", want: 1},
		{src: "a.qc2", dst: "b.qc2", wantErr: ErrExists},
		{src: "a.qc2", dst: "$n.qc2", wantErr: ErrUnsafeName},
		{src: "a.qc2", dst: "../n.qc2", wantErr: ErrNotManaged},
		{src: "gone.qc2", dst: "n.qc2", wantErr: fs.ErrNotExist},
		{src: "win", dst: "n.qc2", wantErr: ErrNotManaged},
		{src: "../a.qc2", dst: "n.qc2", wantErr: ErrNotManaged},
	}

	for _, tc := range tests {
		got, err := CloneSize(tc.src, tc.dst)
		if got != tc.want || !errors.Is(err, tc.wantErr) {
			t.Errorf("CloneSize(%s, %s) = %d, %v; want %d, %v", tc.src, tc.dst, got, err, tc.want, tc.wantErr)
		}
	}

	if len(fake.calls) != 0 {
		t.Errorf("CloneSize DiskFiles calls = %q, want none", fake.calls)
	}
}

// GetImage asks DiskFiles about a regular file only, relative to the files
// directory or absolute.
//
//nolint:paralleltest // replaces DefaultDiskFiles and the files directory
func TestGetImageChecksTheFile(t *testing.T) {
	dir := useFilesDir(t)
	writeActionFiles(t, dir)

	fake := useRecordingDisks(t)
	a := filepath.Join(dir, "a.qc2")
	fake.images[a] = Details{Name: "a.qc2", FullPath: a}

	for _, p := range []string{"a.qc2", a} {
		got, err := GetImage(p)
		if err != nil || got.FullPath != a {
			t.Errorf("GetImage(%s) = %q, %v; want %q, nil", p, got.FullPath, err, a)
		}
	}

	for p, want := range map[string]error{"win": ErrNotManaged, "gone.qc2": fs.ErrNotExist} {
		if _, err := GetImage(p); !errors.Is(err, want) {
			t.Errorf("GetImage(%s) error = %v, want %v", p, err, want)
		}
	}
}
