package workflow_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/workflow"
)

// The random suffixes of two staging leftovers of a name.
const (
	unstageTokenA = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	unstageTokenB = "234567ABCDEFGHIJKLMNOPQRST"
)

// unstageEntries returns the names of the entries in dir, sorted.
func unstageEntries(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// unstageCase is one layout of an injects base for the tests of
// FindStagedInjects and RemoveStagedInjects. setup builds it in root and
// returns the base and the protect paths. want is relative to the resolved
// base, and kept lists the entries of the resolved base after the removal.
type unstageCase struct {
	name    string
	setup   func(t *testing.T, root string) (string, []string)
	keepDir bool
	want    workflow.Staged
	kept    []string
}

// unstageCases returns the layouts that both FindStagedInjects and
// RemoveStagedInjects accept.
func unstageCases() []unstageCase {
	plainBase := func(t *testing.T, root string) string {
		t.Helper()

		base := filepath.Join(root, "injects")
		stageMkdir(t, base, 0o755)

		return base
	}

	return []unstageCase{
		{
			name: "directory present",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, "lab", "scripts", "a.sh"), "a", 0o644)

				return base, nil
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{},
		},
		{
			name: "directory absent",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, "other", "a.sh"), "a", 0o644)

				return base, nil
			},
			want: workflow.Staged{},
			kept: []string{"other"},
		},
		{
			name: "symlink removed and its target kept",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(root, "target", "keep.txt"), "keep", 0o644)
				stageSymlink(t, filepath.Join(root, "target"), filepath.Join(base, "lab"))

				return base, nil
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{},
		},
		{
			name: "dangling symlink removed",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageSymlink(t, filepath.Join(root, "missing"), filepath.Join(base, "lab"))

				return base, nil
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{},
		},
		{
			name: "leftovers removed",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, "lab", "a.sh"), "a", 0o644)
				stageFile(t, filepath.Join(base, ".lab.tmp-"+unstageTokenA, "a.sh"), "a", 0o644)
				stageFile(t, filepath.Join(base, ".lab.old-"+unstageTokenB, "a.sh"), "a", 0o644)

				return base, nil
			},
			want: workflow.Staged{Dir: "lab", Leftovers: []string{".lab.old-" + unstageTokenB, ".lab.tmp-" + unstageTokenA}},
			kept: []string{},
		},
		{
			name: "only leftovers",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, ".lab.tmp-"+unstageTokenA, "a.sh"), "a", 0o644)

				return base, nil
			},
			want: workflow.Staged{Leftovers: []string{".lab.tmp-" + unstageTokenA}},
			kept: []string{},
		},
		{
			name: "directory kept and leftovers removed",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, "lab", "a.sh"), "a", 0o644)
				stageFile(t, filepath.Join(base, ".lab.old-"+unstageTokenB, "a.sh"), "a", 0o644)

				return base, nil
			},
			keepDir: true,
			want:    workflow.Staged{Leftovers: []string{".lab.old-" + unstageTokenB}},
			kept:    []string{"lab"},
		},
		{
			name: "siblings with a shared prefix kept",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				for _, entry := range []string{
					"lab", "lab-a", "lab.old", ".lab-a.tmp-" + unstageTokenA, ".lab.tmp-short", "lab.tmp-" + unstageTokenA,
				} {
					stageFile(t, filepath.Join(base, entry, "a.sh"), "a", 0o644)
				}

				return base, nil
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{".lab-a.tmp-" + unstageTokenA, ".lab.tmp-short", "lab-a", "lab.old", "lab.tmp-" + unstageTokenA},
		},
		{
			name: "base is a symlink",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				target := filepath.Join(root, "real")
				stageFile(t, filepath.Join(target, "lab", "a.sh"), "a", 0o644)
				stageFile(t, filepath.Join(target, "lab-a", "a.sh"), "a", 0o644)

				base := filepath.Join(root, "injects")
				stageSymlink(t, target, base)

				return base, nil
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{"lab-a"},
		},
		{
			name: "base missing",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				return filepath.Join(root, "missing"), nil
			},
			want: workflow.Staged{},
		},
		{
			name: "protect path outside the directory",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageFile(t, filepath.Join(base, "lab", "a.sh"), "a", 0o644)
				stageMkdir(t, filepath.Join(base, "lab-a"), 0o755)

				return base, []string{root, filepath.Join(base, "lab-a")}
			},
			want: workflow.Staged{Dir: "lab"},
			kept: []string{"lab-a"},
		},
		{
			name: "protect path inside a kept directory",
			setup: func(t *testing.T, root string) (string, []string) {
				t.Helper()

				base := plainBase(t, root)
				stageMkdir(t, filepath.Join(base, "lab", "work"), 0o755)

				return base, []string{filepath.Join(base, "lab", "work")}
			},
			keepDir: true,
			want:    workflow.Staged{},
			kept:    []string{"lab"},
		},
	}
}

