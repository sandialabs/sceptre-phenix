package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/disk"
	"phenix/api/disk/disktest"
	"phenix/api/experiment"
	"phenix/api/job"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/web/cache"
	"phenix/web/rbac"
	"phenix/web/util"
)

// diskFiles is a minimega files directory for a test.
type diskFiles struct {
	dir     string          // the files directory
	outside string          // a folder outside it
	sent    func() []string // the minimega commands sent since last asked
	runs    func() int      // the times qemu-img has run
}

// useDiskFilesDir makes a new <PhenixBase>/images the minimega files directory, puts the fake qemu-img first on PATH,
// and has fake minimega answer every command with an empty success; the commands it reports leave out files-dir lookups.
func useDiskFilesDir(t *testing.T) diskFiles {
	t.Helper()

	received := mmtest.Use(t, nil)
	runs := disktest.UseFakeQemuImg(t)

	disk.ClearCache()
	t.Cleanup(disk.ClearCache)

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	dir := filepath.Join(base, "images")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("making the files directory: %v", err)
	}

	var seen int

	sent := func() []string {
		var sent []string

		all := mmtest.Bases(received())
		for _, base := range all[seen:] {
			if base != "args" {
				sent = append(sent, base)
			}
		}

		seen = len(all)

		return sent
	}

	return diskFiles{dir: dir, outside: t.TempDir(), sent: sent, runs: runs}
}

// writeDisks writes each file, making its folder, holding its path.
func writeDisks(t *testing.T, paths ...string) {
	t.Helper()

	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("making the folder of %s: %v", p, err)
		}

		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
}

// backedBy makes the fake qemu-img report the image at path as backed by
// backing, relative to path's folder or absolute.
func backedBy(t *testing.T, path, backing string) {
	t.Helper()

	if err := os.WriteFile(path+".backing", []byte(backing), 0o600); err != nil {
		t.Fatalf("writing the backing of %s: %v", path, err)
	}
}

// diskVM is a topology node, a VM booting from the images.
func diskVM(name string, images ...string) map[string]any {
	drives := make([]map[string]any, 0, len(images))
	for _, image := range images {
		drives = append(drives, map[string]any{"image": image})
	}

	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name},
		"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux", "drives": drives},
	}
}

// disksRole may take the verbs on every disk, and get and list the
// experiments.
func disksRole(experiments []string, verbs ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{Resources: []string{"disks"}, ResourceNames: []string{"*"}, Verbs: verbs},
				{Resources: []string{"experiments"}, ResourceNames: experiments, Verbs: []string{"get", "list"}},
			},
		},
	}
}

// listedDisk is a disk as GET /disks returns it.
type listedDisk struct {
	Kind            string               `json:"kind"`
	Name            string               `json:"name"`
	FullPath        string               `json:"fullPath"`
	RelativePath    string               `json:"relativePath"`
	OutsideFilesDir bool                 `json:"outsideFilesDir"`
	ReadOnly        bool                 `json:"readOnly"`
	Experiments     []disk.ExperimentUse `json:"experiments"`
	BackingImages   []string             `json:"backingImages"`
}

// getDisks lists the disks the role sees at query, failing the test unless
// the status is want.
func getDisks(t *testing.T, role rbac.Role, query string, want int) []listedDisk {
	t.Helper()

	resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+"/api/v1/disks"+query, "")
	body := readBody(t, resp)

	if resp.StatusCode != want {
		t.Fatalf("GET /api/v1/disks%s = %d, want %d: %s", query, resp.StatusCode, want, body)
	}

	var list struct {
		Disks []listedDisk `json:"disks"`
	}

	if want == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("GET /api/v1/disks%s = %s, not a disk list: %v", query, body, err)
		}

		if list.Disks == nil {
			t.Fatalf("GET /api/v1/disks%s = %s, want a disks list", query, body)
		}
	}

	return list.Disks
}

// fullPaths returns each disk's full path.
func fullPaths(disks []listedDisk) []string {
	paths := make([]string, 0, len(disks))
	for _, d := range disks {
		paths = append(paths, d.FullPath)
	}

	return paths
}

// useDiskExperiments stores running exp-a, booting from a nested image backed by the files directory's a.qc2 and from
// o.qc2 outside it, and stopped exp-b, booting from b-only.qc2 outside it, a missing image and exp-a's nested image name.
func useDiskExperiments(t *testing.T) diskFiles {
	t.Helper()

	files := useDiskFilesDir(t)
	dir, outside := files.dir, files.outside

	writeDisks(t,
		dir+"/a.qc2", dir+"/win/win10.qcow2", dir+"/linux/win10.qcow2", dir+"/exp-a/files/s.hdd",
		dir+"/over.qc2", outside+"/o.qc2", outside+"/b-only.qc2", outside+"/base.qc2",
	)

	backedBy(t, dir+"/win/win10.qcow2", "../a.qc2")
	backedBy(t, dir+"/over.qc2", outside+"/base.qc2")

	useTestStore(t,
		namedExperiment(t, "exp-a", "2026-09-30T00:00:00Z", diskVM("vm1", "win/win10.qcow2"), diskVM("vm2", outside+"/o.qc2")),
		namedExperiment(t, "exp-b", "", diskVM("vm1", outside+"/b-only.qc2"), diskVM("vm2", outside+"/missing.qc2"),
			diskVM("vm3", "linux/win10.qcow2"), diskVM("vm4", "a.qc2")),
	)

	return files
}

// GET /disks lists images in the files directory's folders and the images
// topologies name outside it, each by its full path, sorted by name and then
// full path.
func TestGetDisksListsFoldersAndOutsideImages(t *testing.T) {
	files := useDiskExperiments(t)
	dir, outside := files.dir, files.outside

	var (
		expA = disk.ExperimentUse{Name: "exp-a", Running: true}
		expB = disk.ExperimentUse{Name: "exp-b", Running: false}
	)

	want := []listedDisk{
		{Kind: "VM", Name: "a.qc2", FullPath: dir + "/a.qc2", RelativePath: "a.qc2",
			Experiments: []disk.ExperimentUse{expA, expB}, BackingImages: []string{}},
		{Kind: "VM", Name: "b-only.qc2", FullPath: outside + "/b-only.qc2", OutsideFilesDir: true, ReadOnly: true,
			Experiments: []disk.ExperimentUse{expB}, BackingImages: []string{}},
		{Kind: "VM", Name: "base.qc2", FullPath: outside + "/base.qc2", OutsideFilesDir: true, ReadOnly: true,
			BackingImages: []string{}},
		{Kind: "VM", Name: "o.qc2", FullPath: outside + "/o.qc2", OutsideFilesDir: true, ReadOnly: true,
			Experiments: []disk.ExperimentUse{expA}, BackingImages: []string{}},
		{Kind: "VM", Name: "over.qc2", FullPath: dir + "/over.qc2", RelativePath: "over.qc2",
			BackingImages: []string{outside + "/base.qc2"}},
		{Kind: "VM", Name: "win10.qcow2", FullPath: dir + "/linux/win10.qcow2", RelativePath: "linux/win10.qcow2",
			Experiments: []disk.ExperimentUse{expB}, BackingImages: []string{}},
		{Kind: "VM", Name: "win10.qcow2", FullPath: dir + "/win/win10.qcow2", RelativePath: "win/win10.qcow2",
			Experiments: []disk.ExperimentUse{expA}, BackingImages: []string{dir + "/a.qc2"}},
	}

	got := getDisks(t, disksRole([]string{"*"}, "list"), "", http.StatusOK)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /api/v1/disks mismatch (-want +got):\n%s", diff)
	}

	// one experiment's images, and every image in the files directory
	paths := fullPaths(getDisks(t, disksRole([]string{"*"}, "list"), "?expName=exp-a", http.StatusOK))

	if slices.Contains(paths, outside+"/b-only.qc2") {
		t.Errorf("GET /api/v1/disks?expName=exp-a lists %q, want no %s, which only exp-b uses", paths, outside+"/b-only.qc2")
	}

	if !slices.Contains(paths, outside+"/o.qc2") {
		t.Errorf("GET /api/v1/disks?expName=exp-a lists %q, want exp-a's %s", paths, outside+"/o.qc2")
	}

	getDisks(t, disksRole([]string{"*"}, "list"), "?expName=nope", http.StatusNotFound)
}

