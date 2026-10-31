package disk

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/testenv"
)

// useFilesDir makes a new <PhenixBase>/images the files directory until the test ends,
// with no minimega answering and an empty experiment store, and returns it.
func useFilesDir(t *testing.T) string {
	t.Helper()

	mmtest.Use(t, nil)
	storetest.Use(t)

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	dir := filepath.Join(base, "images")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("os.Mkdir(%s) error = %v", dir, err)
	}

	return dir
}

// storeExperiments adds the experiments to the store.
func storeExperiments(t *testing.T, exps ...types.Experiment) {
	t.Helper()

	for _, exp := range exps {
		err := store.Create(&store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: exp.Metadata,
			Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
			Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
		})
		if err != nil {
			t.Fatalf("store.Create(experiment %s) error = %v", exp.Metadata.Name, err)
		}
	}
}

// writeImages writes a file at each path, holding the path, making the
// folders it is in.
func writeImages(t *testing.T, paths ...string) {
	t.Helper()

	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(p), err)
		}

		writeFile(t, p, p)
	}
}

// listed returns the images GetImages lists, by full path.
func listed(t *testing.T, expName string) map[string]Details {
	t.Helper()

	images, err := GetImages(expName)
	if err != nil {
		t.Fatalf("GetImages(%q) error = %v", expName, err)
	}

	byPath := make(map[string]Details, len(images))

	for _, image := range images {
		if _, ok := byPath[image.FullPath]; ok {
			t.Fatalf("GetImages(%q) lists %s twice", expName, image.FullPath)
		}

		byPath[image.FullPath] = image
	}

	return byPath
}

// The listing searches the files directory's folders down to maxDepth, leaving out
// what experiments and minimega keep there and every file that is not an image.
func TestGetImagesSearchesFolders(t *testing.T) {
	for name, symlinked := range map[string]bool{"files directory": false, "files directory is a symlink": true} {
		t.Run(name, func(t *testing.T) {
			useFakeQemuImg(t)

			dir := useFilesDir(t)
			outside := t.TempDir()

			folder := dir
			if symlinked {
				folder = filepath.Join(filepath.Dir(dir), "real-images")
				if err := os.Rename(dir, folder); err != nil {
					t.Fatalf("os.Rename(%s, %s) error = %v", dir, folder, err)
				}

				if err := os.Symlink(folder, dir); err != nil {
					t.Fatalf("os.Symlink(%s, %s) error = %v", folder, dir, err)
				}
			}

			deep := strings.Repeat("d/", maxDepth)

			kept := []string{
				"a.qc2", "linux/a.qc2", "win/win10.qcow2", "win/10/deep.qc2", "tools.iso",
				"files/kept.qc2", "exp1/kept.qc2", "a/b/files/kept.qc2", deep + "at-limit.qc2",
			}

			for _, rel := range append(slices.Clone(kept),
				"exp1/files/s.hdd", "exp1/tmp/snapshot-1.qc2", "exp1/miniccc_responses/x.qc2",
				".hidden/x.qc2", ".x.qc2", "lost+found/x.qc2", "win/lost+found/x.qc2",
				"saved/vm/x.hdd", "transfer_1/x.qc2", "notes.txt", "h_e_v_snapshot",
				deep+"d/too-deep.qc2",
			) {
				writeImages(t, filepath.Join(folder, rel))
			}

			writeImages(t, filepath.Join(outside, "o.qc2"))

			for link, target := range map[string]string{
				"loop":     "..",
				"ext":      outside,
				"link.qc2": "win/win10.qcow2",
				"gone.qc2": "missing.qc2",
			} {
				if err := os.Symlink(target, filepath.Join(folder, link)); err != nil {
					t.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
				}
			}

			if err := syscall.Mkfifo(filepath.Join(folder, "pipe.qc2"), 0o600); err != nil {
				t.Fatalf("syscall.Mkfifo(pipe.qc2) error = %v", err)
			}

			// one folder phenix cannot read leaves the rest listed; root reads it
			if os.Geteuid() != 0 {
				writeImages(t, filepath.Join(folder, "locked", "x.qc2"))

				if err := os.Chmod(filepath.Join(folder, "locked"), 0); err != nil {
					t.Fatalf("os.Chmod(locked, 0) error = %v", err)
				}

				t.Cleanup(func() { _ = os.Chmod(filepath.Join(folder, "locked"), 0o750) })
			}

			want := make(map[string]Details, len(kept)+1)
			for _, rel := range slices.Concat(kept, []string{"link.qc2"}) {
				path := filepath.Join(dir, rel)
				want[path] = Details{Kind: VMImage, Name: filepath.Base(rel), FullPath: path, RelativePath: rel}
			}

			tools := want[filepath.Join(dir, "tools.iso")]
			tools.Kind = ISOImage
			want[tools.FullPath] = tools

			ignore := cmpopts.IgnoreFields(Details{}, "Size", "VirtualSize", "Experiments", "BackingImages", "InUse", "Format")
			if diff := cmp.Diff(want, listed(t, ""), ignore); diff != "" {
				t.Errorf("GetImages(%q) mismatch (-want +got):\n%s", "", diff)
			}
		})
	}
}

