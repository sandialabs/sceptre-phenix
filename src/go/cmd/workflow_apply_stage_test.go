package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phenix/api/workflow"
)

// guardApplied is the topology directory, with phenix-injects, that a row
// of the staging guard's tests applies, under the row's root.
const guardApplied = "work/foo"

// guardRow is a layout that the guard of the staging destination refuses.
// files and links are written under the row's root, each link pointing to
// its target under that root. dir is the topology directory to apply when
// it is not guardApplied, base the injects directory the server reports,
// and topologies --base-dir.topologies, all under the root. With relative,
// the command runs from the topology directory, and base and topologies are
// given as written. branch is -b, and want the requests the server
// receives. In wantErr, "<root>" is the root as written and "<resolved
// root>" the root with its symbolic links resolved.
type guardRow struct {
	name       string
	files      map[string]string
	links      map[string]string
	dir        string
	base       string
	relative   bool
	topologies string
	branch     string
	want       []string
	wantErr    string
}

// runGuardRows runs each row once as a real run and once with -n, which
// refuses at the same point with the same error. Each run sends only the
// row's requests, changes nothing under the row's root, and logs neither
// that it stages injects nor that a dry run completed.
func runGuardRows(t *testing.T, rows []guardRow) {
	t.Helper()

	for _, tt := range rows {
		for _, dryRun := range []bool{false, true} {
			name := tt.name
			if dryRun {
				name += ", with -n"
			}

			t.Run(name, func(t *testing.T) {
				logs := captureLogs(t)
				root := t.TempDir()
				t.Setenv("GIT_CEILING_DIRECTORIES", root)
				writeWorkflowTree(t, root, tt.files)

				for link, target := range tt.links {
					if err := os.Symlink(filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, link)); err != nil {
						t.Fatalf("linking %s: %v", link, err)
					}
				}

				dir := filepath.Join(root, filepath.FromSlash(guardApplied))
				if tt.dir != "" {
					dir = filepath.Join(root, filepath.FromSlash(tt.dir))
				}

				// A relative base names a directory from the topology
				// directory, which the server reports too.
				base := filepath.Join(root, filepath.FromSlash(tt.base))
				if tt.relative {
					base = filepath.Join(dir, filepath.FromSlash(tt.base))
				}

				fake := newFakePhenix(t, fakePhenix{
					options: serverOptions(base),
					plan:    workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
				})

				opts := testApplyOptions(fake.socket, t.TempDir())
				opts.Name = tt.branch
				opts.DryRun = dryRun

				if tt.topologies != "" {
					opts.TopologiesBase = filepath.Join(root, filepath.FromSlash(tt.topologies))
				}

				if tt.relative {
					t.Chdir(dir)

					opts.InjectsBase = tt.base
					opts.InjectsBaseExplicit = true
					opts.TopologiesBase = tt.topologies
				}

				resolvedRoot, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatalf("resolving %s: %v", root, err)
				}

				before := treeSnapshot(t, root)

				err = runWorkflowApply(t.Context(), opts, dir)

				want := strings.ReplaceAll(strings.ReplaceAll(tt.wantErr, "<resolved root>", resolvedRoot), "<root>", root)
				if err == nil || err.Error() != want {
					t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
				}

				checkRequests(t, fake, tt.want...)

				if after := treeSnapshot(t, root); !reflect.DeepEqual(after, before) {
					t.Errorf("the run changed the disk:\nbefore %v\nafter  %v", before, after)
				}

				checkNotLogged(t, logs, "staging injects", "dry run complete; nothing was changed")
			})
		}
	}
}