// A role sees an image outside the files directory only when it may get an
// experiment using it, or when it backs an image the role sees; and only the
// experiments it may list.
func TestGetDisksHidesWhatTheRoleCannotSee(t *testing.T) {
	files := useDiskExperiments(t)
	dir, outside := files.dir, files.outside

	byPath := func(disks []listedDisk) map[string]listedDisk {
		m := make(map[string]listedDisk)
		for _, d := range disks {
			m[d.FullPath] = d
		}

		return m
	}

	disks := byPath(getDisks(t, disksRole([]string{"exp-a"}, "list"), "", http.StatusOK))

	if _, ok := disks[outside+"/b-only.qc2"]; ok {
		t.Errorf("GET /api/v1/disks as an exp-a viewer lists %s, which only exp-b uses; want it left out", outside+"/b-only.qc2")
	}

	if _, ok := disks[outside+"/base.qc2"]; !ok {
		t.Errorf("GET /api/v1/disks as an exp-a viewer leaves out %s, which backs over.qc2; want it listed", outside+"/base.qc2")
	}

	want := []disk.ExperimentUse{{Name: "exp-a", Running: true}}
	if diff := cmp.Diff(want, disks[dir+"/a.qc2"].Experiments); diff != "" {
		t.Errorf("GET /api/v1/disks as an exp-a viewer a.qc2 experiments mismatch (-want +got):\n%s", diff)
	}

	getDisks(t, disksRole([]string{"exp-a"}, "list"), "?expName=exp-b", http.StatusForbidden)

	// a disk's name covers the file of that name in every folder
	role := combinedRole(testRole("disks", "list", "win10.qcow2"), testRole("experiments", "get", "*"))

	paths := fullPaths(getDisks(t, role, "", http.StatusOK))
	if diff := cmp.Diff([]string{dir + "/linux/win10.qcow2", dir + "/win/win10.qcow2"}, paths); diff != "" {
		t.Errorf("GET /api/v1/disks as a win10.qcow2 lister mismatch (-want +got):\n%s", diff)
	}

	// a role that sees no image gets an empty list
	if got := getDisks(t, testRole("disks", "list", "none.qc2"), "", http.StatusOK); len(got) != 0 {
		t.Errorf("GET /api/v1/disks as a none.qc2 lister = %q, want nothing", fullPaths(got))
	}
}

// genDiskChains draws disks inside and outside the files directory, each backed by the
// whole chain of an earlier one (and maybe a missing image), in any order.
func genDiskChains(experiments []string) *rapid.Generator[[]disk.Details] {
	return rapid.Custom(func(t *rapid.T) []disk.Details {
		n := rapid.IntRange(0, 7).Draw(t, "disks")
		disks := make([]disk.Details, 0, n)

		for i := range n {
			d := disk.Details{Name: "d" + strconv.Itoa(i) + ".qc2", OutsideFilesDir: rapid.Bool().Draw(t, "outside")}

			d.FullPath = "/files/" + d.Name
			if d.OutsideFilesDir {
				d.FullPath = "/outside/" + d.Name
			}

			if parent := rapid.IntRange(-1, i-1).Draw(t, "parent"); parent >= 0 {
				d.BackingImages = append([]string{disks[parent].FullPath}, disks[parent].BackingImages...)
			} else if rapid.Bool().Draw(t, "missingBase") {
				d.BackingImages = []string{"/outside/missing.qc2"}
			}

			uses := rapid.SliceOfDistinct(rapid.SampledFrom(experiments), func(s string) string { return s }).Draw(t, "uses")
			for _, name := range uses {
				d.Experiments = append(d.Experiments, disk.ExperimentUse{Name: name, Running: rapid.Bool().Draw(t, "running")})
			}

			disks = append(disks, d)
		}

		return rapid.Permutation(disks).Draw(t, "order")
	})
}

// visibleDisks shows every disk inside the files directory or used by an experiment the
// role may get, plus exactly the images those are backed by, each once and cut to the
// experiments the role may list; the list it was given is left as it was.
func TestVisibleDisksProperties(t *testing.T) {
	t.Parallel()

	experiments := []string{"exp-a", "exp-b", "exp-c"}
	subset := rapid.SliceOfDistinct(rapid.SampledFrom(experiments), func(s string) string { return s })

	rapid.Check(t, func(t *rapid.T) {
		disks := genDiskChains(experiments).Draw(t, "disks")
		gettable, listable := subset.Draw(t, "get"), subset.Draw(t, "list")
		role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
			{Resources: []string{"experiments"}, ResourceNames: gettable, Verbs: []string{"get"}},
			{Resources: []string{"experiments"}, ResourceNames: listable, Verbs: []string{"list"}},
		}}}

		given := make([]disk.Details, len(disks))
		for i, d := range disks {
			given[i] = d
			given[i].Experiments = slices.Clone(d.Experiments)
		}

		visible := visibleDisks(role, disks)

		if diff := cmp.Diff(given, disks, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("visibleDisks changed the disks it was given (-before +after):\n%s", diff)
		}

		byPath := make(map[string]disk.Details, len(disks))
		for _, d := range disks {
			byPath[d.FullPath] = d
		}

		shown := make(map[string]bool, len(visible))
		for _, d := range visible {
			if _, ok := byPath[d.FullPath]; !ok || shown[d.FullPath] {
				t.Fatalf("visibleDisks(get %q) = %q, want each of the given disks at most once", gettable, diskChainPaths(visible))
			}

			shown[d.FullPath] = true
		}

		base := func(d disk.Details) bool {
			return !d.OutsideFilesDir ||
				slices.ContainsFunc(d.Experiments, func(u disk.ExperimentUse) bool { return slices.Contains(gettable, u.Name) })
		}
		backsBase := make(map[string]bool)

		for _, d := range disks {
			if !base(d) {
				continue
			}

			if !shown[d.FullPath] {
				t.Fatalf("visibleDisks(get %q) = %q, want %s, inside or used by a gettable experiment",
					gettable, diskChainPaths(visible), d.FullPath)
			}

			for _, backing := range d.BackingImages {
				backsBase[backing] = true
			}
		}

		for _, d := range visible {
			if !base(byPath[d.FullPath]) && !backsBase[d.FullPath] {
				t.Fatalf("visibleDisks(get %q) = %q, want %s left out: nothing shown is backed by it",
					gettable, diskChainPaths(visible), d.FullPath)
			}

			for _, backing := range d.BackingImages {
				if _, ok := byPath[backing]; ok && !shown[backing] {
					t.Fatalf("visibleDisks(get %q) = %q, want %s, which backs %s", gettable, diskChainPaths(visible), backing, d.FullPath)
				}
			}

			want := slices.DeleteFunc(slices.Clone(byPath[d.FullPath].Experiments), func(u disk.ExperimentUse) bool {
				return !slices.Contains(listable, u.Name)
			})
			if diff := cmp.Diff(want, d.Experiments, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("visibleDisks(list %q) %s experiments mismatch (-want +got):\n%s", listable, d.FullPath, diff)
			}
		}
	})
}

// diskChainPaths returns each disk's full path.
func diskChainPaths(disks []disk.Details) []string {
	paths := make([]string, 0, len(disks))
	for _, d := range disks {
		paths = append(paths, d.FullPath)
	}

	return paths
}