// unstageWant returns want with each path joined to the resolved base.
func unstageWant(t *testing.T, base string, want workflow.Staged) workflow.Staged {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(base)
	if errors.Is(err, fs.ErrNotExist) {
		return want
	}

	if err != nil {
		t.Fatal(err)
	}

	if want.Dir != "" {
		want.Dir = filepath.Join(resolved, want.Dir)
	}

	leftovers := make([]string, 0, len(want.Leftovers))
	for _, leftover := range want.Leftovers {
		leftovers = append(leftovers, filepath.Join(resolved, leftover))
	}

	want.Leftovers = leftovers

	return want
}

func TestFindStagedInjects(t *testing.T) {
	for _, tt := range unstageCases() {
		t.Run(tt.name, func(t *testing.T) {
			if tt.keepDir {
				t.Skip("FindStagedInjects always lists the directory")
			}

			root := t.TempDir()
			base, protect := tt.setup(t, root)

			before := treeSnapshotOrNil(t, root)

			got, err := workflow.FindStagedInjects(base, "lab", protect...)
			if err != nil {
				t.Fatalf("FindStagedInjects(%q, lab) error = %v", base, err)
			}

			if diff := cmp.Diff(unstageWant(t, base, tt.want), got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("FindStagedInjects(%q, lab) mismatch (-want +got):\n%s", base, diff)
			}

			if diff := cmp.Diff(before, treeSnapshotOrNil(t, root)); diff != "" {
				t.Errorf("FindStagedInjects(%q, lab) changed the tree (-before +after):\n%s", base, diff)
			}
		})
	}
}

func TestRemoveStagedInjects(t *testing.T) {
	for _, tt := range unstageCases() {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			base, protect := tt.setup(t, root)
			outside := treeSnapshotOrNil(t, filepath.Join(root, "target"))

			got, err := workflow.RemoveStagedInjects(base, "lab", workflow.RemoveOptions{KeepDir: tt.keepDir, Protect: protect})
			if err != nil {
				t.Fatalf("RemoveStagedInjects(%q, lab) error = %v", base, err)
			}

			if diff := cmp.Diff(unstageWant(t, base, tt.want), got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("RemoveStagedInjects(%q, lab) mismatch (-want +got):\n%s", base, diff)
			}

			if _, err := os.Stat(base); err == nil {
				if diff := cmp.Diff(tt.kept, unstageEntries(t, base), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("entries of %s after RemoveStagedInjects mismatch (-want +got):\n%s", base, diff)
				}
			}

			if diff := cmp.Diff(outside, treeSnapshotOrNil(t, filepath.Join(root, "target"))); diff != "" {
				t.Errorf("RemoveStagedInjects(%q, lab) changed a symlink target (-before +after):\n%s", base, diff)
			}

			// A second run finds nothing more to remove.
			again, err := workflow.RemoveStagedInjects(base, "lab", workflow.RemoveOptions{KeepDir: tt.keepDir, Protect: protect})
			if err != nil {
				t.Fatalf("second RemoveStagedInjects(%q, lab) error = %v", base, err)
			}

			if diff := cmp.Diff(workflow.Staged{}, again, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("second RemoveStagedInjects(%q, lab) mismatch (-want +got):\n%s", base, diff)
			}
		})
	}
}