// TestRunWorkflowApplyGuardsTheStagingDestination stages with a wrong
// --base-dir.injects or -b that would replace a topology directory, a part
// of the topology directory being applied, or a file.
func TestRunWorkflowApplyGuardsTheStagingDestination(t *testing.T) {
	const applied = guardApplied

	injectsOnly := map[string]string{applied + "/phenix-injects/a.txt": "new"}

	runGuardRows(t, []guardRow{
		{
			name:  "the destination holds a topology directory's entry",
			files: mergedFiles(injectsOnly, map[string]string{"injects/foo/.phenix.yml": "old"}),
			base:  "injects",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/injects/foo: it holds .phenix.yml, which marks a topology directory; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "a directory in the destination holds a topology directory's entry",
			files: mergedFiles(injectsOnly, map[string]string{"injects/foo/bar/phenix-configs/topology.yml": "old"}),
			base:  "injects",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/injects/foo: its directory bar holds phenix-configs, which marks a topology directory; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "the base is a topology directory",
			files: mergedFiles(injectsOnly, map[string]string{"topologies/bar/phenix.yml": wfWorkflowYAML}),
			base:  "topologies/bar",
			want:  []string{reqOptions},
			wantErr: "refusing to stage into <root>/topologies/bar: it holds phenix.yml, which marks a topology directory; " +
				"check --base-dir.injects",
		},
		{
			name:  "the base is the topology directory being applied",
			files: underDir(applied, fullTopology()),
			base:  applied,
			want:  []string{reqOptions},
			wantErr: "refusing to stage into <root>/work/foo: it holds phenix.yml, which marks a topology directory; " +
				"check --base-dir.injects",
		},
		{
			name:  "the destination lies inside the topology directory",
			files: mergedFiles(injectsOnly, map[string]string{applied + "/docs/README.md": "notes"}),
			base:  applied + "/docs",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/work/foo/docs/foo: it lies inside the topology directory <root>/work/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "the destination is the topology directory",
			files: injectsOnly,
			base:  "work",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/work/foo: it lies inside the topology directory <root>/work/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "a missing base inside the topology directory",
			files: injectsOnly,
			base:  applied + "/new/injects",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/work/foo/new/injects/foo: it lies inside the topology directory <root>/work/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "a base that is a symbolic link into the topology directory",
			files: mergedFiles(injectsOnly, map[string]string{applied + "/docs/README.md": "notes"}),
			links: map[string]string{"link": applied + "/docs"},
			base:  "link",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/link/foo: it lies inside the topology directory <root>/work/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:  "a topology directory given through a symbolic link",
			files: mergedFiles(injectsOnly, map[string]string{applied + "/docs/README.md": "notes"}),
			links: map[string]string{"worklink": "work"},
			dir:   "worklink/foo",
			base:  applied + "/docs",
			want:  []string{reqOptions},
			wantErr: "refusing to replace <root>/work/foo/docs/foo: it lies inside the topology directory <root>/worklink/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:     "a relative --base-dir.injects inside the topology directory",
			files:    mergedFiles(injectsOnly, map[string]string{applied + "/docs/README.md": "notes"}),
			base:     "docs",
			relative: true,
			want:     []string{reqOptions},
			wantErr: "refusing to replace docs/foo: it lies inside the topology directory <root>/work/foo; " +
				"check --base-dir.injects and -b",
		},
		{
			name:    "the destination is a regular file",
			files:   mergedFiles(injectsOnly, map[string]string{"injects/foo": "a file"}),
			base:    "injects",
			want:    []string{reqOptions},
			wantErr: "refusing to replace <root>/injects/foo: it is not a directory; check --base-dir.injects and -b",
		},
	})
}

// TestRunWorkflowApplyRefusesABaseInsideATopologyDirectory stages into a
// base inside another topology directory, below a level that holds no entry
// that marks one, so that staging would replace a part of that directory.
func TestRunWorkflowApplyRefusesABaseInsideATopologyDirectory(t *testing.T) {
	files := mergedFiles(
		map[string]string{guardApplied + "/phenix-injects/a.txt": "new"},
		underDir("topologies/bar", fullTopology()),
	)

	runGuardRows(t, []guardRow{
		{
			name:  "a base two levels inside another topology directory",
			files: files,
			base:  "topologies/bar/phenix-injects/scripts",
			want:  []string{reqOptions},
			wantErr: "refusing to stage into <root>/topologies/bar/phenix-injects/scripts: " +
				"it lies inside <resolved root>/topologies/bar, which holds phenix.yml, a name that marks a topology directory; " +
				"check --base-dir.injects",
		},
		{
			name:  "a missing base inside another topology directory",
			files: files,
			base:  "topologies/bar/new/injects",
			want:  []string{reqOptions},
			wantErr: "refusing to stage into <root>/topologies/bar/new/injects: it lies inside <resolved root>/topologies/bar, " +
				"which holds phenix.yml, a name that marks a topology directory; check --base-dir.injects",
		},
		{
			name:   "a base that is a symbolic link into another topology directory, with -b scripts",
			files:  files,
			links:  map[string]string{"link": "topologies/bar/phenix-injects"},
			base:   "link",
			branch: "scripts",
			want:   []string{reqOptions},
			wantErr: "refusing to stage into <root>/link: it lies inside <resolved root>/topologies/bar, " +
				"which holds phenix.yml, a name that marks a topology directory; check --base-dir.injects",
		},
	})
}

