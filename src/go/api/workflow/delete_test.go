package workflow_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// deleteExp returns an experiment named name, running when running is true,
// whose topology has one node for each list of injection sources in nodes.
func deleteExp(name string, running bool, nodes ...[]string) types.Experiment {
	exp := planExp(name, running)

	topology := new(v1.TopologySpec)

	for _, srcs := range nodes {
		node := new(v1.Node)

		for _, src := range srcs {
			node.InjectionsF = append(node.InjectionsF, &v1.Injection{SrcF: src, DstF: "/dst"})
		}

		topology.NodesF = append(topology.NodesF, node)
	}

	exp.Spec = &v1.ExperimentSpec{TopologyF: topology}

	return exp
}

func TestNewDeletePlan(t *testing.T) {
	tests := []struct {
		name    string
		mapped  []types.Experiment
		force   bool
		want    workflow.DeletePlan
		wantIs  error
		wantErr []string
	}{
		{name: "none mapped", want: workflow.DeletePlan{}},
		{name: "none mapped, force", force: true, want: workflow.DeletePlan{}},
		{
			name:   "stopped",
			mapped: []types.Experiment{planExp("lab", false)},
			want:   workflow.DeletePlan{Experiment: "lab"},
		},
		{
			name:   "stopped, force",
			mapped: []types.Experiment{planExp("lab", false)},
			force:  true,
			want:   workflow.DeletePlan{Experiment: "lab"},
		},
		{
			name:   "running, force",
			mapped: []types.Experiment{planExp("lab", true)},
			force:  true,
			want:   workflow.DeletePlan{Experiment: "lab", Stop: true},
		},
		{
			name:    "running without force",
			mapped:  []types.Experiment{planExp("lab", true)},
			want:    workflow.DeletePlan{Experiment: "lab"},
			wantIs:  workflow.ErrRunning,
			wantErr: []string{`"lab"`, "running", "force", "-f"},
		},
		{
			name:    "two mapped",
			mapped:  []types.Experiment{planExp("b", false), planExp("a", true)},
			force:   true,
			wantIs:  workflow.ErrConflict,
			wantErr: []string{"2 experiments", `"main"`, "(a, b)"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflow.NewDeletePlan("main", tc.mapped, tc.force)

			if !errors.Is(err, tc.wantIs) || (tc.wantIs == nil) != (err == nil) {
				t.Fatalf("NewDeletePlan() error = %v, want %v", err, tc.wantIs)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("NewDeletePlan() error = %q, want it to contain %q", err, want)
				}
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("NewDeletePlan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInjectsUsers(t *testing.T) {
	const dir = "/phenix/injects/lab"

	tests := []struct {
		name   string
		exps   []types.Experiment
		dir    string
		except string
		want   []string
	}{
		{name: "no experiments", dir: dir, want: []string{}},
		{
			name: "source equal to the directory",
			exps: []types.Experiment{deleteExp("a", false, []string{dir})},
			dir:  dir,
			want: []string{"a"},
		},
		{
			name: "source inside the directory",
			exps: []types.Experiment{deleteExp("a", true, []string{dir + "/x/y.txt"})},
			dir:  dir,
			want: []string{"a"},
		},
		{
			name: "source with a shared name prefix",
			exps: []types.Experiment{deleteExp("a", false, []string{"/phenix/injects/lab-a/x", "/phenix/injects/labx"})},
			dir:  dir,
			want: []string{},
		},
		{
			name: "parent of the directory",
			exps: []types.Experiment{deleteExp("a", false, []string{"/phenix/injects"})},
			dir:  dir,
			want: []string{},
		},
		{
			name: "relative source",
			exps: []types.Experiment{deleteExp("a", false, []string{"lab/x", "phenix/injects/lab/x"})},
			dir:  dir,
			want: []string{},
		},
		{
			name: "source that is not clean",
			exps: []types.Experiment{deleteExp("a", false, []string{"/phenix/injects/other/../lab//x"})},
			dir:  dir,
			want: []string{"a"},
		},
		{
			name: "directory that is not clean",
			exps: []types.Experiment{deleteExp("a", false, []string{dir + "/x"})},
			dir:  "/phenix//injects/lab/",
			want: []string{"a"},
		},
		{
			name: "except is left out",
			exps: []types.Experiment{
				deleteExp("lab", true, []string{dir + "/x"}),
				deleteExp("other", false, []string{dir + "/y"}),
			},
			dir:    dir,
			except: "lab",
			want:   []string{"other"},
		},
		{
			name: "sorted, and one entry for each experiment",
			exps: []types.Experiment{
				deleteExp("c", false, []string{dir + "/a"}, []string{dir + "/b"}),
				deleteExp("a", false, []string{"/elsewhere", dir}),
				deleteExp("b", false, []string{"/elsewhere"}),
			},
			dir:  dir,
			want: []string{"a", "c"},
		},
		{
			name: "experiment without a spec",
			exps: []types.Experiment{{Metadata: store.ConfigMetadata{Name: "a"}}},
			dir:  dir,
			want: []string{},
		},
		{
			name: "experiment without a topology",
			exps: []types.Experiment{{Metadata: store.ConfigMetadata{Name: "a"}, Spec: &v1.ExperimentSpec{}}},
			dir:  dir,
			want: []string{},
		},
		{
			name: "relative directory",
			exps: []types.Experiment{deleteExp("a", false, []string{"/lab/x"})},
			dir:  "lab",
			want: []string{},
		},
		{
			name: "root directory",
			exps: []types.Experiment{deleteExp("a", false, []string{"/x"}), deleteExp("b", false, []string{"x"})},
			dir:  "/",
			want: []string{"a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflow.InjectsUsers(tc.exps, tc.dir, tc.except)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("InjectsUsers(%q, %q) mismatch (-want +got):\n%s", tc.dir, tc.except, diff)
			}
		})
	}
}

// deleteLayout is an injects base with symbolic links to its parts: root
// holds it, and labs, labAs and bases are the paths of the lab directory, of
// lab-a and of the base, as written and through each link.
type deleteLayout struct {
	root               string
	labs, labAs, bases []string
}

// deleteLinkedLayout builds, in a new directory with its symbolic links
// resolved, the injects base data/injects with the directories lab and lab-a,
// each with the file motd, and links to the parts of it.
func deleteLinkedLayout(t *testing.T) deleteLayout {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	data := filepath.Join(root, "data", "injects")

	for _, dir := range []string{"lab", "lab-a"} {
		stageFile(t, filepath.Join(data, dir, "motd"), "hello", 0o644)
	}

	stageSymlink(t, filepath.Join(root, "data"), filepath.Join(root, "phenix"))
	stageSymlink(t, data, filepath.Join(root, "injects"))
	stageSymlink(t, filepath.Join(data, "lab"), filepath.Join(root, "lab"))

	layout := deleteLayout{
		root:  root,
		bases: []string{data, filepath.Join(root, "phenix", "injects"), filepath.Join(root, "injects")},
	}

	layout.labs = append(make([]string, 0, 1+len(layout.bases)), filepath.Join(root, "lab"))
	layout.labAs = make([]string, 0, len(layout.bases))

	for _, base := range layout.bases {
		layout.labs = append(layout.labs, filepath.Join(base, "lab"))
		layout.labAs = append(layout.labAs, filepath.Join(base, "lab-a"))
	}

	return layout
}

// TestInjectsUsersSymlinks checks sources and directories that name the same
// directory through a symbolic link, in both directions, and a source that
// does not exist yet under a linked parent.
func TestInjectsUsersSymlinks(t *testing.T) {
	root := deleteLinkedLayout(t).root

	var (
		realDir = filepath.Join(root, "data", "injects", "lab")
		linked  = filepath.Join(root, "phenix", "injects", "lab")
	)

	tests := []struct {
		name string
		src  string
		dir  string
		want []string
	}{
		{name: "real source, linked directory", src: realDir + "/motd", dir: linked, want: []string{"a"}},
		{name: "linked source, real directory", src: linked + "/motd", dir: realDir, want: []string{"a"}},
		{name: "linked source that does not exist yet", src: linked + "/new/file", dir: realDir, want: []string{"a"}},
		{name: "real source, link to the directory", src: realDir + "/motd", dir: root + "/lab", want: []string{"a"}},
		{name: "linked directory that does not exist yet", src: realDir + "2/x", dir: linked + "2", want: []string{"a"}},
		{name: "source in the parent of a link target", src: root + "/lab/../lab-a/motd", dir: realDir + "-a", want: []string{"a"}},
		{name: "directory in the parent of a link target", src: realDir + "-a/motd", dir: root + "/lab/../lab-a", want: []string{"a"}},
		{
			name: "source in the parent of a link target that does not exist yet",
			src:  root + "/lab/../lab-a/new/file",
			dir:  realDir + "-a",
			want: []string{"a"},
		},
		{name: "linked source in a sibling", src: linked + "-a/motd", dir: realDir, want: []string{}},
		{name: "linked source in the parent", src: filepath.Dir(linked), dir: realDir, want: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflow.InjectsUsers([]types.Experiment{deleteExp("a", false, []string{tc.src})}, tc.dir, "")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("InjectsUsers(%q, %q) mismatch (-want +got):\n%s", tc.src, tc.dir, diff)
			}
		})
	}
}

// TestInjectsUsersProperties checks InjectsUsers over sources that name the
// lab directory, a sibling or the parent through generated symbolic link
// forms: the result is never nil, is sorted, has no duplicate, never holds
// except, and names exactly the experiments with a source in lab.
func TestInjectsUsersProperties(t *testing.T) {
	layout := deleteLinkedLayout(t)

	type source struct {
		path string
		in   bool
	}

	src := rapid.Custom(func(t *rapid.T) source {
		switch rapid.IntRange(0, 4).Draw(t, "kind") {
		case 0:
			suffix := rapid.SampledFrom([]string{"", "/motd", "/new/file"}).Draw(t, "suffix")

			return source{path: rapid.SampledFrom(layout.labs).Draw(t, "lab") + suffix, in: true}
		case 1:
			suffix := rapid.SampledFrom([]string{"", "/motd"}).Draw(t, "suffix")

			return source{path: rapid.SampledFrom(layout.labAs).Draw(t, "sibling") + suffix}
		case 2:
			return source{path: rapid.SampledFrom(layout.bases).Draw(t, "parent")}
		case 3:
			return source{path: "lab/motd"}
		default:
			return source{path: "/phenix-delete-test-missing/lab/motd"}
		}
	})

	rapid.Check(t, func(t *rapid.T) {
		var (
			want   = map[string]bool{}
			except = rapid.SampledFrom([]string{"", "a", "lab"}).Draw(t, "except")
			dir    = rapid.SampledFrom(layout.labs).Draw(t, "dir")
			count  = rapid.IntRange(0, 4).Draw(t, "exps")
			exps   = make([]types.Experiment, 0, count)
		)

		for i := range count {
			name := rapid.SampledFrom([]string{"a", "b", "c", "lab"}).Draw(t, "name"+strconv.Itoa(i))
			drawn := rapid.SliceOfN(rapid.SliceOfN(src, 0, 3), 0, 3).Draw(t, "nodes"+strconv.Itoa(i))

			nodes := make([][]string, 0, len(drawn))

			for _, node := range drawn {
				paths := make([]string, 0, len(node))

				for _, s := range node {
					paths = append(paths, s.path)

					if s.in && name != except {
						want[name] = true
					}
				}

				nodes = append(nodes, paths)
			}

			exps = append(exps, deleteExp(name, false, nodes...))
		}

		got := workflow.InjectsUsers(exps, dir, except)

		if got == nil {
			t.Fatal("InjectsUsers() = nil, want a non-nil list")
		}

		if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Fatalf("InjectsUsers() = %q, want a sorted list without duplicates", got)
		}

		wantNames := slices.Sorted(maps.Keys(want))
		if wantNames == nil {
			wantNames = []string{}
		}

		if diff := cmp.Diff(wantNames, got); diff != "" {
			t.Fatalf("InjectsUsers(%s, %q) mismatch (-want +got):\n%s", dir, except, diff)
		}
	})
}

