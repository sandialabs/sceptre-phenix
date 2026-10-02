package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"phenix/api/disk"
	"phenix/api/disk/disktest"
	"phenix/api/experiment"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/web/cache"
	"phenix/web/rbac"
)

// diskFiles is a minimega files directory for a test.
type diskFiles struct {
	dir     string          // the files directory
	outside string          // a folder outside it
	sent    func() []string // the minimega commands sent since last asked
	runs    func() int      // the times qemu-img has run
}

// useDiskFilesDir makes a new folder the minimega files directory, as
// <PhenixBase>/images, until the test ends, puts the fake qemu-img first on
// PATH, and has the fake minimega answer every command with an empty success.
// The commands it reports sent leave out phenix's own questions about the
// files directory.
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
		t.Fatal(err)
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
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// backedBy makes the fake qemu-img report the image at path as backed by
// backing, relative to path's folder or absolute.
func backedBy(t *testing.T, path, backing string) {
	t.Helper()

	if err := os.WriteFile(path+".backing", []byte(backing), 0o600); err != nil {
		t.Fatal(err)
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
		t.Fatalf("GET /disks%s: status %d, want %d: %s", query, resp.StatusCode, want, body)
	}

	var list struct {
		Disks []listedDisk `json:"disks"`
	}

	if want == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("decoding %s: %v", body, err)
		}

		if list.Disks == nil {
			t.Fatalf("GET /disks%s = %s, want a disks list", query, body)
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

// useDiskExperiments stores exp-a, running, whose VM boots from a nested
// image backed by the files directory's a.qc2, and from o.qc2 outside it; and
// exp-b, stopped, whose VMs boot from b-only.qc2 outside it, a missing image
// and a nested image with the same name as exp-a's.
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

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GET /disks =\n%+v\nwant\n%+v", got, want)
	}

	// one experiment's images, and every image in the files directory
	got = getDisks(t, disksRole([]string{"*"}, "list"), "?expName=exp-a", http.StatusOK)

	paths := fullPaths(got)
	if slices.Contains(paths, outside+"/b-only.qc2") || !slices.Contains(paths, outside+"/o.qc2") {
		t.Errorf("GET /disks?expName=exp-a lists %q, want exp-a's outside images only", paths)
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
		t.Error("lists an outside image only an experiment the role cannot get uses")
	}

	if _, ok := disks[outside+"/base.qc2"]; !ok {
		t.Error("leaves out an outside image backing an image the role sees")
	}

	if got := disks[dir+"/a.qc2"].Experiments; !reflect.DeepEqual(got, []disk.ExperimentUse{{Name: "exp-a", Running: true}}) {
		t.Errorf("a.qc2 experiments = %+v, want exp-a only", got)
	}

	getDisks(t, disksRole([]string{"exp-a"}, "list"), "?expName=exp-b", http.StatusForbidden)

	// a disk's name covers the file of that name in every folder
	role := combinedRole(testRole("disks", "list", "win10.qcow2"), testRole("experiments", "get", "*"))

	paths := fullPaths(getDisks(t, role, "", http.StatusOK))
	if want := []string{dir + "/linux/win10.qcow2", dir + "/win/win10.qcow2"}; !slices.Equal(paths, want) {
		t.Errorf("a role listing win10.qcow2 sees %q, want %q", paths, want)
	}

	// a role that sees no image gets an empty list
	if got := getDisks(t, testRole("disks", "list", "none.qc2"), "", http.StatusOK); len(got) != 0 {
		t.Errorf("a role listing none.qc2 sees %q, want nothing", fullPaths(got))
	}
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
		t.Fatal(err)
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
		t.Fatal(err)
	}

	return files
}

