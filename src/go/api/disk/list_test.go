package disk

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// useFilesDir makes a new folder the minimega files directory until the test
// ends, as <PhenixBase>/images with no minimega answering, with an empty
// experiment store, and returns it.
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
		t.Fatal(err)
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
			t.Fatal(err)
		}
	}
}

// writeImages writes a file at each path, holding the path, making the
// folders it is in.
func writeImages(t *testing.T, paths ...string) {
	t.Helper()

	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		writeFile(t, p, p)
	}
}

// listed returns the images GetImages lists, by full path.
func listed(t *testing.T, expName string) map[string]Details {
	t.Helper()

	images, err := GetImages(expName)
	if err != nil {
		t.Fatalf("GetImages(%q): %v", expName, err)
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

// The listing searches the files directory's folders at any depth, down to
// maxDepth, leaving out what experiments and minimega keep there, and every
// file that is not an image.
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
					t.Fatal(err)
				}

				if err := os.Symlink(folder, dir); err != nil {
					t.Fatal(err)
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
					t.Fatal(err)
				}
			}

			want := slices.Concat(kept, []string{"link.qc2"})

			if err := syscall.Mkfifo(filepath.Join(folder, "pipe.qc2"), 0o600); err != nil {
				t.Fatal(err)
			}

			// one folder phenix cannot read leaves the rest listed; root reads it
			if os.Geteuid() != 0 {
				writeImages(t, filepath.Join(folder, "locked", "x.qc2"))

				if err := os.Chmod(filepath.Join(folder, "locked"), 0); err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() { _ = os.Chmod(filepath.Join(folder, "locked"), 0o750) })
			}

			images := listed(t, "")

			wantPaths := make([]string, 0, len(want))
			for _, rel := range want {
				wantPaths = append(wantPaths, filepath.Join(dir, rel))
			}

			slices.Sort(wantPaths)

			if got := slices.Sorted(maps.Keys(images)); !slices.Equal(got, wantPaths) {
				t.Fatalf("listed %q,\nwant %q", got, wantPaths)
			}

			for _, rel := range want {
				image := images[filepath.Join(dir, rel)]
				if image.RelativePath != rel || image.Name != filepath.Base(rel) || image.OutsideFilesDir || image.ReadOnly {
					t.Errorf("%s: %+v, want it inside the files directory at %s", rel, image, rel)
				}
			}

			if got := images[filepath.Join(dir, "tools.iso")].Kind; got != ISOImage {
				t.Errorf("tools.iso kind = %v, want ISO", got)
			}
		})
	}
}

// Topologies name images anywhere, of any extension; each is listed, with the
// images backing it, when phenix can read it, and every image is matched to
// the experiments using it by full path.
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
		t.Fatal(err)
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

	type image struct {
		rel         string
		outside     bool
		readOnly    bool
		kind        Kind
		backing     []string
		experiments []ExperimentUse
	}

	want := map[string]image{
		dir + "/base.qc2": {rel: "base.qc2", kind: VMImage, experiments: []ExperimentUse{expA}},
		dir + "/win/child.qc2": {
			rel:         "win/child.qc2",
			kind:        VMImage,
			backing:     []string{dir + "/base.qc2"},
			experiments: []ExperimentUse{expA},
		},
		dir + "/other/base.qc2":       {rel: "other/base.qc2", kind: VMImage, experiments: []ExperimentUse{expB}},
		dir + "/sub/x.qc2":            {rel: "sub/x.qc2", kind: VMImage, experiments: []ExperimentUse{expA}},
		dir + "/exp-a/files/snap.hdd": {rel: "exp-a/files/snap.hdd", readOnly: true, kind: VMImage, experiments: []ExperimentUse{expA}},
		outside + "/o.img":            {outside: true, readOnly: true, kind: UNKNOWN, experiments: []ExperimentUse{expA}},
		outside + "/b-only.qc2":       {outside: true, readOnly: true, kind: VMImage, experiments: []ExperimentUse{expB}},
	}

	images := listed(t, "")

	if got, wantPaths := slices.Sorted(maps.Keys(images)), slices.Sorted(maps.Keys(want)); !slices.Equal(got, wantPaths) {
		t.Fatalf("listed %q,\nwant %q", got, wantPaths)
	}

	for path, w := range want {
		got := images[path]

		if got.RelativePath != w.rel || got.OutsideFilesDir != w.outside || got.ReadOnly != w.readOnly || got.Kind != w.kind {
			t.Errorf("%s: %+v, want %+v", path, got, w)
		}

		if !slices.Equal(got.BackingImages, w.backing) || !reflect.DeepEqual(got.Experiments, w.experiments) {
			t.Errorf("%s: backing %q, experiments %+v; want %q, %+v", path, got.BackingImages, got.Experiments, w.backing, w.experiments)
		}
	}

	// once per image phenix can read, and never for the others
	if got := calls(); got != len(want) {
		t.Errorf("qemu-img ran %d times, want %d", got, len(want))
	}

	// one experiment's listing leaves out the images only others name
	images = listed(t, "exp-b")

	if _, ok := images[outside+"/o.img"]; ok {
		t.Error("exp-b's listing has an image only exp-a names")
	}

	if got := images[outside+"/b-only.qc2"].Experiments; !reflect.DeepEqual(got, []ExperimentUse{expB}) {
		t.Errorf("exp-b's listing: b-only.qc2 experiments = %+v", got)
	}

	if _, err := GetImages("nope"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("GetImages of an unknown experiment: %v, want store.ErrNotExist", err)
	}
}