// TestDeleteResultJSON checks the field names of a delete response, and that
// a nil injectsUsedBy list is an empty array.
func TestDeleteResultJSON(t *testing.T) {
	result := workflow.DeleteResult{
		Branch: "lab", DryRun: true, Experiment: "lab", Stopped: true,
		InjectsDir: "/phenix/injects/lab", InjectsUsedBy: nil,
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", raw, err)
	}

	want := map[string]any{
		"branch": "lab", "dryRun": true, "experiment": "lab", "stopped": true,
		"injectsDir": "/phenix/injects/lab", "injectsUsedBy": []any{},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DeleteResult JSON mismatch (-want +got):\n%s", diff)
	}
}

// TestResolvedPath checks that links resolve before "..", and that the part
// of a path that does not exist follows as written.
func TestResolvedPath(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir) error = %v", err)
	}

	for _, dir := range []string{"dir", "inj"} {
		if err := os.MkdirAll(filepath.Join(root, "t", dir), 0o750); err != nil {
			t.Fatalf("MkdirAll error = %v", err)
		}
	}

	if err := os.Mkdir(filepath.Join(root, "x"), 0o750); err != nil {
		t.Fatalf("Mkdir error = %v", err)
	}

	if err := os.Symlink(filepath.Join(root, "t", "dir"), filepath.Join(root, "x", "link")); err != nil {
		t.Fatalf("Symlink error = %v", err)
	}

	sep := string(filepath.Separator)
	// Join would clean the "..", so the path is built by hand.
	throughLink := root + sep + "x" + sep + "link" + sep + ".." + sep + "inj"

	tests := []struct {
		name string
		path string
		want string
	}{
		{"existing path through a link", filepath.Join(root, "x", "link"), filepath.Join(root, "t", "dir")},
		{"missing file under a link", filepath.Join(root, "x", "link", "new"), filepath.Join(root, "t", "dir", "new")},
		{"existing path in the parent of a link target", throughLink, filepath.Join(root, "t", "inj")},
		{
			"missing file in the parent of a link target",
			throughLink + sep + "new" + sep + "file",
			filepath.Join(root, "t", "inj", "new", "file"),
		},
		{"missing directory in the parent of a link target", throughLink + "2" + sep + "file", filepath.Join(root, "t", "inj2", "file")},
		{"parent of a missing directory", root + sep + "a" + sep + "b" + sep + ".." + sep + "c", filepath.Join(root, "a", "c")},
		{
			"repeated separators before a missing file",
			filepath.Join(root, "x", "link") + sep + sep + "new",
			filepath.Join(root, "t", "dir", "new"),
		},
		{"missing path with a trailing separator", filepath.Join(root, "a", "b") + sep, filepath.Join(root, "a", "b")},
		{"root", sep, sep},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := workflow.ResolvedPath(tt.path); got != tt.want {
				t.Errorf("ResolvedPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
