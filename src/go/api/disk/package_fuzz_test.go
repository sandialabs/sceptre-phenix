package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// packageFuzzOracle decides from the files themselves which paths the disk
// actions in dir may pass on to DiskFiles, and as what path.
type packageFuzzOracle struct{ dir string }

// minimega returns the path of an existing image minimega may act on.
func (o packageFuzzOracle) minimega(p string) (string, bool) {
	path, ok := pathsFuzzResolve(o.dir, p)
	if !ok || ValidateMinimegaPath(path) != nil {
		return "", false
	}

	info, err := os.Stat(path)

	return path, err == nil && info.Mode().IsRegular()
}

// regular returns the path of an existing image phenix may read.
func (o packageFuzzOracle) regular(p string) (string, bool) {
	path, ok := pathsFuzzResolve(o.dir, p)
	if !ok {
		return "", false
	}

	info, err := os.Stat(path)

	return path, err == nil && info.Mode().IsRegular()
}

// entry returns the path of an existing entry, not a folder, phenix may act on.
func (o packageFuzzOracle) entry(p string) (string, bool) {
	path, ok := pathsFuzzResolve(o.dir, p)
	if !ok {
		return "", false
	}

	info, err := os.Lstat(path)

	return path, err == nil && !info.IsDir()
}

// fresh returns the path of a new image: minimega may take it, nothing is
// there yet, and its folder exists.
func (o packageFuzzOracle) fresh(p string) (string, bool) {
	path, ok := pathsFuzzResolve(o.dir, p)
	if !ok || ValidateMinimegaPath(path) != nil {
		return "", false
	}

	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return "", false
	}

	info, err := os.Stat(filepath.Dir(path))

	return path, err == nil && info.IsDir()
}

// The disk actions pass DiskFiles exactly the calls an oracle, which reads
// the files itself, expects: only the cleaned paths of images in the files
// directory, never an existing target, and for minimega only names it can
// take. Each refused action returns an error.
func FuzzActionsPassOnlyCheckedPaths(f *testing.F) {
	mmtest.Use(f, nil)

	original := common.PhenixBase
	common.PhenixBase = f.TempDir() //nolint:reassign // the files directory's parent

	f.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	dir := mm.GetMMFullPath("")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		f.Fatalf("os.MkdirAll(%s) error = %v", dir, err)
	}

	writeActionFiles(f, dir)

	fake := useRecordingDisks(f)
	base := filepath.Join(dir, "b.qc2")

	for _, seed := range [][2]string{
		{"a.qc2", "n.qc2"}, {"a.qc2", ""}, {"a.qc2", "b.qc2"}, {"win/../a.qc2", "win/./n.qc2"}, {"../a.qc2", "n.qc2"},
		{"a.qc2", "../n.qc2"}, {"$x.qc2", "n.qc2"}, {"a.qc2", "n,1.qc2"}, {"win", "n.qc2"}, {"dangling.qc2", "n.qc2"},
		{"link.qc2", "dangling.qc2"}, {"a.qc2", "plain/n.qc2"}, {"a.qc2", "gone/n.qc2"}, {"exp/files/s.hdd", "n.qc2"},
		{"a.qc2", "exp/files/n.qc2"}, {dir + "/a.qc2", dir + "/win/n.qc2"}, {"/etc/hosts", "n.qc2"}, {"", ""},
		{"a.qc2", dir}, {"a.qc2", "win"}, {"win/c.qc2", "a b.qc2"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, src, dst string) {
		o := packageFuzzOracle{dir: dir}

		var want []string

		expect := func(ok bool, format string, args ...any) {
			if ok {
				want = append(want, fmt.Sprintf(format, args...))
			}
		}

		mmSrc, mmSrcOK := o.minimega(src)
		mmDst, mmDstOK := o.minimega(dst)
		regSrc, regSrcOK := o.regular(src)
		entSrc, entSrcOK := o.entry(src)
		newDst, newDstOK := o.fresh(dst)

		fake.calls = nil
		fake.images[mmSrc] = Details{BackingImages: []string{base}}

		t.Cleanup(func() { delete(fake.images, mmSrc) })

		actions := []struct {
			name string
			ok   bool
			act  func() error
		}{
			{"CommitDisk", mmSrcOK, func() error { return CommitDisk(src) }},
			{"SnapshotDisk", mmSrcOK && newDstOK, func() error { return SnapshotDisk(src, dst) }},
			{"RebaseDisk", mmSrcOK && (dst == "" || mmDstOK), func() error { return RebaseDisk(src, dst, false) }},
			{"ResizeDisk", mmSrcOK, func() error { return ResizeDisk(src, "+1G") }},
			{"CloneDisk", regSrcOK && newDstOK, func() error { return CloneDisk(src, dst) }},
			{"RenameDisk", entSrcOK && newDstOK, func() error { return RenameDisk(src, dst) }},
			{"DeleteDisk", entSrcOK, func() error { return DeleteDisk(src, true) }},
		}

		expect(mmSrcOK, "commit %s", mmSrc)
		expect(mmSrcOK && newDstOK, "snapshot %s %s", mmSrc, newDst)
		expect(mmSrcOK && (dst == "" || mmDstOK), "rebase %s %q false", mmSrc, mmDst)
		expect(mmSrcOK, "resize %s +1G", mmSrc)
		expect(regSrcOK && newDstOK, "clone %s %s", regSrc, newDst)
		expect(entSrcOK && newDstOK, "rename %s %s", entSrc, newDst)
		expect(entSrcOK, "delete %s", entSrc)

		for _, a := range actions {
			if err := a.act(); (err == nil) != a.ok {
				t.Errorf("%s(%q, %q) error = %v, want an error: %t", a.name, src, dst, err, !a.ok)
			}
		}

		if diff := cmp.Diff(want, fake.calls); diff != "" {
			t.Fatalf("actions on (%q, %q) DiskFiles calls mismatch (-want +got):\n%s", src, dst, diff)
		}
	})
}