// TestRunWorkflowApplyRefusesToReplaceTheTopologiesDirectory stages into a
// destination that is the topologies directory or holds it, as with a base
// of / and the name of the directory that holds everything.
func TestRunWorkflowApplyRefusesToReplaceTheTopologiesDirectory(t *testing.T) {
	injectsOnly := map[string]string{guardApplied + "/phenix-injects/a.txt": "new"}

	runGuardRows(t, []guardRow{
		{
			name: "the directory that holds the topologies directory, with -b grand",
			files: mergedFiles(
				injectsOnly,
				underDir("grand/topologies/bar", fullTopology()),
				map[string]string{"grand/notes": "notes"},
			),
			base:       "",
			topologies: "grand/topologies",
			branch:     "grand",
			want:       []string{reqOptions},
			wantErr: "refusing to replace <root>/grand: it is or holds the topologies directory <root>/grand/topologies; " +
				"check --base-dir.injects and -b",
		},
		{
			name:       "a topologies directory given through a symbolic link, with -b grand",
			files:      mergedFiles(injectsOnly, underDir("grand/topologies/bar", fullTopology())),
			links:      map[string]string{"toplink": "grand/topologies"},
			base:       "",
			topologies: "toplink",
			branch:     "grand",
			want:       []string{reqOptions},
			wantErr: "refusing to replace <root>/grand: it is or holds the topologies directory <root>/toplink; " +
				"check --base-dir.injects and -b",
		},
		{
			name:       "a relative --base-dir.injects and --base-dir.topologies, with -b grand",
			files:      mergedFiles(injectsOnly, underDir("work/grand/topologies/bar", fullTopology())),
			base:       "..",
			relative:   true,
			topologies: "../grand/topologies",
			branch:     "grand",
			want:       []string{reqOptions},
			wantErr: "refusing to replace ../grand: it is or holds the topologies directory ../grand/topologies; " +
				"check --base-dir.injects and -b",
		},
	})
}