// The disk watch moves to minimega's files directory once minimega gives one
// other than <PhenixBase>/images, reporting that the list changed.
func TestWatchFilesDirectoryFollowsMinimega(t *testing.T) {
	useDiskFilesDir(t)

	var (
		given   = make(chan struct{})
		changes atomic.Int32
	)

	watchFilesDirectory(t.Context(), given, 10*time.Millisecond, func() { changes.Add(1) })

	// minimega gives another files directory, which GetMMFullPath returns
	base := t.TempDir()
	if err := os.Mkdir(base+"/images", 0o750); err != nil {
		t.Fatalf("making the new files directory: %v", err)
	}

	common.PhenixBase = base //nolint:reassign // the new files directory's parent; useDiskFilesDir restores it

	close(given)

	polltest.Until(t, "the move is reported", func() bool { return changes.Load() >= 1 })

	writeDisks(t, base+"/images/a.qc2")

	polltest.Until(t, "writing an image in the new files directory is reported", func() bool { return changes.Load() >= 2 })
}

// tree returns every file under dir with its contents.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := make(map[string]string)

	err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		b, err := os.ReadFile(p)
		files[p] = string(b)

		return err
	})
	if err != nil {
		t.Fatalf("reading the files under %s: %v", dir, err)
	}

	return files
}

// A new image's name is relative to the folder of its source. A snapshot is a qcow2 image, so it gets .qcow2 unless
// it has a qcow2 extension. A copy or a rename keeps the format of its source, so it keeps or gets the source's
// extension, and a name with a known image extension of another format is refused.
func TestNormalizeDstDisk(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src, dst   string
		sameFormat bool
		want       string // empty when refused
	}{
		"snapshot":                    {src: "/files/a.qc2", dst: "new", want: "/files/new.qcow2"},
		"snapshot to .qc2":            {src: "/files/a.qc2", dst: "new.qc2", want: "/files/new.qc2"},
		"snapshot of qcow":            {src: "/files/old.qcow", dst: "new", want: "/files/new.qcow2"},
		"snapshot of qcow to .qcow":   {src: "/files/old.qcow", dst: "new.qcow", want: "/files/new.qcow.qcow2"},
		"snapshot of an ISO to .iso":  {src: "/files/boot.iso", dst: "new.iso", want: "/files/new.iso.qcow2"},
		"copy":                        {src: "/files/win/b.qcow2", dst: "new", sameFormat: true, want: "/files/win/new.qcow2"},
		"copy of qcow2 to .qc2":       {src: "/files/win/b.qcow2", dst: "/abs/new.qc2", sameFormat: true, want: "/abs/new.qc2"},
		"copy of qcow2 to .qcow":      {src: "/files/a.qc2", dst: "new.qcow", sameFormat: true},
		"copy of qcow2 to .iso":       {src: "/files/a.qc2", dst: "new.iso", sameFormat: true},
		"copy of qcow":                {src: "/files/old.qcow", dst: "new", sameFormat: true, want: "/files/new.qcow"},
		"copy of qcow to .qcow":       {src: "/files/old.qcow", dst: "/abs/new.qcow", sameFormat: true, want: "/abs/new.qcow"},
		"copy of qcow to .qc2":        {src: "/files/old.qcow", dst: "new.qc2", sameFormat: true, want: "/files/new.qc2"},
		"copy of qcow to other":       {src: "/files/old.qcow", dst: "new.img", sameFormat: true, want: "/files/new.img.qcow"},
		"copy of qcow to .hdd":        {src: "/files/old.qcow", dst: "new.hdd", sameFormat: true},
		"copy of a .qcow2.qcow image": {src: "/files/a.qcow2.qcow", dst: "../b", sameFormat: true, want: "/b.qcow"},
		"copy of an ISO":              {src: "/files/boot.iso", dst: "bar", sameFormat: true, want: "/files/bar.iso"},
		"copy of an ISO to .iso":      {src: "/files/boot.iso", dst: "sub/bar.iso", sameFormat: true, want: "/files/sub/bar.iso"},
		"copy of an ISO to a folder":  {src: "/files/iso/boot.iso", dst: "../bar", sameFormat: true, want: "/files/bar.iso"},
		"copy of an ISO to other":     {src: "/files/boot.iso", dst: "/abs/bar.img", sameFormat: true, want: "/abs/bar.img.iso"},
		"copy of an ISO to .qcow2":    {src: "/files/boot.iso", dst: "bar.qcow2", sameFormat: true},
		"copy of an ISO to .qcow":     {src: "/files/boot.iso", dst: "/abs/bar.qcow", sameFormat: true},
		"copy of an HDD":              {src: "/files/d.hdd", dst: "/abs/e", sameFormat: true, want: "/abs/e.hdd"},
		"copy of an HDD to .hdd":      {src: "/files/d.hdd", dst: "e.hdd", sameFormat: true, want: "/files/e.hdd"},
		"copy of an HDD to .iso":      {src: "/files/d.hdd", dst: "e.iso", sameFormat: true},
		"copy of a rootfs":            {src: "/files/c_rootfs.tgz", dst: "d", sameFormat: true, want: "/files/d_rootfs.tgz"},
		"copy of a rootfs to rootfs":  {src: "/files/c_rootfs.tgz", dst: "d_rootfs.tgz", sameFormat: true, want: "/files/d_rootfs.tgz"},
		"copy of a rootfs to .tgz":    {src: "/files/c_rootfs.tgz", dst: "d.tgz", sameFormat: true, want: "/files/d.tgz_rootfs.tgz"},
		"copy of a rootfs to .qc2":    {src: "/files/c_rootfs.tgz", dst: "d.qc2", sameFormat: true},
		"copy of an unknown image":    {src: "/files/x.img", dst: "y", sameFormat: true, want: "/files/y.qcow2"},
		"copy of unknown to .iso":     {src: "/files/x.img", dst: "y.iso", sameFormat: true, want: "/files/y.iso.qcow2"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeDstDisk(tt.src, tt.dst, tt.sameFormat)

			switch {
			case tt.want == "" && !errors.Is(err, disk.ErrWrongExtension):
				t.Errorf("normalizeDstDisk(%q, %q, %t) = %q, %v, want an error wrapping %v",
					tt.src, tt.dst, tt.sameFormat, got, err, disk.ErrWrongExtension)
			case tt.want != "" && (err != nil || got != tt.want):
				t.Errorf("normalizeDstDisk(%q, %q, %t) = %q, %v, want %q", tt.src, tt.dst, tt.sameFormat, got, err, tt.want)
			}
		})
	}
}

// The extensions each source extension's copies may end in; a snapshot, or a copy of an image with none of these
// extensions, is named as a qcow2 image.
var normalizeDstDiskGroups = map[string][]string{ //nolint:gochecknoglobals // test table
	".qcow2":      {".qcow2", ".qc2"},
	".qc2":        {".qcow2", ".qc2"},
	".qcow":       {".qcow", ".qcow2", ".qc2"},
	".iso":        {".iso"},
	".hdd":        {".hdd"},
	"_rootfs.tgz": {"_rootfs.tgz"},
}