// treeSnapshotOrNil returns the stageSnapshot of root, or nil when root does
// not exist.
func treeSnapshotOrNil(t *testing.T, root string) map[string]string {
	t.Helper()

	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	snapshot, err := stageSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}

	return snapshot
}

func TestRemoveStagedInjectsRefuses(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		setup  func(t *testing.T, base string) []string
		wantIs error
	}{
		{
			name:   "invalid name",
			value:  "../lab",
			setup:  func(*testing.T, string) []string { return nil },
			wantIs: workflow.ErrInvalidName,
		},
		{
			name:  "protect path is the directory",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				return []string{filepath.Join(base, "lab")}
			},
			wantIs: workflow.ErrUnsafeDestination,
		},
		{
			name:  "protect path inside the directory",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				stageMkdir(t, filepath.Join(base, "lab", "work"), 0o755)

				return []string{filepath.Join(base, "lab", "work")}
			},
			wantIs: workflow.ErrUnsafeDestination,
		},
		{
			name:  "protect path inside a leftover",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				leftover := filepath.Join(base, ".lab.tmp-"+unstageTokenA)
				stageMkdir(t, leftover, 0o755)

				return []string{leftover}
			},
			wantIs: workflow.ErrUnsafeDestination,
		},
		{
			name:  "protect path through a symlink to the directory",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				link := filepath.Join(filepath.Dir(base), "link")
				stageSymlink(t, filepath.Join(base, "lab"), link)

				return []string{link}
			},
			wantIs: workflow.ErrUnsafeDestination,
		},
		{
			name:  "directory is a file",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				if err := os.RemoveAll(filepath.Join(base, "lab")); err != nil {
					t.Fatal(err)
				}

				stageFile(t, filepath.Join(base, "lab"), "not a directory", 0o644)

				return nil
			},
			wantIs: workflow.ErrUnsafeDestination,
		},
		{
			name:  "missing protect path",
			value: "lab",
			setup: func(t *testing.T, base string) []string {
				t.Helper()

				return []string{filepath.Join(base, "missing")}
			},
			wantIs: fs.ErrNotExist,
		},
	}

	for _, tt := range tests {
		for _, find := range []bool{true, false} {
			t.Run(tt.name+"/find="+strconv.FormatBool(find), func(t *testing.T) {
				root := t.TempDir()
				base := filepath.Join(root, "injects")
				stageFile(t, filepath.Join(base, "lab", "a.sh"), "a", 0o644)
				stageFile(t, filepath.Join(base, ".lab.old-"+unstageTokenB, "a.sh"), "a", 0o644)

				protect := tt.setup(t, base)
				before := treeSnapshotOrNil(t, root)

				var err error
				if find {
					_, err = workflow.FindStagedInjects(base, tt.value, protect...)
				} else {
					_, err = workflow.RemoveStagedInjects(base, tt.value, workflow.RemoveOptions{Protect: protect})
				}

				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("error = %v, want errors.Is(err, %v)", err, tt.wantIs)
				}

				if diff := cmp.Diff(before, treeSnapshotOrNil(t, root)); diff != "" {
					t.Errorf("a refused call changed the tree (-before +after):\n%s", diff)
				}
			})
		}
	}
}

func TestRemoveStagedInjectsPermissionError(t *testing.T) {
	stageSkipIfRoot(t)

	root := t.TempDir()
	base := filepath.Join(root, "injects")
	stageFile(t, filepath.Join(base, "lab", "a.sh"), "a", 0o644)
	stageMkdir(t, base, 0o555)

	got, err := workflow.RemoveStagedInjects(base, "lab", workflow.RemoveOptions{})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("RemoveStagedInjects() error = %v, want errors.Is(err, fs.ErrPermission)", err)
	}

	if diff := cmp.Diff(workflow.Staged{}, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("RemoveStagedInjects() mismatch (-want +got):\n%s", diff)
	}
}