// TestRunWorkflowApplyStagesBesideTopologyDirectories stages into a base
// that lies next to a topology directory or the topologies directory, not
// inside one, and into a destination inside which a topologies directory is
// configured but does not exist. The topology directory being applied is
// work/foo under the row's root, and the base is injects there.
func TestRunWorkflowApplyStagesBesideTopologyDirectories(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		topologies string
	}{
		{
			name:       "a topologies directory next to the base",
			files:      underDir("topologies/bar", fullTopology()),
			topologies: "topologies",
		},
		{
			name:  "a topology directory next to the base",
			files: map[string]string{"bar/phenix.yml": wfWorkflowYAML},
		},
		{
			name:       "a missing topologies directory inside the destination",
			files:      map[string]string{"injects/foo/a.txt": "old"},
			topologies: "injects/foo/topologies",
		},
	}

	// outsideInjects drops the injects directory from a snapshot of a root.
	outsideInjects := func(snapshot map[string]string) map[string]string {
		maps.DeleteFunc(snapshot, func(rel, _ string) bool {
			return rel == "injects" || strings.HasPrefix(rel, "injects"+string(filepath.Separator))
		})

		return snapshot
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", root)
			writeWorkflowTree(t, root, tt.files)
			writeWorkflowTree(t, root, map[string]string{"work/foo/phenix-injects/a.txt": "new"})

			fake := newFakePhenix(t, fakePhenix{options: serverOptions(filepath.Join(root, "injects"))})

			opts := testApplyOptions(fake.socket, t.TempDir())
			if tt.topologies != "" {
				opts.TopologiesBase = filepath.Join(root, filepath.FromSlash(tt.topologies))
			}

			before := outsideInjects(treeSnapshot(t, root))

			if err := runWorkflowApply(t.Context(), opts, filepath.Join(root, "work", "foo")); err != nil {
				t.Fatalf("runWorkflowApply() error = %v", err)
			}

			checkRequests(t, fake, reqOptions)

			staged := filepath.Join(root, "injects", "foo")
			if got, err := os.ReadFile(filepath.Join(staged, "a.txt")); err != nil || string(got) != "new" {
				t.Errorf("staged a.txt = %q (err %v), want %q", got, err, "new")
			}

			if entries, _ := os.ReadDir(staged); len(entries) != 1 {
				t.Errorf("%s holds %d entries, want only a.txt", staged, len(entries))
			}

			if after := outsideInjects(treeSnapshot(t, root)); !reflect.DeepEqual(after, before) {
				t.Errorf("the run changed the disk outside injects:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// TestRunWorkflowApplyGuardsTheDestinationAgainBeforeStaging stages into a
// destination that gains a workflow config while the dry runs are under way,
// after the preflight found nothing wrong with it.
func TestRunWorkflowApplyGuardsTheDestinationAgainBeforeStaging(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()
	dest := filepath.Join(injects, "foo")

	actOnLog(t, "workflow plan", func() { writeWorkflowTree(t, dest, map[string]string{"phenix.yml": "old"}) })

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

	want := "refusing to replace " + dest + ": it holds phenix.yml, which marks a topology directory; " +
		"check --base-dir.injects and -b"
	if err == nil || err.Error() != want {
		t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
	}

	checkRequests(t, fake, fullPreflight("foo")...)

	if entries, _ := os.ReadDir(dest); len(entries) != 1 {
		t.Errorf("%s holds %d entries, want only phenix.yml", dest, len(entries))
	}

	if got, readErr := os.ReadFile(filepath.Join(dest, "phenix.yml")); readErr != nil || string(got) != "old" {
		t.Errorf("phenix.yml = %q (err %v), want it unchanged", got, readErr)
	}

	checkNotLogged(t, logs, "staging injects")
}

// TestRunWorkflowApplyReplacesALinkAtTheDestination stages into a
// destination that is a symbolic link to the topology directory being
// applied or to another one. The preflight does not look through the link,
// since staging replaces the link itself, and the target is unchanged.
func TestRunWorkflowApplyReplacesALinkAtTheDestination(t *testing.T) {
	for _, target := range []string{"work/foo", "topologies/bar"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", root)
			writeWorkflowTree(t, root, underDir("topologies/bar", fullTopology()))
			writeWorkflowTree(t, root, map[string]string{"work/foo/phenix-injects/a.txt": "new", "injects/README": "base"})

			dest := filepath.Join(root, "injects", "foo")
			if err := os.Symlink(filepath.Join(root, filepath.FromSlash(target)), dest); err != nil {
				t.Fatalf("linking %s: %v", dest, err)
			}

			fake := newFakePhenix(t, fakePhenix{options: serverOptions(filepath.Join(root, "injects"))})
			work, topologies := treeSnapshot(t, filepath.Join(root, "work")), treeSnapshot(t, filepath.Join(root, "topologies"))

			dir := filepath.Join(root, "work", "foo")

			if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
				t.Fatalf("runWorkflowApply() error = %v", err)
			}

			checkRequests(t, fake, reqOptions)

			if info, err := os.Lstat(dest); err != nil || !info.IsDir() {
				t.Fatalf("%s is not a directory after the run (err %v)", dest, err)
			}

			if got, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(got) != "new" {
				t.Errorf("staged a.txt = %q (err %v), want %q", got, err, "new")
			}

			if entries, _ := os.ReadDir(filepath.Join(root, "injects")); len(entries) != 2 {
				t.Errorf("the injects directory holds %d entries, want README and foo", len(entries))
			}

			if after := treeSnapshot(t, filepath.Join(root, "work")); !reflect.DeepEqual(after, work) {
				t.Errorf("the run changed work:\nbefore %v\nafter  %v", work, after)
			}

			if after := treeSnapshot(t, filepath.Join(root, "topologies")); !reflect.DeepEqual(after, topologies) {
				t.Errorf("the run changed topologies:\nbefore %v\nafter  %v", topologies, after)
			}
		})
	}
}

// TestRunWorkflowApplyStagesMarkerNamesDeeper runs a topology directory
// twice whose phenix-injects holds marker names deeper than the preflight
// looks: two levels down, or behind a symbolic link, which staging copies as
// a link. The second run replaces what the first staged.
func TestRunWorkflowApplyStagesMarkerNamesDeeper(t *testing.T) {
	tests := []struct {
		name  string
		links map[string]string
	}{
		{name: "a marker name two levels down"},
		{name: "a directory reached through a symbolic link", links: map[string]string{"phenix-injects/lnk": "sub/deeper"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", map[string]string{"phenix-injects/sub/deeper/phenix.yml": "staged"})

			for link, target := range tt.links {
				if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
					t.Fatalf("linking %s: %v", link, err)
				}
			}

			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

			for _, content := range []string{"one", "two"} {
				writeWorkflowTree(t, dir, map[string]string{"phenix-injects/a.txt": content})

				if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
					t.Fatalf("run staging %q: runWorkflowApply() error = %v", content, err)
				}

				if got, err := os.ReadFile(filepath.Join(injects, "foo", "a.txt")); err != nil || string(got) != content {
					t.Fatalf("staged a.txt = %q (err %v), want %q", got, err, content)
				}

				marker := filepath.Join(injects, "foo", "sub", "deeper", "phenix.yml")
				if got, err := os.ReadFile(marker); err != nil || string(got) != "staged" {
					t.Fatalf("staged sub/deeper/phenix.yml = %q (err %v), want %q", got, err, "staged")
				}
			}

			checkRequests(t, fake, reqOptions, reqOptions)
		})
	}
}