// Topology images of any path or extension are listed with their backing images when
// readable, and matched to the experiments using them by full path.
func TestGetImagesListsTopologyImages(t *testing.T) {
	calls := useFakeQemuImg(t)

	dir := useFilesDir(t)
	outside := t.TempDir()

	writeImages(t,
		filepath.Join(dir, "base.qc2"),
		filepath.Join(dir, "win", "child.qc2"),
		filepath.Join(dir, "other", "base.qc2"),
		filepath.Join(dir, "sub", "x.qc2"),
		filepath.Join(dir, "exp-a", "files", "snap.hdd"),
		filepath.Join(outside, "o.img"),
		filepath.Join(outside, "b-only.qc2"),
	)

	writeFile(t, filepath.Join(dir, "win", "child.qc2.backing"), "../base.qc2")

	if err := syscall.Mkfifo(filepath.Join(outside, "pipe.qc2"), 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(pipe.qc2) error = %v", err)
	}

	storeExperiments(t,
		testExperiment(t, "exp-a", "2026-09-30T00:00:00Z",
			"win/child.qc2", outside+"/o.img", "sub/./x.qc2", "exp-a/files/snap.hdd",
			outside+"/missing.qc2", outside+"/pipe.qc2", "/proc/self/status", ""),
		testExperiment(t, "exp-b", "", outside+"/b-only.qc2", dir+"/other/../other/base.qc2"),
	)

	var (
		expA = ExperimentUse{Name: "exp-a", Running: true}
		expB = ExperimentUse{Name: "exp-b", Running: false}
	)

	inside := func(rel string, readOnly bool, kind Kind, backing []string, uses ...ExperimentUse) Details {
		path := dir + "/" + rel

		return Details{
			Kind: kind, Name: filepath.Base(rel), FullPath: path, RelativePath: rel,
			ReadOnly: readOnly, BackingImages: backing, Experiments: uses,
		}
	}

	away := func(name string, kind Kind, uses ...ExperimentUse) Details {
		path := outside + "/" + name

		return Details{
			Kind: kind, Name: name, FullPath: path, OutsideFilesDir: true, ReadOnly: true, Experiments: uses,
		}
	}

	want := map[string]Details{
		dir + "/base.qc2":             inside("base.qc2", false, VMImage, nil, expA),
		dir + "/win/child.qc2":        inside("win/child.qc2", false, VMImage, []string{dir + "/base.qc2"}, expA),
		dir + "/other/base.qc2":       inside("other/base.qc2", false, VMImage, nil, expB),
		dir + "/sub/x.qc2":            inside("sub/x.qc2", false, VMImage, nil, expA),
		dir + "/exp-a/files/snap.hdd": inside("exp-a/files/snap.hdd", true, VMImage, nil, expA),
		outside + "/o.img":            away("o.img", UNKNOWN, expA),
		outside + "/b-only.qc2":       away("b-only.qc2", VMImage, expB),
	}

	opts := cmp.Options{cmpopts.IgnoreFields(Details{}, "Size", "VirtualSize", "InUse", "Format"), cmpopts.EquateEmpty()}
	if diff := cmp.Diff(want, listed(t, ""), opts); diff != "" {
		t.Errorf("GetImages(%q) mismatch (-want +got):\n%s", "", diff)
	}

	// once per image phenix can read, and never for the others
	if got := calls(); got != len(want) {
		t.Errorf("qemu-img runs for GetImages(%q) = %d, want %d", "", got, len(want))
	}

	// one experiment's listing leaves out the images only others name
	images := listed(t, "exp-b")

	if _, ok := images[outside+"/o.img"]; ok {
		t.Errorf("GetImages(%q) lists o.img, which only exp-a names", "exp-b")
	}

	if diff := cmp.Diff([]ExperimentUse{expB}, images[outside+"/b-only.qc2"].Experiments); diff != "" {
		t.Errorf("GetImages(%q) b-only.qc2 Experiments mismatch (-want +got):\n%s", "exp-b", diff)
	}

	if _, err := GetImages("nope"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("GetImages(%q) error = %v, want %v", "nope", err, store.ErrNotExist)
	}
}