// A new image's name becomes an absolute path that ends in an extension of its source's group, kept as given once it
// has one, so normalizing it again changes nothing. A copy or a rename to a known image extension of another group is
// refused instead.
func TestNormalizeDstDiskProperties(t *testing.T) {
	t.Parallel()

	extOf := func(name string) string {
		for ext := range normalizeDstDiskGroups {
			if strings.HasSuffix(name, ext) {
				return ext
			}
		}

		return ""
	}

	rapid.Check(t, func(t *rapid.T) {
		src := rapid.SampledFrom([]string{
			"/files/a.qc2", "/files/win/b.qcow2", "/outside/c.img", "/files/old.qcow",
			"/files/boot.iso", "/files/d.hdd", "/files/e_rootfs.tgz",
		}).Draw(t, "src")
		dst := rapid.SampledFrom([]string{"", "/abs/", "sub/", "../", "./"}).Draw(t, "folder") + "new" +
			rapid.SampledFrom([]string{"", ".qc2", ".qcow2", ".qcow", ".img", ".qcow2.bak", ".iso", ".hdd", "_rootfs.tgz", ".tgz"}).
				Draw(t, "extension")
		sameFormat := rapid.Bool().Draw(t, "sameFormat")

		exts, strict := normalizeDstDiskGroups[".qcow2"], false
		if group, ok := normalizeDstDiskGroups[extOf(src)]; sameFormat && ok {
			exts, strict = group, true
		}

		ends := func(name string) bool {
			return slices.ContainsFunc(exts, func(ext string) bool { return strings.HasSuffix(name, ext) })
		}

		got, err := normalizeDstDisk(src, dst, sameFormat)

		if refuse := strict && !ends(dst) && extOf(dst) != ""; refuse != (err != nil) {
			t.Fatalf("normalizeDstDisk(%q, %q, %t) = %q, %v, want refused %t", src, dst, sameFormat, got, err, refuse)
		}

		if err != nil {
			if !errors.Is(err, disk.ErrWrongExtension) {
				t.Fatalf("normalizeDstDisk(%q, %q, %t) error = %v, want it to wrap %v",
					src, dst, sameFormat, err, disk.ErrWrongExtension)
			}

			return
		}

		if !filepath.IsAbs(got) || !ends(got) {
			t.Fatalf("normalizeDstDisk(%q, %q, %t) = %q, want an absolute path ending in one of %q",
				src, dst, sameFormat, got, exts)
		}

		if again, err := normalizeDstDisk(src, got, sameFormat); err != nil || again != got {
			t.Fatalf("normalizeDstDisk(%q, %q, %t) = %q, %v, want it unchanged", src, got, sameFormat, again, err)
		}
	})
}

// A copy or a rename keeps the format of an ISO, HDD or root file system image, so the new image keeps its
// extension, and a new name with the extension of another format is refused with the extension it must have.
func TestDiskActionsKeepTheExtension(t *testing.T) {
	files := useDiskFilesDir(t)
	dir := files.dir

	writeDisks(t, dir+"/boot.iso", dir+"/data.hdd", dir+"/c_rootfs.tgz")

	server := serveAs(t, "alice", disksRole(nil, "create", "update", "get")).URL + "/api/v1/disks"

	for _, target := range []string{
		"/clone?disk=boot.iso&new=copy",
		"/rename?disk=copy.iso&new=moved.iso",
		"/clone?disk=data.hdd&new=" + url.QueryEscape(dir+"/data2"),
		"/rename?disk=c_rootfs.tgz&new=r",
	} {
		// each request acts on what the ones before it left
		resp := do(t, http.MethodPost, server+target, "")
		if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/v1/disks%s = %d, want %d: %s", target, resp.StatusCode, http.StatusOK, body)
		}
	}

	resp := do(t, http.MethodPost, server+"/rename?disk=boot.iso&new=boot.qcow2", "")
	body := readBody(t, resp)

	if want := "must end in .iso"; resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, want) {
		t.Errorf("POST /api/v1/disks/rename to boot.qcow2 = %d %s, want %d with %q",
			resp.StatusCode, body, http.StatusBadRequest, want)
	}

	want := map[string]string{
		dir + "/boot.iso":     dir + "/boot.iso",
		dir + "/moved.iso":    dir + "/boot.iso",
		dir + "/data.hdd":     dir + "/data.hdd",
		dir + "/data2.hdd":    dir + "/data.hdd",
		dir + "/r_rootfs.tgz": dir + "/c_rootfs.tgz",
	}

	if diff := cmp.Diff(want, tree(t, dir)); diff != "" {
		t.Errorf("files after the disk actions mismatch (-want +got):\n%s", diff)
	}
}

// A snapshot of a .qcow image is a qcow2 image, so it gets .qcow2, while a copy or a rename of it keeps .qcow.
func TestDiskActionsOnQcowImages(t *testing.T) {
	files := useDiskFilesDir(t)
	dir := files.dir

	writeDisks(t, dir+"/old.qcow")

	server := serveAs(t, "alice", disksRole(nil, "create", "update", "get")).URL + "/api/v1/disks"

	for _, tc := range []struct {
		target string
		sent   []string
	}{
		{"/snapshot?disk=old.qcow&new=snap", []string{"disk snapshot " + dir + "/old.qcow " + dir + "/snap.qcow2"}},
		{"/snapshot?disk=old.qcow&new=snap.qcow", []string{"disk snapshot " + dir + "/old.qcow " + dir + "/snap.qcow.qcow2"}},
		{"/clone?disk=old.qcow&new=copy", nil},
		{"/rename?disk=copy.qcow&new=moved", nil},
		{"/clone?disk=old.qcow&new=new.qc2", nil},
	} {
		// each request acts on what the ones before it left
		resp := do(t, http.MethodPost, server+tc.target, "")
		if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/v1/disks%s = %d, want %d: %s", tc.target, resp.StatusCode, http.StatusOK, body)
		}

		if diff := cmp.Diff(tc.sent, files.sent(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("POST /api/v1/disks%s minimega commands mismatch (-want +got):\n%s", tc.target, diff)
		}
	}

	// minimega is fake, so only the clones and the rename change files
	want := map[string]string{
		dir + "/old.qcow":   dir + "/old.qcow",
		dir + "/moved.qcow": dir + "/old.qcow",
		dir + "/new.qc2":    dir + "/old.qcow",
	}

	if diff := cmp.Diff(want, tree(t, dir)); diff != "" {
		t.Errorf("files after the disk actions mismatch (-want +got):\n%s", diff)
	}
}