// The disk actions refuse, before touching anything, paths outside the files
// directory or in the folders the listing leaves out, names minimega cannot
// use, and anything the role may not do.
func TestDiskActionsRefuse(t *testing.T) {
	files := useDiskExperiments(t)
	dir, outside := files.dir, files.outside

	writeDisks(t, dir+"/win/a b.qc2", dir+"/win/a#b.qc2")

	all := disksRole([]string{"*"}, "create", "update", "delete", "get")
	q := url.QueryEscape

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
		{"clone to no name", http.MethodPost, "/clone?disk=" + q("win/win10.qcow2") + "&new=", all, http.StatusBadRequest},
		{"clone to a folder", http.MethodPost, "/clone?disk=" + q("win/win10.qcow2") + "&new=" + q("backup/"), all, http.StatusBadRequest},
		{"rename onto an image", http.MethodPost, "/rename?disk=a.qc2&new=over.qc2", all, http.StatusConflict},
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

			resp := do(t, tc.method, serveAs(t, "alice", tc.role).URL+"/api/v1/disks"+tc.target, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.want, body)
			}

			if got := files.sent(); len(got) != 0 {
				t.Errorf("sent minimega %q", got)
			}

			if !reflect.DeepEqual(tree(t, filepath.Dir(dir)), before) {
				t.Error("changed the files")
			}

			if tc.want == http.StatusForbidden && files.runs() != runs {
				t.Error("inspected an image for a role not allowed to act on it")
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
		server = serveAs(t, "alice", disksRole(nil, "create", "update", "delete")).URL + "/api/v1/disks"
		q      = url.QueryEscape
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
		resp := do(t, tc.method, server+tc.target, "")
		if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status %d: %s", tc.method, tc.target, resp.StatusCode, body)
		}

		if got := files.sent(); !slices.Equal(got, tc.sent) {
			t.Errorf("%s %s sent %q, want %q", tc.method, tc.target, got, tc.sent)
		}
	}

	written := tree(t, dir)

	for path, want := range map[string]string{
		dir + "/win/moved.qc2":   dir + "/win/win10.qcow2",
		dir + "/win/a+b&c=2.qc2": dir + "/win/a+b&c.qc2",
		dir + "/win/win10.qcow2": dir + "/win/win10.qcow2",
		dir + "/win/copy.qcow2":  "",
		dir + "/win/a b.qc2":     "",
	} {
		if got, ok := written[path]; want == "" && ok || want != "" && got != want {
			t.Errorf("%s holds %q (exists: %t), want %q", path, got, ok, want)
		}
	}
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
	}{
		"in a folder":            {disk: "win/a.qc2", want: http.StatusOK},
		"absolute, in a folder":  {disk: dir + "/win/a.qc2", want: http.StatusOK},
		"missing":                {disk: "a.qc2", want: http.StatusNotFound},
		"relative, outside":      {disk: "../a.qc2", want: http.StatusBadRequest},
		"absolute, outside":      {disk: "/etc/a.qc2", want: http.StatusBadRequest},
		"sibling sharing prefix": {disk: dir + "-other/a.qc2", want: http.StatusBadRequest},
		"the directory above":    {disk: "..", want: http.StatusBadRequest},
		"experiment files":       {disk: "exp/files/a.qc2", want: http.StatusBadRequest},
		"a hidden name":          {disk: "..a.qc2", want: http.StatusBadRequest},
		"not allowed":            {disk: "b.qc2", want: http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodGet, server+url.QueryEscape(tc.disk), "")

			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, resp.StatusCode)
			}
		})
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

		var body bytes.Buffer

		form := multipart.NewWriter(&body)

		part, err := form.CreateFormFile(field, filename)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := part.Write([]byte("uploaded")); err != nil {
			t.Fatal(err)
		}

		if err := form.Close(); err != nil {
			t.Fatal(err)
		}

		return do(t, http.MethodPost, server, body.String(), "Content-Type", form.FormDataContentType())
	}

	before := tree(t, dir)

	for name, tc := range map[string]struct{ field, filename string }{
		"no file":         {field: "image", filename: "x.qc2"},
		"a folder's name": {field: "file", filename: "win"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := upload(t, tc.field, tc.filename)
			if body := readBody(t, resp); resp.StatusCode != http.StatusInternalServerError ||
				!strings.HasPrefix(body, "Error uploading") {
				t.Errorf("status %d: %s; want 500 with the error", resp.StatusCode, body)
			}

			if !reflect.DeepEqual(tree(t, dir), before) {
				t.Error("changed the files")
			}
		})
	}

	if resp := upload(t, "file", "new.qc2"); resp.StatusCode != http.StatusOK {
		t.Fatalf("uploading after the refusals: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	if got := tree(t, dir)[dir+"/new.qc2"]; got != "uploaded" {
		t.Errorf("uploaded new.qc2 holds %q, want %q", got, "uploaded")
	}
}

// Redeploy reads its request before it locks or inspects the VM, so it
// refuses a missing or malformed request having touched nothing.
func TestRedeployRefusesABadRequestFirst(t *testing.T) {
	useAnnotatedVMExperiment(t)

	received := mmtest.Use(t, nil)

	// held by another action: a request reaching the lock would get a 409
	if err := cache.LockVMForRedeploying("test-experiment", "test-vm"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { cache.UnlockVM("test-experiment", "test-vm") })

	server := serveAs(t, "alice", testRole("vms/redeploy", "update", "test-experiment/test-vm")).URL +
		"/api/v1/experiments/test-experiment/vms/test-vm/redeploy"

	for name, request := range map[string]string{
		"no body":                    "",
		"malformed":                  `{"disk": `,
		"a disk minimega cannot use": `{"disk": "a b.qc2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPost, server, request)
			if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status %d: %s; want 400", resp.StatusCode, body)
			}
		})
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("sent minimega %q", sent)
	}
}

// A request updating several VMs, one of them with a disk minimega cannot
// boot from, is refused whole: no VM changes, even those listed before it.
func TestUpdateVMsRefusesAnUnusableDiskWhole(t *testing.T) {
	useTestStore(t, testExperiment(t, "", diskVM("vm1", "a.qc2"), diskVM("vm2", "b.qc2")))

	server := serveAs(t, "alice", testRole("vms", "patch", "test-experiment/*")).URL

	resp := do(t, http.MethodPatch, server+"/api/v1/experiments/test-experiment/vms",
		`{"total": 2, "vms": [{"name": "vm1", "disk": "c.qc2"}, {"name": "vm2", "disk": "a b.qc2"}]}`)
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "vm2") {
		t.Errorf("status %d: %s; want 400 naming vm2", resp.StatusCode, body)
	}

	exp, err := experiment.Get("test-experiment")
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"vm1": "a.qc2", "vm2": "b.qc2"} {
		if got := exp.Spec.Topology().FindNodeByName(name).Hardware().Drives()[0].Image(); got != want {
			t.Errorf("%s boots from %q, want %q", name, got, want)
		}
	}
}

// A VM's disk must be a path minimega can boot from: one argument, with
// nothing it would expand or read as a disk option.
func TestVMDiskMustSuitMinimega(t *testing.T) {
	useAnnotatedVMExperiment(t)

	resp := patchVM(t, vmRole("get", "patch"), `{"disk": "win/a b.qc2"}`)
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "contains ' '") {
		t.Errorf("patching a VM's disk to a name with a space: status %d: %s; want 400 naming the space", resp.StatusCode, body)
	}

	server := serveAs(t, "alice", testRole("vms/redeploy", "update", "test-experiment/test-vm")).URL

	resp = do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/vms/test-vm/redeploy", `{"disk": "a,b.qc2"}`)
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "contains ','") {
		t.Errorf("redeploying a VM from a name with a comma: status %d: %s; want 400 naming the comma", resp.StatusCode, body)
	}
}