// TestRunWorkflowApplyStageErrors checks how a failed staging is reported:
// the hint names the path at fault, and the working directory is protected.
func TestRunWorkflowApplyStageErrors(t *testing.T) {
	t.Run("permission denied", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}

		injects := t.TempDir()

		if err := os.Chmod(injects, 0o500); err != nil {
			t.Fatalf("making %s read-only: %v", injects, err)
		}

		t.Cleanup(func() { _ = os.Chmod(injects, 0o700) })

		dir := newTopologyDir(t, "foo", map[string]string{"phenix-injects/a.txt": "a"})
		fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

		err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
		if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "run phenix as a user who can write to "+injects) {
			t.Fatalf("runWorkflowApply() error = %v, want a permission error with a hint", err)
		}

		checkRequests(t, fake, reqOptions)
	})

	t.Run("unreadable source file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores file permissions")
		}

		injects := t.TempDir()
		dir := newTopologyDir(t, "foo", map[string]string{"phenix-injects/a.txt": "a", "phenix-injects/secret.key": "key"})
		secret := filepath.Join(dir, injectsDirName, "secret.key")

		if err := os.Chmod(secret, 0o000); err != nil {
			t.Fatalf("making %s unreadable: %v", secret, err)
		}

		fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

		err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
		want := "; make " + secret + " readable by the user running phenix"

		if !errors.Is(err, fs.ErrPermission) || !strings.HasSuffix(err.Error(), want) || strings.Contains(err.Error(), "can write to") {
			t.Fatalf("runWorkflowApply() error = %v, want a permission error ending in %q", err, want)
		}

		checkRequests(t, fake, reqOptions)
		assertEmptyDir(t, injects)
	})

	t.Run("working directory inside the destination", func(t *testing.T) {
		dir := newTopologyDir(t, "foo", map[string]string{"phenix-injects/a.txt": "a"})
		injects := t.TempDir()
		dest := filepath.Join(injects, "foo")
		writeWorkflowTree(t, dest, map[string]string{"work/a.txt": "old"})
		t.Chdir(filepath.Join(dest, "work"))
		fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

		err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
		want := "staging injects into " + dest
		if !errors.Is(err, workflow.ErrUnsafeDestination) || !strings.Contains(err.Error(), want) {
			t.Fatalf("runWorkflowApply() error = %v, want workflow.ErrUnsafeDestination", err)
		}

		if got, readErr := os.ReadFile(filepath.Join(dest, "work", "a.txt")); readErr != nil || string(got) != "old" {
			t.Errorf("work/a.txt = %q (err %v), want it unchanged", got, readErr)
		}
	})
}

// TestRunWorkflowApplyReportsLeftovers stages next to hidden siblings that
// earlier runs of the same name left behind: each is reported after the
// staging, and none is removed.
func TestRunWorkflowApplyReportsLeftovers(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()
	leftovers := []string{".foo.old-ABCDEFGHIJKLMNOPQRSTUVWXYZ", ".foo.tmp-234567ABCDEFGHIJKLMNOPQRST"}

	writeWorkflowTree(t, injects, map[string]string{leftovers[0] + "/a.txt": "old", leftovers[1] + "/a.txt": "tmp"})

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake, fullRun("foo", workflow.ActionCreateAndStart)...)
	checkFields(t, logs.first(t, "staging injects"), map[string]string{
		"level": "INFO", "type": "SYSTEM", "step": "injects", "dir": filepath.Join(injects, "foo"),
	})

	// The paths have every symlink in the injects directory resolved.
	resolved, err := filepath.EvalSymlinks(injects)
	if err != nil {
		t.Fatal(err)
	}

	warned := logs.all(t, "leftover of an earlier run next to the staged injects; remove it by hand if no other run is active")
	if len(warned) != len(leftovers) {
		t.Fatalf("got %d leftover records, want %d", len(warned), len(leftovers))
	}

	for i, name := range leftovers {
		checkFields(t, warned[i], map[string]string{
			"level": "WARN", "type": "SYSTEM", "step": "injects", "path": filepath.Join(resolved, name),
		})

		if _, statErr := os.Stat(filepath.Join(injects, name, "a.txt")); statErr != nil {
			t.Errorf("%s was not kept: %v", name, statErr)
		}
	}
}