// The disk actions refuse, before touching anything, paths outside the files
// directory or in the folders the listing leaves out, names minimega cannot
// use, and anything the role may not do.
func TestDiskActionsRefuse(t *testing.T) {
	files := useDiskExperiments(t)
	dir, outside := files.dir, files.outside

	writeDisks(t, dir+"/win/a b.qc2", dir+"/win/a#b.qc2", dir+"/boot.iso")

	all := disksRole([]string{"*"}, "create", "update", "delete", "get")
	q := url.QueryEscape

	// may create any image and get only its own (x*), so not a.qc2
	ownOnly := disksRole(nil, "create")
	ownOnly.Spec.Policies = append(ownOnly.Spec.Policies, &v1.PolicySpec{
		Resources: []string{"disks"}, ResourceNames: []string{"x*"}, Verbs: []string{"get"},
	})

	for _, tc := range []struct {
		name, method, target string
		role                 rbac.Role
		want                 int
	}{
		{"snapshot outside", http.MethodPost, "/snapshot?disk=" + q(outside+"/o.qc2") + "&new=x", all, http.StatusBadRequest},
		{"snapshot above", http.MethodPost, "/snapshot?disk=../a.qc2&new=x", all, http.StatusBadRequest},
		{"snapshot to above", http.MethodPost, "/snapshot?disk=a.qc2&new=" + q("../../etc/x"), all, http.StatusBadRequest},
		{"snapshot of experiment files", http.MethodPost, "/snapshot?disk=" + q("exp-a/files/s.hdd") + "&new=x", all, http.StatusBadRequest},
		{"snapshot a name with a space", http.MethodPost, "/snapshot?disk=" + q("win/a b.qc2") + "&new=x", all, http.StatusBadRequest},
		{"snapshot to a name with $", http.MethodPost, "/snapshot?disk=a.qc2&new=" + q("x$HOME"), all, http.StatusBadRequest},
		{"snapshot missing", http.MethodPost, "/snapshot?disk=missing.qc2&new=x", all, http.StatusNotFound},
		{"snapshot onto an image", http.MethodPost, "/snapshot?disk=a.qc2&new=over.qc2", all, http.StatusConflict},
		{"snapshot not allowed", http.MethodPost, "/snapshot?disk=a.qc2&new=x", disksRole(nil, "update"), http.StatusForbidden},
		{"snapshot a disk it may not get", http.MethodPost, "/snapshot?disk=a.qc2&new=x", ownOnly, http.StatusForbidden},
		{"snapshot to its folder", http.MethodPost, "/snapshot?disk=" + q("win/win10.qcow2") + "&new=.", all, http.StatusBadRequest},
		{"commit outside", http.MethodPost, "/commit?disk=" + q(outside+"/o.qc2"), all, http.StatusBadRequest},
		{"commit into outside", http.MethodPost, "/commit?disk=over.qc2", all, http.StatusBadRequest},
		{"commit a name with #", http.MethodPost, "/commit?disk=" + q("win/a#b.qc2"), all, http.StatusBadRequest},
		{"commit missing", http.MethodPost, "/commit?disk=missing.qc2", all, http.StatusNotFound},
		{"commit not allowed", http.MethodPost, "/commit?disk=" + q("win/win10.qcow2"), disksRole(nil, "create"), http.StatusForbidden},
		{"rebase onto outside", http.MethodPost, "/rebase?disk=a.qc2&unsafe=true&backing=" + q(outside+"/o.qc2"), all, http.StatusBadRequest},
		{"rebase not boolean", http.MethodPost, "/rebase?disk=a.qc2&unsafe=maybe&backing=", all, http.StatusBadRequest},
		{"rebase not allowed", http.MethodPost, "/rebase?disk=a.qc2&unsafe=true&backing=", disksRole(nil, "create"), http.StatusForbidden},
		{"resize outside", http.MethodPost, "/resize?disk=" + q(outside+"/o.qc2") + "&size=1G", all, http.StatusBadRequest},
		{"resize a hidden image", http.MethodPost, "/resize?disk=" + q(".a.qc2") + "&size=1G", all, http.StatusBadRequest},
		{"resize not allowed", http.MethodPost, "/resize?disk=a.qc2&size=1G", disksRole(nil, "create"), http.StatusForbidden},
		{"clone outside", http.MethodPost, "/clone?disk=" + q(outside+"/o.qc2") + "&new=x", all, http.StatusBadRequest},
		{"clone to outside", http.MethodPost, "/clone?disk=a.qc2&new=" + q("/tmp/x"), all, http.StatusBadRequest},
		{"clone onto an image", http.MethodPost, "/clone?disk=a.qc2&new=over.qc2", all, http.StatusConflict},
		{"clone not allowed", http.MethodPost, "/clone?disk=a.qc2&new=x", disksRole(nil, "update", "get"), http.StatusForbidden},
		{"clone a disk it may not get", http.MethodPost, "/clone?disk=a.qc2&new=x", ownOnly, http.StatusForbidden},
		{"clone to no name", http.MethodPost, "/clone?disk=" + q("win/win10.qcow2") + "&new=", all, http.StatusBadRequest},
		{"clone to a folder", http.MethodPost, "/clone?disk=" + q("win/win10.qcow2") + "&new=" + q("backup/"), all, http.StatusBadRequest},
		{"clone an ISO to a qcow2 name", http.MethodPost, "/clone?disk=boot.iso&new=x.qcow2", all, http.StatusBadRequest},
		{"clone a qcow2 image to a .qcow name", http.MethodPost, "/clone?disk=a.qc2&new=x.qcow", all, http.StatusBadRequest},
		{"clone job of an ISO to a qcow2 name", http.MethodPost, "/clone/jobs?disk=boot.iso&new=x.qc2", all, http.StatusBadRequest},
		{"clone job outside", http.MethodPost, "/clone/jobs?disk=" + q(outside+"/o.qc2") + "&new=x", all, http.StatusBadRequest},
		{"clone job onto an image", http.MethodPost, "/clone/jobs?disk=a.qc2&new=over.qc2", all, http.StatusConflict},
		{"clone job of a missing image", http.MethodPost, "/clone/jobs?disk=missing.qc2&new=x", all, http.StatusNotFound},
		{"clone job not allowed", http.MethodPost, "/clone/jobs?disk=a.qc2&new=x", disksRole(nil, "update", "get"), http.StatusForbidden},
		{"clone job of a disk it may not get", http.MethodPost, "/clone/jobs?disk=a.qc2&new=x", ownOnly, http.StatusForbidden},
		{"rename onto an image", http.MethodPost, "/rename?disk=a.qc2&new=over.qc2", all, http.StatusConflict},
		{"rename an ISO to an HDD name", http.MethodPost, "/rename?disk=boot.iso&new=x.hdd", all, http.StatusBadRequest},
		{"rename a qcow2 image to an ISO name", http.MethodPost, "/rename?disk=a.qc2&new=x.iso", all, http.StatusBadRequest},
		{"rename to a name with a comma", http.MethodPost, "/rename?disk=a.qc2&new=" + q("a,b"), all, http.StatusBadRequest},
		{"rename a folder", http.MethodPost, "/rename?disk=win&new=x", all, http.StatusBadRequest},
		{"rename not allowed", http.MethodPost, "/rename?disk=a.qc2&new=x", disksRole(nil, "create"), http.StatusForbidden},
		{"rename to a folder above", http.MethodPost, "/rename?disk=" + q("win/win10.qcow2") + "&new=" + q("x/.."), all, http.StatusBadRequest},
		{"delete outside", http.MethodDelete, "?disk=" + q(outside+"/o.qc2"), all, http.StatusBadRequest},
		{"delete a name like an option", http.MethodDelete, "?disk=" + q("-rf /"), all, http.StatusNotFound},
		{"delete experiment files", http.MethodDelete, "?disk=" + q("exp-a/files/s.hdd"), all, http.StatusBadRequest},
		{"delete the files directory", http.MethodDelete, "?disk=" + q(dir), all, http.StatusBadRequest},
		{"delete a folder", http.MethodDelete, "?disk=win", all, http.StatusBadRequest},
		{"delete missing", http.MethodDelete, "?disk=missing.qc2", all, http.StatusNotFound},
		{"delete not allowed", http.MethodDelete, "?disk=a.qc2", disksRole(nil, "update"), http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, runs := tree(t, filepath.Dir(dir)), files.runs()
			server := serveAs(t, "alice", tc.role).URL
			events := captureAudit(t)

			request := tc.method + " /api/v1/disks" + tc.target

			resp := do(t, tc.method, server+"/api/v1/disks"+tc.target, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("%s = %d, want %d: %s", request, resp.StatusCode, tc.want, body)
			}

			if got := files.sent(); len(got) != 0 {
				t.Errorf("%s sent minimega %q, want nothing", request, got)
			}

			if diff := cmp.Diff(before, tree(t, filepath.Dir(dir))); diff != "" {
				t.Errorf("%s changed the files (-before +after):\n%s", request, diff)
			}

			if n := files.runs() - runs; tc.want == http.StatusForbidden && n != 0 {
				t.Errorf("%s ran qemu-img %d times, want 0 for a role not allowed to act", request, n)
			}

			action, _, _ := strings.Cut(strings.TrimPrefix(tc.target, "/"), "?")
			action = strings.TrimSuffix(action, "/jobs") // a job audits its synchronous route's action

			if action == "" {
				action = "delete"
			}

			switch got := events(); tc.want {
			case http.StatusForbidden:
				wantDenied(t, got, "disk-"+action)
			case http.StatusConflict:
				wantAudit(t, got, "disk-"+action, "failure", map[string]any{"user.name": "alice"})
			default:
				// refused before the RBAC check, or failed after it: neither a
				// refusal nor a success
				if n := countAudit(got, "disk-"+action, "success", nil); n != 0 {
					t.Errorf("%s audited %d successes, want 0: %v", request, n, got)
				}

				if n := countAudit(got, "disk-"+action, "failure", map[string]any{"event.reason": "forbidden"}); n != 0 {
					t.Errorf("%s audited %d refusals, want 0: %v", request, n, got)
				}
			}
		})
	}
}