// Without qemu-img, minimega lists and inspects the files directory folder by folder; phenix
// asks only about paths it can name in one argument, not what it sees as non-regular files.
func TestGetImagesThroughMinimega(t *testing.T) {
	dir := useFilesDir(t)
	outside := t.TempDir()

	t.Setenv("PATH", t.TempDir())

	writeImages(t, filepath.Join(outside, "o.img"), filepath.Join(outside, "my o.qc2"), dir+"/exp1/kept.qc2")
	storeExperiments(t, testExperiment(t, "exp", "", outside+"/o.img", outside+"/my o.qc2"))

	// qemu-img would block on the FIFO, holding up minimega; a.qc2 and the
	// other images phenix cannot see are still inspected
	if err := syscall.Mkfifo(dir+"/pipe.qc2", 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(pipe.qc2) error = %v", err)
	}

	for link, target := range map[string]string{"gone.qc2": "nowhere.qc2", "win/link.qc2": "../exp1/kept.qc2"} {
		if err := os.MkdirAll(filepath.Dir(dir+"/"+link), 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(link), err)
		}

		if err := os.Symlink(target, dir+"/"+link); err != nil {
			t.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
		}
	}

	entry := func(dir, name string) []string { return []string{dir, name, "0", "2026-09-30T00:00:00Z"} }

	var (
		listing = []string{"dir", "name", "size", "modified"}
		info    = []string{"image", "format", "virtualsize", "disksize", "backingfile", "inuse"}
		folders = map[string][][]string{
			"file list": {
				entry("<dir>", "win"), entry("<dir>", "exp1"), entry("<dir>", "my dir"), entry("<dir>", "file:x"),
				entry("<dir>", ".hidden"), entry("", "a.qc2"), entry("", "notes.txt"), entry("", "b c.qc2"),
				entry("", "pipe.qc2"), entry("", "gone.qc2"),
			},
			"file list win":    {entry("", "win/win10.qcow2"), entry("", "win/link.qc2"), entry("<dir>", "win/10")},
			"file list win/10": {entry("", "win/10/a.qc2")},
			"file list exp1":   {entry("<dir>", "exp1/files"), entry("", "exp1/kept.qc2")},
		}
		inspected = map[string][][]string{
			dir + "/a.qc2":         {{dir + "/a.qc2", "qcow2", "1G", "2K", "", "false"}},
			dir + "/win/10/a.qc2":  {{dir + "/win/10/a.qc2", "qcow2", "1G", "2K", "", "true"}},
			dir + "/exp1/kept.qc2": {{dir + "/exp1/kept.qc2", "qcow2", "1G", "2K", "", "false"}},
			dir + "/win/link.qc2":  {{dir + "/win/link.qc2", "qcow2", "1G", "2K", "", "false"}},
			outside + "/o.img":     {{outside + "/o.img", "raw", "1G", "1G", "", "false"}},
			dir + "/win/win10.qcow2": {
				{dir + "/win/win10.qcow2", "qcow2", "1G", "2K", "../a.qc2", "false"},
				{dir + "/win/../a.qc2", "qcow2", "1G", "2K", "", "false"},
			},
		}
	)

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if rows, ok := folders[cmd.Base]; ok {
			return []*minicli.Response{mmtest.Tabular("head", listing, rows...)}
		}

		image, ok := strings.CutPrefix(cmd.Base, "disk info ")
		if rows, found := inspected[strings.TrimSuffix(image, " recursive")]; ok && found {
			return []*minicli.Response{mmtest.Tabular("head", info, rows...)}
		}

		return nil
	})

	inside := func(rel string) Details {
		return Details{Kind: VMImage, Name: filepath.Base(rel), FullPath: dir + "/" + rel, RelativePath: rel}
	}

	inUse := inside("win/10/a.qc2")
	inUse.InUse = true

	backed := inside("win/win10.qcow2")
	backed.BackingImages = []string{dir + "/a.qc2"}

	want := map[string]Details{
		dir + "/a.qc2":           inside("a.qc2"),
		dir + "/win/10/a.qc2":    inUse,
		dir + "/exp1/kept.qc2":   inside("exp1/kept.qc2"),
		dir + "/win/win10.qcow2": backed,
		dir + "/win/link.qc2":    inside("win/link.qc2"),
		outside + "/o.img":       {Kind: UNKNOWN, Name: "o.img", FullPath: outside + "/o.img", OutsideFilesDir: true},
	}

	opts := cmp.Options{
		cmpopts.IgnoreFields(Details{}, "ReadOnly", "Size", "VirtualSize", "Experiments", "Format"),
		cmpopts.EquateEmpty(),
	}
	if diff := cmp.Diff(want, listed(t, ""), opts); diff != "" {
		t.Errorf("GetImages(%q) through minimega mismatch (-want +got):\n%s", "", diff)
	}

	var sent []string

	for _, base := range mmtest.Bases(received()) {
		if base != "args" {
			sent = append(sent, base)
		}
	}

	wantSent := []string{
		"file list",
		"disk info " + dir + "/a.qc2 recursive",
		"file list win",
		"disk info " + dir + "/win/win10.qcow2 recursive",
		"disk info " + dir + "/win/link.qc2 recursive",
		"file list exp1",
		"disk info " + dir + "/exp1/kept.qc2 recursive",
		"file list win/10",
		"disk info " + dir + "/win/10/a.qc2 recursive",
		"disk info " + outside + "/o.img recursive",
	}

	if diff := cmp.Diff(wantSent, sent); diff != "" {
		t.Errorf("GetImages(%q) minimega commands mismatch (-want +got):\n%s", "", diff)
	}
}