// TestRunWorkflowApplyCannotListLeftovers stages into an injects directory
// that the user may write to but not list: the staging works, with a
// warning that the run could not look for leftovers.
func TestRunWorkflowApplyCannotListLeftovers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()

	if err := os.Chmod(injects, 0o300); err != nil {
		t.Fatalf("making %s unlistable: %v", injects, err)
	}

	t.Cleanup(func() { _ = os.Chmod(injects, 0o700) })

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake, fullRun("foo", workflow.ActionCreateAndStart)...)

	record := logs.first(t, "could not look for leftovers of earlier runs next to the staged injects")
	checkFields(t, record, map[string]string{"level": "WARN", "type": "SYSTEM", "step": "injects", "dir": injects})

	if msg := fmt.Sprint(record["err"]); !strings.Contains(msg, "permission denied") {
		t.Errorf("err field = %q, want a permission error", msg)
	}
}

// TestRunWorkflowApplyPreviousCopyKept stages over a previous copy that
// cannot be removed once the new tree is in place: the run warns without a
// permission hint and goes on, and the copy it kept is not reported again as
// a leftover of an earlier run, while a real leftover next to it is.
func TestRunWorkflowApplyPreviousCopyKept(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()
	locked := filepath.Join(injects, "foo", "locked")
	writeWorkflowTree(t, locked, map[string]string{"old.txt": "old"})

	earlier := ".foo.tmp-ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	writeWorkflowTree(t, injects, map[string]string{earlier + "/a.txt": "tmp"})

	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("locking %s: %v", locked, err)
	}

	// The locked directory moves to .foo.old-*, so restore access there too.
	t.Cleanup(func() {
		matches, _ := filepath.Glob(filepath.Join(injects, ".foo.old-*", "locked"))
		for _, path := range append(matches, locked) {
			_ = os.Chmod(path, 0o750)
		}
	})

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake, fullRun("foo", workflow.ActionCreateAndStart)...)

	staged, err := os.ReadFile(filepath.Join(injects, "foo", "scripts", "hello.sh"))
	if err != nil || string(staged) != "echo hello\n" {
		t.Errorf("staged inject = %q, %v; want %q", staged, err, "echo hello\n")
	}

	kept := logs.first(t, "injects staged, but the previous copy could not be removed; remove it by hand")
	checkFields(t, kept, map[string]string{"level": "WARN", "type": "SYSTEM", "step": "injects", "dir": filepath.Join(injects, "foo")})

	keptErr := fmt.Sprint(kept["err"])
	for _, part := range []string{"could not remove the previous injects copy", "permission denied"} {
		if !strings.Contains(keptErr, part) {
			t.Errorf("the warning's err = %q, want it to contain %q", keptErr, part)
		}
	}

	if strings.Contains(keptErr, "run phenix as a user who can write to") {
		t.Errorf("the warning's err = %q, want no permission hint", keptErr)
	}

	checkFields(t, logs.first(t, "injects staged"), map[string]string{
		"level": "INFO", "step": "injects", "dir": filepath.Join(injects, "foo"), "files": "1",
	})
	checkFields(t, logs.first(t, "workflow apply complete"), map[string]string{"level": "INFO", "step": "apply", "name": "foo"})

	// The leftover records name paths with every symlink in the injects
	// directory resolved, as the warning's err does.
	resolved, err := filepath.EvalSymlinks(injects)
	if err != nil {
		t.Fatal(err)
	}

	keptCopies, err := filepath.Glob(filepath.Join(resolved, ".foo.old-*"))
	if err != nil || len(keptCopies) != 1 || !strings.Contains(keptErr, keptCopies[0]) {
		t.Fatalf("kept copies = %q (err %v), want the one the warning's err names: %q", keptCopies, err, keptErr)
	}

	leftovers := logs.all(t, "leftover of an earlier run next to the staged injects; remove it by hand if no other run is active")
	if len(leftovers) != 1 {
		t.Fatalf("got %d leftover records, want 1: %v", len(leftovers), leftovers)
	}

	checkFields(t, leftovers[0], map[string]string{
		"level": "WARN", "type": "SYSTEM", "step": "injects", "path": filepath.Join(resolved, earlier),
	})
}