// The disk actions work on images in the files directory's folders, the
// minimega ones naming each image by its full path.
func TestDiskActionsOnNestedImages(t *testing.T) {
	files := useDiskFilesDir(t)
	dir := files.dir

	writeDisks(t, dir+"/a.qc2", dir+"/win/win10.qcow2", dir+"/win/child.qc2", dir+"/win/a+b&c.qc2", dir+"/win/a b.qc2")
	backedBy(t, dir+"/win/child.qc2", "../a.qc2")

	var (
		server = serveAs(t, "alice", disksRole(nil, "create", "update", "delete", "get")).URL + "/api/v1/disks"
		q      = url.QueryEscape
		events = captureAudit(t)
	)

	for _, tc := range []struct {
		method, target string
		sent           []string
	}{
		{http.MethodPost, "/snapshot?disk=win/win10.qcow2&new=snap", []string{
			"disk snapshot " + dir + "/win/win10.qcow2 " + dir + "/win/snap.qcow2",
		}},
		{http.MethodPost, "/commit?disk=" + q(dir+"/win/child.qc2"), []string{"disk commit " + dir + "/win/child.qc2"}},
		{http.MethodPost, "/resize?disk=win/win10.qcow2&size=" + q("+1G"), []string{"disk resize " + dir + "/win/win10.qcow2 +1G"}},
		{http.MethodPost, "/rebase?disk=win/child.qc2&backing=a.qc2&unsafe=true", []string{
			"disk set-backing " + dir + "/win/child.qc2 " + dir + "/a.qc2",
		}},
		{http.MethodPost, "/clone?disk=win/win10.qcow2&new=copy", nil},
		{http.MethodPost, "/rename?disk=win/copy.qcow2&new=moved.qc2", nil},
		{http.MethodPost, "/clone?disk=" + q("win/a+b&c.qc2") + "&new=" + q("a+b&c=2.qc2"), nil},
		{http.MethodDelete, "?disk=" + q("win/a b.qc2"), nil},
	} {
		// each request acts on what the ones before it left
		resp := do(t, tc.method, server+tc.target, "")
		if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s /api/v1/disks%s = %d, want %d: %s", tc.method, tc.target, resp.StatusCode, http.StatusOK, body)
		}

		if diff := cmp.Diff(tc.sent, files.sent(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("%s /api/v1/disks%s minimega commands mismatch (-want +got):\n%s", tc.method, tc.target, diff)
		}
	}

	for action, fields := range map[string]map[string]any{
		"disk-snapshot": {"file.path": dir + "/win/win10.qcow2", "phenix.file.target_path": dir + "/win/snap.qcow2"},
		"disk-commit":   {"file.path": dir + "/win/child.qc2", "phenix.file.target_path": dir + "/a.qc2"},
		"disk-resize":   {"file.path": dir + "/win/win10.qcow2", "phenix.disk.size": "+1G"},
		"disk-rebase":   {"file.path": dir + "/win/child.qc2", "phenix.disk.unsafe": true},
		"disk-clone":    {"file.path": dir + "/win/win10.qcow2", "phenix.file.target_path": dir + "/win/copy.qcow2"},
		"disk-rename":   {"file.path": dir + "/win/copy.qcow2", "phenix.file.target_path": dir + "/win/moved.qc2"},
		"disk-delete":   {"file.path": dir + "/win/a b.qc2", "file.name": "a b.qc2"},
	} {
		fields["user.name"] = "alice"
		wantAudit(t, events(), action, "success", fields)
	}

	// minimega is fake, so only the clones, the rename and the delete change files
	want := map[string]string{
		dir + "/a.qc2":                 dir + "/a.qc2",
		dir + "/win/win10.qcow2":       dir + "/win/win10.qcow2",
		dir + "/win/child.qc2":         dir + "/win/child.qc2",
		dir + "/win/child.qc2.backing": "../a.qc2",
		dir + "/win/a+b&c.qc2":         dir + "/win/a+b&c.qc2",
		dir + "/win/moved.qc2":         dir + "/win/win10.qcow2",
		dir + "/win/a+b&c=2.qc2":       dir + "/win/a+b&c.qc2",
	}

	if diff := cmp.Diff(want, tree(t, dir)); diff != "" {
		t.Errorf("files after the disk actions mismatch (-want +got):\n%s", diff)
	}
}