func TestGetImagesWithRealQemuImg(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")

	ClearCache()
	t.Cleanup(ClearCache)

	dir := useFilesDir(t)
	outside := t.TempDir()

	for _, folder := range []string{dir + "/sub/deeper", outside + "/dir"} {
		if err := os.MkdirAll(folder, 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", folder, err)
		}
	}

	if err := os.Symlink(outside+"/dir", dir+"/sub/sym"); err != nil {
		t.Fatalf("os.Symlink(dir, sub/sym) error = %v", err)
	}

	// minimega names a snapshot's backing file relative to the snapshot
	for _, args := range [][]string{
		{"create", "-f", "qcow2", dir + "/base.qc2", "1G"},
		{"create", "-f", "qcow2", "-b", dir + "/base.qc2", "-F", "qcow2", dir + "/snap.qcow2"},
		{"create", "-f", "qcow2", "-b", "../../base.qc2", "-F", "qcow2", dir + "/sub/deeper/snap.qc2"},
		{"create", "-f", "raw", dir + "/tools.iso", "1M"},
		{"create", "-f", "qcow2", dir + "/sub/base.qc2", "1G"},
		{"create", "-f", "qcow2", outside + "/base.qc2", "2G"},
		{"create", "-f", "qcow2", "-b", "../base.qc2", "-F", "qcow2", outside + "/dir/over.qc2"},
	} {
		if out, err := exec.Command("qemu-img", args...).CombinedOutput(); err != nil {
			t.Fatalf("qemu-img %v: %v: %s", args, err, out)
		}
	}

	images := listed(t, "")

	wantPaths := []string{
		dir + "/base.qc2", dir + "/snap.qcow2", dir + "/sub/base.qc2", dir + "/sub/deeper/snap.qc2", dir + "/tools.iso",
	}

	if diff := cmp.Diff(wantPaths, slices.Sorted(maps.Keys(images))); diff != "" {
		t.Errorf("GetImages(%q) paths mismatch (-want +got):\n%s", "", diff)
	}

	for _, snap := range []string{"snap.qcow2", "sub/deeper/snap.qc2"} {
		if diff := cmp.Diff([]string{dir + "/base.qc2"}, images[dir+"/"+snap].BackingImages); diff != "" {
			t.Errorf("GetImages(%q) %s BackingImages mismatch (-want +got):\n%s", "", snap, diff)
		}
	}

	if got := images[dir+"/tools.iso"].Kind; got != ISOImage {
		t.Errorf("GetImages(%q) tools.iso Kind = %v, want %v", "", got, ISOImage)
	}

	if got := images[dir+"/base.qc2"].VirtualSize; got != "1.0 GiB" {
		t.Errorf("GetImages(%q) base.qc2 VirtualSize = %q, want %q", "", got, "1.0 GiB")
	}

	// "../" after a symlinked folder leaves the link's target, not sub/
	over, err := GetImage("sub/sym/over.qc2")
	if err != nil {
		t.Fatalf("GetImage(%q) error = %v", "sub/sym/over.qc2", err)
	}

	want, err := filepath.EvalSymlinks(outside + "/base.qc2")
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(base.qc2) error = %v", err)
	}

	if diff := cmp.Diff([]string{want}, over.BackingImages); diff != "" {
		t.Errorf("GetImage(%q).BackingImages mismatch (-want +got):\n%s", "sub/sym/over.qc2", diff)
	}
}