// Without qemu-img, minimega lists the files directory one folder at a time
// and inspects each image; phenix only asks it about folders and images it
// can name in one argument, and not about what it sees as something other
// than a regular file.
func TestGetImagesThroughMinimega(t *testing.T) {
	dir := useFilesDir(t)
	outside := t.TempDir()

	t.Setenv("PATH", t.TempDir())

	writeImages(t, filepath.Join(outside, "o.img"), filepath.Join(outside, "my o.qc2"), dir+"/exp1/kept.qc2")
	storeExperiments(t, testExperiment(t, "exp", "", outside+"/o.img", outside+"/my o.qc2"))

	// qemu-img would block on the FIFO, holding up minimega; a.qc2 and the
	// other images phenix cannot see are still inspected
	if err := syscall.Mkfifo(dir+"/pipe.qc2", 0o600); err != nil {
		t.Fatal(err)
	}

	for link, target := range map[string]string{"gone.qc2": "nowhere.qc2", "win/link.qc2": "../exp1/kept.qc2"} {
		if err := os.MkdirAll(filepath.Dir(dir+"/"+link), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, dir+"/"+link); err != nil {
			t.Fatal(err)
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

	images := listed(t, "")

	for path, rel := range map[string]string{
		dir + "/a.qc2":           "a.qc2",
		dir + "/win/10/a.qc2":    "win/10/a.qc2",
		dir + "/exp1/kept.qc2":   "exp1/kept.qc2",
		dir + "/win/win10.qcow2": "win/win10.qcow2",
		dir + "/win/link.qc2":    "win/link.qc2",
		outside + "/o.img":       "",
	} {
		if image := images[path]; image.RelativePath != rel || image.OutsideFilesDir != (rel == "") {
			t.Errorf("%s: %+v, want relative path %q", path, image, rel)
		}
	}

	if len(images) != 6 {
		t.Errorf("listed %q, want 6 images", slices.Sorted(maps.Keys(images)))
	}

	if got := images[dir+"/win/win10.qcow2"].BackingImages; !slices.Equal(got, []string{dir + "/a.qc2"}) {
		t.Errorf("win10.qcow2 backing images = %q, want [%s/a.qc2]", got, dir)
	}

	if !images[dir+"/win/10/a.qc2"].InUse || images[outside+"/o.img"].Kind != UNKNOWN {
		t.Errorf("minimega's details not kept: %+v, %+v", images[dir+"/win/10/a.qc2"], images[outside+"/o.img"])
	}

	var sent []string

	for _, base := range mmtest.Bases(received()) {
		if base != "args" {
			sent = append(sent, base)
		}
	}

	want := []string{
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

	if !slices.Equal(sent, want) {
		t.Errorf("sent %q,\nwant %q", sent, want)
	}
}

func TestGetImagesWithRealQemuImg(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}

	ClearCache()
	t.Cleanup(ClearCache)

	dir := useFilesDir(t)
	outside := t.TempDir()

	for _, folder := range []string{dir + "/sub/deeper", outside + "/dir"} {
		if err := os.MkdirAll(folder, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Symlink(outside+"/dir", dir+"/sub/sym"); err != nil {
		t.Fatal(err)
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

	if got := slices.Sorted(maps.Keys(images)); !slices.Equal(got, wantPaths) {
		t.Fatalf("listed %q,\nwant %q", got, wantPaths)
	}

	for _, snap := range []string{"snap.qcow2", "sub/deeper/snap.qc2"} {
		if got := images[dir+"/"+snap].BackingImages; !slices.Equal(got, []string{dir + "/base.qc2"}) {
			t.Errorf("%s backing images = %q, want [%s/base.qc2]", snap, got, dir)
		}
	}

	if images[dir+"/tools.iso"].Kind != ISOImage || images[dir+"/base.qc2"].VirtualSize != "1.0 GiB" {
		t.Errorf("unexpected details: %+v", images)
	}

	// "../" after a symlinked folder leaves the link's target, not sub/
	over, err := GetImage("sub/sym/over.qc2")
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(outside + "/base.qc2")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(over.BackingImages, []string{want}) {
		t.Errorf("over.qc2 backing images = %q, want [%s]", over.BackingImages, want)
	}
}