// A clone job answers 202 with the job, which copies the image, counting its
// bytes, and leaves no temporary file.
func TestCloneDiskJob(t *testing.T) {
	files := useDiskFilesDir(t)
	registry := useJobs(t)

	writeDisks(t, files.dir+"/win/win10.qcow2")

	server := serveAs(t, "alice", disksRole(nil, "create", "get")).URL + "/api/v1/disks"
	events := captureAudit(t)

	const path = "/api/v1/disks/clone/jobs?disk=win/win10.qcow2&new=copy"

	started := startClone(t, server+"/clone/jobs?disk=win/win10.qcow2&new=copy")
	size := int64(len(files.dir + "/win/win10.qcow2"))

	want := job.Job{
		Kind:       "disk.clone",
		Owner:      "alice",
		Cancelable: true,
		Progress:   job.Progress{BytesTotal: size},
		Items:      []job.Item{{Name: "copy.qcow2"}},
	}
	if diff := cmp.Diff(want, started, startedJobOpts); diff != "" {
		t.Errorf("POST %s job mismatch (-want +got):\n%s", path, diff)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusSucceeded {
		t.Errorf("POST %s job status = %s, want %s", path, j.Status, job.StatusSucceeded)
	}

	if j.Progress.Bytes != size {
		t.Errorf("POST %s job copied %d bytes, want %d", path, j.Progress.Bytes, size)
	}

	wantFiles := map[string]string{
		files.dir + "/win/win10.qcow2": files.dir + "/win/win10.qcow2",
		files.dir + "/win/copy.qcow2":  files.dir + "/win/win10.qcow2",
	}
	if diff := cmp.Diff(wantFiles, tree(t, files.dir)); diff != "" {
		t.Errorf("files after POST %s mismatch (-want +got):\n%s", path, diff)
	}

	// audited when the job finishes, as the user who started it
	got := waitAudit(t, events, "disk-clone", "success", map[string]any{
		"user.name": "alice", "file.path": files.dir + "/win/win10.qcow2",
		"phenix.file.target_path": files.dir + "/win/copy.qcow2", "phenix.job.id": started.ID,
	})

	if n := len(got); n != 1 {
		t.Errorf("POST %s logged %d audit events, want 1: %v", path, n, got)
	}
}

// A clone job whose copy fails, or that is canceled before it copies, is
// audited as a failed clone when it finishes.
func TestCloneDiskJobAuditsFailure(t *testing.T) {
	files := useDiskFilesDir(t)

	// one job at a time, so the clones wait behind a held job
	registry := useJobsIn(t, t.TempDir(), job.WithLimits(1, 1))
	events := captureAudit(t)

	writeDisks(t, files.dir+"/a.qc2", files.dir+"/b.qc2")

	server := serveAs(t, "alice", disksRole(nil, "create", "get")).URL + "/api/v1/disks"

	release := make(chan struct{})
	held := startTestJob(t, registry, "alice", "", release, "")

	failing := startClone(t, server+"/clone/jobs?disk=a.qc2&new=a-copy")
	canceled := startClone(t, server+"/clone/jobs?disk=b.qc2&new=b-copy")

	// the image is gone by the time the job runs
	if err := os.Remove(files.dir + "/a.qc2"); err != nil {
		t.Fatalf("removing a.qc2: %v", err)
	}

	cancel := strings.TrimSuffix(server, "/disks") + "/jobs/" + canceled.ID + "/cancel"
	if resp := do(t, http.MethodPost, cancel, ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/v1/jobs/%s/cancel = %d, want %d", canceled.ID, resp.StatusCode, http.StatusAccepted)
	}

	close(release)
	waitJob(t, registry, held.ID)

	if j := waitJob(t, registry, failing.ID); j.Status != job.StatusFailed {
		t.Errorf("clone job of a removed image status = %s, want %s", j.Status, job.StatusFailed)
	}

	if j := waitJob(t, registry, canceled.ID); j.Status != job.StatusCanceled {
		t.Errorf("canceled clone job status = %s, want %s", j.Status, job.StatusCanceled)
	}

	e := waitAudit(t, events, "disk-clone", "failure", map[string]any{
		"user.name": "alice", "file.path": files.dir + "/a.qc2", "phenix.job.id": failing.ID,
	})
	wantAudit(t, e, "disk-clone", "failure", map[string]any{
		"user.name": "alice", "file.path": files.dir + "/b.qc2", "phenix.job.id": canceled.ID,
		"error.message": "canceled",
	})

	if n := countAudit(events(), "disk-clone", "success", nil); n != 0 {
		t.Errorf("a failed and a canceled clone job audited %d successes, want 0", n)
	}
}

// startClone asks for the clone job at url, and returns the job started.
func startClone(t *testing.T, url string) job.Job {
	t.Helper()

	resp := do(t, http.MethodPost, url, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want %d: %s", url, resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("POST %s did not answer with a job: %v", url, err)
	}

	if loc := resp.Header.Get("Location"); loc != "/api/v1/jobs/"+started.ID {
		t.Errorf("POST %s Location = %q, want %q", url, loc, "/api/v1/jobs/"+started.ID)
	}

	return started
}

// A disk download names its image relative to the minimega files directory,
// or by an absolute path; either way it must be in that directory, and not in
// a folder the disk list leaves out.
func TestDownloadDiskRefuses(t *testing.T) {
	dir := useDiskFilesDir(t).dir

	writeDisks(t, dir+"/win/a.qc2", dir+"/exp/files/a.qc2")

	server := serveAs(t, "alice", testRole("disks", "get", "a.qc2")).URL + "/api/v1/disks/download?disk="

	for name, tc := range map[string]struct {
		disk string
		want int
		// the audit event's outcome, "" for none
		outcome string
	}{
		"in a folder":            {disk: "win/a.qc2", want: http.StatusOK, outcome: "success"},
		"absolute, in a folder":  {disk: dir + "/win/a.qc2", want: http.StatusOK, outcome: "success"},
		"missing":                {disk: "a.qc2", want: http.StatusNotFound, outcome: "failure"},
		"relative, outside":      {disk: "../a.qc2", want: http.StatusBadRequest},
		"absolute, outside":      {disk: "/etc/a.qc2", want: http.StatusBadRequest},
		"sibling sharing prefix": {disk: dir + "-other/a.qc2", want: http.StatusBadRequest},
		"the directory above":    {disk: "..", want: http.StatusBadRequest},
		"experiment files":       {disk: "exp/files/a.qc2", want: http.StatusBadRequest},
		"a hidden name":          {disk: "..a.qc2", want: http.StatusBadRequest},
		"not allowed":            {disk: "b.qc2", want: http.StatusForbidden, outcome: "failure"},
	} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)
			resp := do(t, http.MethodGet, server+url.QueryEscape(tc.disk), "")

			if resp.StatusCode != tc.want {
				t.Fatalf("GET /api/v1/disks/download?disk=%s = %d, want %d", tc.disk, resp.StatusCode, tc.want)
			}

			readBody(t, resp)

			if tc.outcome == "" {
				if got := events(); len(got) != 0 {
					t.Errorf("GET /api/v1/disks/download?disk=%s audit events = %v, want none before the RBAC check", tc.disk, got)
				}

				return
			}

			e := wantAudit(t, events(), "disk-download", tc.outcome, map[string]any{"user.name": "alice"})
			if got := e["event.reason"]; tc.want == http.StatusForbidden && e != nil && got != "forbidden" {
				t.Errorf("GET /api/v1/disks/download?disk=%s audit event.reason = %v, want forbidden", tc.disk, got)
			}
		})
	}
}