// TestRemoveStagedInjectsPartialFailure removes a directory that holds a
// directory it cannot empty. It still removes the leftover, and returns it
// with the error.
func TestRemoveStagedInjectsPartialFailure(t *testing.T) {
	stageSkipIfRoot(t)

	base := filepath.Join(t.TempDir(), "injects")
	leftover := ".lab.tmp-" + unstageTokenA

	stageFile(t, filepath.Join(base, "lab", "sub", "file"), "a", 0o644)
	stageMkdir(t, filepath.Join(base, "lab", "sub"), 0o555)
	stageFile(t, filepath.Join(base, leftover, "a.sh"), "a", 0o644)

	got, err := workflow.RemoveStagedInjects(base, "lab", workflow.RemoveOptions{})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("RemoveStagedInjects() error = %v, want errors.Is(err, fs.ErrPermission)", err)
	}

	want := unstageWant(t, base, workflow.Staged{Leftovers: []string{leftover}})
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("RemoveStagedInjects() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"lab"}, unstageEntries(t, base)); diff != "" {
		t.Errorf("entries of the base mismatch (-want +got):\n%s", diff)
	}
}

// RemoveStagedInjects removes name and its staging leftovers, and no other
// entry of the base.
func TestRemoveStagedInjectsProperties(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// No two names differ only in letter case, so the property also holds on
	// a file system that ignores letter case.
	entry := rapid.OneOf(
		rapid.SampledFrom([]string{"lab", "lab-a", "la", "lab.", "x"}),
		rapid.Custom(func(t *rapid.T) string {
			prefix := rapid.SampledFrom([]string{".lab", ".lab-a", ".la", "lab"}).Draw(t, "prefix")
			infix := rapid.SampledFrom([]string{".tmp-", ".old-", "-tmp-"}).Draw(t, "infix")
			suffix := rapid.OneOf(
				rapid.StringMatching(`[A-Z2-7]{26}`),
				rapid.StringMatching(`[A-Z2-7]{25}`),
				rapid.StringMatching(`[a-z]{26}`),
			).Draw(t, "suffix")

			return prefix + infix + suffix
		}),
	)

	checks := 0

	rapid.Check(t, func(t *rapid.T) {
		entries := rapid.SliceOfNDistinct(entry, 0, 6, strings.ToLower).Draw(t, "entries")
		keepDir := rapid.Bool().Draw(t, "keepDir")

		checks++

		base := filepath.Join(root, strconv.Itoa(checks))
		if err := os.Mkdir(base, 0o700); err != nil {
			t.Fatal(err)
		}

		defer func() { _ = os.RemoveAll(base) }()

		leftover := regexp.MustCompile(`^\.lab\.(tmp|old)-[A-Z2-7]{26}$`)
		want := []string{}

		for _, e := range entries {
			if err := os.Mkdir(filepath.Join(base, e), 0o700); err != nil {
				t.Fatal(err)
			}

			if !leftover.MatchString(e) && (e != "lab" || keepDir) {
				want = append(want, e)
			}
		}

		slices.Sort(want)

		if _, err := workflow.RemoveStagedInjects(base, "lab", workflow.RemoveOptions{KeepDir: keepDir}); err != nil {
			t.Fatalf("RemoveStagedInjects() error = %v", err)
		}

		if diff := cmp.Diff(want, unstageEntriesRapid(t, base), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("entries after RemoveStagedInjects mismatch (-want +got):\n%s", diff)
		}
	})
}

// unstageEntriesRapid returns the names of the entries in dir, sorted.
func unstageEntriesRapid(t *rapid.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}