// A disk download answers HEAD and resumes with a range against its ETag.
func TestDownloadDiskResumes(t *testing.T) {
	dir := useDiskFilesDir(t).dir

	writeDisks(t, dir+"/win/a.qc2")

	server := serveAs(t, "alice", testRole("disks", "get", "a.qc2", "win")).URL + "/api/v1/disks/download?disk="
	contents := dir + "/win/a.qc2" // writeDisks writes each image's path into it

	const path = "/api/v1/disks/download?disk=win/a.qc2"

	head := do(t, http.MethodHead, server+"win/a.qc2", "")
	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD %s = %d, want %d", path, head.StatusCode, http.StatusOK)
	}

	etag := head.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("HEAD %s ETag is empty, want one", path)
	}

	if got, want := head.Header.Get("Content-Length"), strconv.Itoa(len(contents)); got != want {
		t.Errorf("HEAD %s Content-Length = %q, want %q", path, got, want)
	}

	if got := head.Header.Get("Content-Disposition"); got != "attachment; filename=a.qc2" {
		t.Errorf("HEAD %s Content-Disposition = %q, want %q", path, got, "attachment; filename=a.qc2")
	}

	resp := do(t, http.MethodGet, server+"win/a.qc2", "", "Range", "bytes=5-", "If-Range", etag)
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("GET %s from byte 5 = %d, want %d", path, resp.StatusCode, http.StatusPartialContent)
	}

	if body := readBody(t, resp); body != contents[5:] {
		t.Errorf("GET %s from byte 5 = %q, want %q", path, body, contents[5:])
	}

	if resp := do(t, http.MethodGet, server+"win", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET /api/v1/disks/download?disk=win = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestGetDiskChecksum(t *testing.T) {
	dir := useDiskFilesDir(t).dir

	writeDisks(t, dir+"/win/a.qc2", dir+"/b.qc2")

	server := serveAs(t, "alice", testRole("disks", "get", "a.qc2", "win")).URL + "/api/v1/disks/checksum?disk="

	resp := do(t, http.MethodGet, server+"win/a.qc2", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/disks/checksum?disk=win/a.qc2 = %d, want %d: %s",
			resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	var body util.Checksum
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /api/v1/disks/checksum?disk=win/a.qc2 did not answer with a checksum: %v", err)
	}

	contents := dir + "/win/a.qc2" // writeDisks writes each image's path into it
	sum := sha256.Sum256([]byte(contents))

	want := util.Checksum{Path: "win/a.qc2", Size: int64(len(contents)), SHA256: hex.EncodeToString(sum[:])}
	if diff := cmp.Diff(want, body, cmpopts.IgnoreFields(util.Checksum{}, "Modified", "ETag")); diff != "" {
		t.Errorf("GET /api/v1/disks/checksum?disk=win/a.qc2 mismatch (-want +got):\n%s", diff)
	}

	events := captureAudit(t)

	for disk, want := range map[string]int{
		"b.qc2":     http.StatusForbidden,
		"win":       http.StatusBadRequest,
		"c.qc2":     http.StatusForbidden,
		"win/c.qc2": http.StatusForbidden,
		"../a.qc2":  http.StatusBadRequest,
	} {
		if resp := do(t, http.MethodGet, server+url.QueryEscape(disk), ""); resp.StatusCode != want {
			t.Errorf("GET /api/v1/disks/checksum?disk=%s = %d, want %d", disk, resp.StatusCode, want)
		}
	}

	wantAudit(t, events(), "disk-download", "failure", map[string]any{
		"event.reason": "forbidden", "file.path": dir + "/b.qc2",
	})

	server = serveAs(t, "alice", testRole("disks", "get", "missing.qc2")).URL + "/api/v1/disks/checksum?disk="
	if resp := do(t, http.MethodGet, server+"missing.qc2", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/v1/disks/checksum?disk=missing.qc2 = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// An upload with no file, or one phenix cannot write, is answered with the
// error and writes nothing, and the server goes on serving uploads.
func TestUploadDiskRefusesWhatItCannotSave(t *testing.T) {
	dir := useDiskFilesDir(t).dir

	writeDisks(t, dir+"/win/a.qc2")

	server := serveAs(t, "alice", testRole("disks", "upload")).URL + "/api/v1/disks"

	upload := func(t *testing.T, field, filename string) *http.Response {
		t.Helper()

		body, contentType := formUpload(t, field, filename, "uploaded")

		return do(t, http.MethodPost, server, string(body), "Content-Type", contentType)
	}

	before := tree(t, dir)

	for name, tc := range map[string]struct{ field, filename string }{
		"no file":         {field: "image", filename: "x.qc2"},
		"a folder's name": {field: "file", filename: "win"},
	} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)
			defer func() { wantAudit(t, events(), "disk-upload", "failure", map[string]any{"user.name": "alice"}) }()

			resp := upload(t, tc.field, tc.filename)

			body := readBody(t, resp)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("POST /api/v1/disks of %s in field %s = %d, want %d: %s",
					tc.filename, tc.field, resp.StatusCode, http.StatusInternalServerError, body)
			}

			if !strings.HasPrefix(errorMessage(t, resp.StatusCode, body), "Error uploading") {
				t.Errorf("POST /api/v1/disks of %s in field %s = %q, want the error", tc.filename, tc.field, body)
			}

			if diff := cmp.Diff(before, tree(t, dir)); diff != "" {
				t.Errorf("POST /api/v1/disks of %s in field %s changed the files (-before +after):\n%s",
					tc.filename, tc.field, diff)
			}
		})
	}

	events := captureAudit(t)

	if resp := upload(t, "file", "new.qc2"); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/disks of new.qc2 after the refusals = %d, want %d: %s",
			resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	wantAudit(t, events(), "disk-upload", "success", map[string]any{
		"user.name": "alice", "phenix.file.target_path": dir + "/new.qc2", "file.size": len("uploaded"),
	})

	refused := serveAs(t, "alice", testRole("disks", "get")).URL + "/api/v1/disks"
	if resp := do(t, http.MethodPost, refused, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/v1/disks as a disk getter = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	wantDenied(t, events(), "disk-upload")

	if got := tree(t, dir)[dir+"/new.qc2"]; got != "uploaded" {
		t.Errorf("POST /api/v1/disks of new.qc2 wrote %q, want %q", got, "uploaded")
	}
}

// Redeploy reads its request before it locks or inspects the VM, so it
// refuses a missing or malformed request having touched nothing.
func TestRedeployRefusesABadRequestFirst(t *testing.T) {
	useAnnotatedVMExperiment(t)

	received := mmtest.Use(t, nil)

	// held by another action: a request reaching the lock would get a 409
	if err := cache.LockVMForRedeploying("test-experiment", "test-vm"); err != nil {
		t.Fatalf("cache.LockVMForRedeploying(test-experiment, test-vm) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockVM("test-experiment", "test-vm") })

	const path = "/api/v1/experiments/test-experiment/vms/test-vm/redeploy"

	server := serveAs(t, "alice", testRole("vms/redeploy", "update", "test-experiment/test-vm")).URL + path

	for name, request := range map[string]string{
		"no body":                    "",
		"malformed":                  `{"disk": `,
		"a disk minimega cannot use": `{"disk": "a b.qc2"}`,
		"a disk with a comma":        `{"disk": "a,b.qc2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPost, server, request)
			if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
				t.Errorf("POST %s %q = %d, want %d: %s", path, request, resp.StatusCode, http.StatusBadRequest, body)
			}
		})
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("bad POST %s requests sent minimega %q, want nothing", path, sent)
	}
}

// A request updating several VMs, one of them with a disk minimega cannot
// boot from, is refused whole: no VM changes, even those listed before it.
func TestUpdateVMsRefusesAnUnusableDiskWhole(t *testing.T) {
	useTestStore(t, testExperiment(t, "", diskVM("vm1", "a.qc2"), diskVM("vm2", "b.qc2")))

	server := serveAs(t, "alice", testRole("vms", "patch", "test-experiment/*")).URL

	const path = "/api/v1/experiments/test-experiment/vms"

	resp := do(t, http.MethodPatch, server+path,
		`{"total": 2, "vms": [{"name": "vm1", "disk": "c.qc2"}, {"name": "vm2", "disk": "a b.qc2"}]}`)

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH %s with vm2 on a b.qc2 = %d, want %d: %s", path, resp.StatusCode, http.StatusBadRequest, body)
	}

	if !strings.Contains(body, "vm2") {
		t.Errorf("PATCH %s with vm2 on a b.qc2 = %q, want it to name vm2", path, body)
	}

	exp, err := experiment.Get("test-experiment")
	if err != nil {
		t.Fatalf("experiment.Get(test-experiment) error = %v", err)
	}

	for name, want := range map[string]string{"vm1": "a.qc2", "vm2": "b.qc2"} {
		if got := exp.Spec.Topology().FindNodeByName(name).Hardware().Drives()[0].Image(); got != want {
			t.Errorf("after the refused PATCH %s, %s boots from %q, want %q", path, name, got, want)
		}
	}
}

// Patching a VM's disk refuses a path minimega cannot boot from, naming what
// it cannot take.
func TestPatchVMRefusesADiskMinimegaCannotUse(t *testing.T) {
	useAnnotatedVMExperiment(t)

	const request = `{"disk": "win/a b.qc2"}`

	resp := patchVM(t, vmRole("get", "patch"), request)

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH test-vm %s = %d, want %d: %s", request, resp.StatusCode, http.StatusBadRequest, body)
	}

	if !strings.Contains(body, "contains ' '") {
		t.Errorf("PATCH test-vm %s = %q, want it to name the space", request, body)
	}
}

// A VM's snapshot, restore, commit and memory snapshot refusals are audited.
func TestVMDiskActionsRefuse(t *testing.T) {
	server := serveAs(t, "alice", testRole("vms", "get", "test-experiment/test-vm")).URL +
		"/api/v1/experiments/test-experiment/vms/test-vm"

	for route, action := range map[string]string{
		"/snapshots":          "vm-snapshot",
		"/snapshots/snap.qc2": "vm-restore",
		"/commit":             "vm-commit",
		"/memorySnapshot":     "vm-memory-snapshot",
	} {
		t.Run(action, func(t *testing.T) {
			events := captureAudit(t)

			resp := do(t, http.MethodPost, server+route, `{"filename":"x.qc2"}`)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("POST .../vms/test-vm%s as a VM getter = %d, want %d: %s",
					route, resp.StatusCode, http.StatusForbidden, readBody(t, resp))
			}

			wantDenied(t, events(), action)
			wantAudit(t, events(), action, "failure", map[string]any{
				"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.vm.name": "test-vm",
			})
		})
	}
}
