package experiment

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
)

// baseDirWorld is a folder tree for the base directory tests, nine folders
// below a temporary folder, so that eight ".." parts stay inside it.
type baseDirWorld struct {
	top  string // the world folder
	base string // the phenix base, <top>/phenix
	root string // the experiments folder, <base>/experiments
}

// newBaseDirWorld makes a baseDirWorld:
//
//	<root>/exp, <root>/file (a file), <top>/outside
//	<root>/out -> <top>/outside, <root>/rel-out -> ../../outside
//	<root>/dangling -> <top>/outside/nothere, <root>/loop -> loop
//	<top>/outside/in -> <root>, <top>/rootlink -> <root>
func newBaseDirWorld(tb testing.TB) baseDirWorld {
	tb.Helper()

	top := filepath.Join(tb.TempDir(), "1", "2", "3", "4", "5", "6", "7", "8", "w")
	w := baseDirWorld{top: top, base: filepath.Join(top, "phenix"), root: filepath.Join(top, "phenix", "experiments")}

	for _, dir := range []string{filepath.Join(w.root, "exp"), filepath.Join(top, "outside")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			tb.Fatalf("os.MkdirAll(%s) error = %v", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(w.root, "file"), nil, 0o600); err != nil {
		tb.Fatalf("os.WriteFile(file) error = %v", err)
	}

	links := map[string]string{
		filepath.Join(w.root, "out"):        filepath.Join(top, "outside"),
		filepath.Join(w.root, "rel-out"):    filepath.Join("..", "..", "outside"),
		filepath.Join(w.root, "dangling"):   filepath.Join(top, "outside", "nothere"),
		filepath.Join(w.root, "loop"):       "loop",
		filepath.Join(top, "outside", "in"): w.root,
		filepath.Join(top, "rootlink"):      w.root,
	}

	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			tb.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
		}
	}

	return w
}

// usePhenixBase sets the phenix base until the test ends.
func usePhenixBase(t *testing.T, base string) {
	t.Helper()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // experiment files go to a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore
}

func TestCheckBaseDir(t *testing.T) {
	t.Parallel()

	w := newBaseDirWorld(t)

	tests := []struct {
		name, dir string
		root      string // w.root when empty
		wantOK    bool
	}{
		{name: "an existing folder inside", dir: w.root + "/exp", wantOK: true},
		{name: "a new folder inside", dir: w.root + "/new", wantOK: true},
		{name: "a new folder two levels inside", dir: w.root + "/new/sub", wantOK: true},
		{name: "a folder whose name starts with two dots", dir: w.root + "/..x", wantOK: true},
		{name: "a dot-dot that stays inside", dir: w.root + "/exp/../new", wantOK: true},
		{name: "a trailing slash", dir: w.root + "/exp/", wantOK: true},
		{name: "a symlink outside that points inside", dir: w.top + "/outside/in/x", wantOK: true},
		{name: "a symlink to the root", dir: w.top + "/rootlink/x", wantOK: true},
		{name: "a root that is a symlink", dir: w.root + "/x", root: w.top + "/rootlink", wantOK: true},
		{name: "a missing root", dir: w.top + "/none/x", root: w.top + "/none", wantOK: true},
		{name: "empty", dir: ""},
		{name: "a relative path", dir: "experiments/x"},
		{name: "a relative path to the root", dir: "."},
		{name: "the file system root", dir: "/"},
		{name: "the root itself", dir: w.root},
		{name: "the root with a slash", dir: w.root + "/"},
		{name: "the root with a dot", dir: w.root + "/."},
		{name: "a dot-dot back to the root", dir: w.root + "/exp/.."},
		{name: "the phenix base", dir: w.base},
		{name: "a dot-dot out of the root", dir: w.root + "/../outside"},
		{name: "a sibling with the root as prefix", dir: w.root + "-other/x"},
		{name: "a folder outside", dir: w.top + "/outside/x"},
		{name: "a symlink inside that points outside", dir: w.root + "/out"},
		{name: "a folder under a symlink that points outside", dir: w.root + "/out/x"},
		{name: "a relative symlink that points outside", dir: w.root + "/rel-out/x"},
		{name: "a dot-dot after a symlink", dir: w.root + "/out/../x"},
		{name: "a dot-dot after a symlink that points inside", dir: w.top + "/outside/in/.."},
		{name: "a dangling symlink that points outside", dir: w.root + "/dangling/x"},
		{name: "a symlink loop", dir: w.root + "/loop/x"},
		{name: "a folder under a file", dir: w.root + "/file/x"},
		{name: "a system folder", dir: "/etc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := tc.root
			if root == "" {
				root = w.root
			}

			err := checkBaseDir(tc.dir, root)
			if (err == nil) != tc.wantOK {
				t.Errorf("checkBaseDir(%q, %q) error = %v, want ok = %t", tc.dir, root, err, tc.wantOK)
			}

			if err != nil && !errors.Is(err, ErrBaseDirOutside) {
				t.Errorf("checkBaseDir(%q, %q) error = %v, want one that wraps ErrBaseDirOutside", tc.dir, root, err)
			}
		})
	}
}

// madeBaseDir makes the folder dir, as an experiment start does, and returns
// the resolved path of the folder made, or false when dir cannot be made.
func madeBaseDir(dir string) (string, bool) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", false
	}

	made, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}

	return made, true
}

// checkBaseDirAgainstMkdir checks checkBaseDir(dir, root) against the folder
// that MkdirAll makes for dir: checkBaseDir must accept dir exactly when that
// folder is strictly inside root. It skips a dir that cannot be made. dir must
// have at most eight ".." parts, so that MkdirAll stays inside the world.
func checkBaseDirAgainstMkdir(dir, root string) (string, bool) {
	err := checkBaseDir(dir, root)

	made, ok := madeBaseDir(dir)
	if !ok {
		return "", true
	}

	realRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		return "EvalSymlinks(root) error = " + rootErr.Error(), false
	}

	if inside := strictlyInside(made, realRoot); inside != (err == nil) {
		return "checkBaseDir(" + dir + ") error = " + errText(err) + ", but mkdir makes " + made, false
	}

	return "", true
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}

	return err.Error()
}

// basedirSegments are the path parts genBaseDirTestPath draws.
var basedirSegments = []string{ //nolint:gochecknoglobals // test table
	"exp", "new", "out", "rel-out", "dangling", "loop", "file", "..", ".", "",
	"phenix", "experiments", "outside", "in", "rootlink", "..x", "nothere",
}

// genBaseDirTestPath draws a path of up to eight parts below the world folder.
func genBaseDirTestPath(t *rapid.T, w baseDirWorld) string {
	parts := rapid.SliceOfN(rapid.SampledFrom(basedirSegments), 0, 8).Draw(t, "parts")

	return w.top + "/" + strings.Join(parts, "/")
}

// checkBaseDir accepts a base directory exactly when the folder that mkdir
// makes for it is inside the experiments folder, whatever symlinks and ".."
// parts it holds.
func TestCheckBaseDirMatchesMkdirProperties(t *testing.T) {
	t.Parallel()

	w := newBaseDirWorld(t)

	rapid.Check(t, func(t *rapid.T) {
		dir := genBaseDirTestPath(t, w)
		root := rapid.SampledFrom([]string{w.root, w.top + "/rootlink"}).Draw(t, "root")

		if msg, ok := checkBaseDirAgainstMkdir(dir, root); !ok {
			t.Fatal(msg)
		}
	})
}

func TestProtectedDir(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	w := newBaseDirWorld(t)
	usePhenixBase(t, w.base)

	tests := []struct {
		dir  string
		want bool
	}{
		{dir: "", want: true},
		{dir: "relative", want: true},
		{dir: "/", want: true},
		{dir: w.top, want: true},
		{dir: w.base, want: true},
		{dir: w.base + "/", want: true},
		{dir: w.root, want: true},
		{dir: w.root + "/.", want: true},
		{dir: w.root + "/exp/..", want: true},
		{dir: w.top + "/rootlink", want: true},
		{dir: w.top + "/outside/in", want: true},
		{dir: w.root + "/exp", want: false},
		{dir: w.top + "/outside", want: false},
		{dir: w.base + "/images", want: false},
	}

	for _, tc := range tests {
		if got := protectedDir(tc.dir); got != tc.want {
			t.Errorf("protectedDir(%q) = %t, want %t", tc.dir, got, tc.want)
		}
	}
}

func TestRemoveBaseDir(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	tests := []struct {
		name     string
		dir      func(w baseDirWorld) string
		api      bool
		wantGone bool
	}{
		{name: "CLI, inside", dir: func(w baseDirWorld) string { return w.root + "/exp" }, wantGone: true},
		{name: "CLI, outside", dir: func(w baseDirWorld) string { return w.top + "/outside" }, wantGone: true},
		{name: "CLI, the experiments folder", dir: func(w baseDirWorld) string { return w.root }},
		{name: "CLI, the phenix base", dir: func(w baseDirWorld) string { return w.base }},
		{name: "API, inside", dir: func(w baseDirWorld) string { return w.root + "/exp" }, api: true, wantGone: true},
		{name: "API, outside", dir: func(w baseDirWorld) string { return w.top + "/outside" }, api: true},
		{name: "API, the experiments folder", dir: func(w baseDirWorld) string { return w.root }, api: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newBaseDirWorld(t)
			usePhenixBase(t, w.base)

			root := ""
			if tc.api {
				root = BaseDirRoot()
			}

			dir := tc.dir(w)
			if err := removeBaseDir("exp", dir, root); err != nil {
				t.Fatalf("removeBaseDir(%s, %q) error = %v", dir, root, err)
			}

			_, err := os.Lstat(dir)
			if gone := errors.Is(err, fs.ErrNotExist); gone != tc.wantGone {
				t.Errorf("removeBaseDir(%s, %q) removed the folder = %t, want %t", dir, root, gone, tc.wantGone)
			}
		})
	}
}

// The create and update hooks of a config with a FilesRoot, as the web API
// sends, refuse a base directory outside it. Without one, as from the CLI,
// they take any base directory.
func TestHooksCheckTheBaseDirOfAPIConfigs(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		baseDir func(w baseDirWorld) string
		api     bool
		wantErr bool
	}{
		{name: "API, the default", baseDir: func(baseDirWorld) string { return "" }, api: true},
		{name: "API, inside", baseDir: func(w baseDirWorld) string { return w.root + "/exp" }, api: true},
		{name: "API, outside", baseDir: func(w baseDirWorld) string { return w.top + "/outside" }, api: true, wantErr: true},
		{name: "API, relative", baseDir: func(baseDirWorld) string { return "exp" }, api: true, wantErr: true},
		{
			name:    "API, a symlink out",
			baseDir: func(w baseDirWorld) string { return w.root + "/out/exp" }, api: true, wantErr: true,
		},
		{name: "API, the root", baseDir: func(w baseDirWorld) string { return w.root }, api: true, wantErr: true},
		{name: "CLI, outside", baseDir: func(w baseDirWorld) string { return w.top + "/outside" }},
		{name: "CLI, relative", baseDir: func(baseDirWorld) string { return "exp" }},
	}

	for _, stage := range []string{"create", "update"} {
		for _, tc := range tests {
			t.Run(stage+", "+tc.name, func(t *testing.T) {
				storetest.Use(t)

				w := newBaseDirWorld(t)
				usePhenixBase(t, w.base)

				c := hookConfig("exp", map[string]any{"experimentName": "exp", "baseDir": tc.baseDir(w)}, nil)
				if tc.api {
					c.FilesRoot = BaseDirRoot()
				}

				err := experimentConfigHook(t.Context(), stage, c)
				if (err != nil) != tc.wantErr {
					t.Errorf("experimentConfigHook(%s, baseDir %q) error = %v, want error = %t",
						stage, tc.baseDir(w), err, tc.wantErr)
				}

				if err != nil && !errors.Is(err, ErrBaseDirOutside) {
					t.Errorf("experimentConfigHook(%s) error = %v, want one that wraps ErrBaseDirOutside", stage, err)
				}
			})
		}
	}
}

// An API rename that would move the base directory out of the experiments
// folder is refused: the rename changes each part of the path that is the old
// name, here also the experiments folder itself.
func TestUpdateHookRefusesARenameOutOfTheRoot(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)

	w := newBaseDirWorld(t)
	usePhenixBase(t, w.base)

	c := hookConfig("new", map[string]any{"experimentName": "experiments", "baseDir": w.root + "/experiments"}, nil)
	c.FilesRoot = BaseDirRoot()

	if err := experimentConfigHook(t.Context(), "update", c); !errors.Is(err, ErrBaseDirOutside) {
		t.Errorf("experimentConfigHook(update, rename experiments to new) error = %v, want %v", err, ErrBaseDirOutside)
	}
}

// A delete through the API (DeleteWithBaseDirInside) deletes the experiment,
// but leaves a base directory outside the experiments folder. A delete from
// the CLI removes it.
func TestDeleteLeavesAnOutsideBaseDirOnlyForTheAPI(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name     string
		del      func(name, root string) error
		wantGone bool
	}{
		{name: "CLI", del: func(name, _ string) error { return Delete(name) }, wantGone: true},
		{
			name: "API",
			del:  func(name, root string) error { return Delete(name, DeleteWithBaseDirInside(root)) },
		},
		{
			name: "CLI config delete",
			del: func(name, _ string) error {
				return config.DeleteAtRevision("experiment/"+name, 0) //nolint:wrapcheck // test
			},
			wantGone: true,
		},
		{
			name: "API config delete",
			del: func(name, root string) error {
				return config.DeleteAtRevision("experiment/"+name, 0, config.DeleteWithFilesRoot(root)) //nolint:wrapcheck // test
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			useStartMM(t, &startMM{})
			useDeletedFiles(t)

			w := newBaseDirWorld(t)
			usePhenixBase(t, w.base)

			outside := w.top + "/outside/exp"
			if err := os.MkdirAll(outside, 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", outside, err)
			}

			storeValidExperiment(t, "exp", nil, map[string]any{"baseDir": outside})

			if err := tc.del("exp", BaseDirRoot()); err != nil {
				t.Fatalf("delete experiment exp error = %v", err)
			}

			c, _ := store.NewConfig("experiment/exp")
			if err := store.Get(c); !errors.Is(err, store.ErrNotExist) {
				t.Errorf("store.Get(experiment/exp) after the delete error = %v, want store.ErrNotExist", err)
			}

			_, err := os.Stat(outside)
			if gone := errors.Is(err, fs.ErrNotExist); gone != tc.wantGone {
				t.Errorf("delete removed the outside base directory = %t, want %t", gone, tc.wantGone)
			}
		})
	}
}

// No delete removes the phenix base or the experiments folder, also from the
// CLI.
func TestDeleteNeverRemovesThePhenixBase(t *testing.T) { //nolint:paralleltest // replaces package globals
	for _, which := range []string{"base", "root"} {
		t.Run(which, func(t *testing.T) {
			storetest.Use(t)
			useStartMM(t, &startMM{})
			useDeletedFiles(t)

			w := newBaseDirWorld(t)
			usePhenixBase(t, w.base)

			dir := map[string]string{"base": w.base, "root": w.root}[which]
			storeValidExperiment(t, "exp", nil, map[string]any{"baseDir": dir})

			if err := Delete("exp"); err != nil {
				t.Fatalf("Delete(exp) error = %v", err)
			}

			if _, err := os.Stat(w.root + "/exp"); err != nil {
				t.Errorf("os.Stat(%s/exp) after Delete(exp) error = %v, want the folder kept", w.root, err)
			}
		})
	}
}

// An API update may keep the stored base directory of an experiment, also one
// outside the experiments folder; only a new or changed base directory, also
// one that a rename rewrites, must be inside it.
func TestUpdateHookKeepsAStoredBaseDirOutsideTheRoot(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		stored  func(w baseDirWorld) string
		update  func(w baseDirWorld) string
		newName string // a rename to newName when not empty
		wantErr bool
	}{
		{
			name:   "unchanged outside",
			stored: func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update: func(w baseDirWorld) string { return w.top + "/outside/exp" },
		},
		{
			name:   "unchanged outside, written another way",
			stored: func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update: func(w baseDirWorld) string { return w.top + "/outside/./exp/" },
		},
		{
			name:   "unchanged outside, through a symlink",
			stored: func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update: func(w baseDirWorld) string { return w.root + "/out/exp" },
		},
		{
			name:   "left empty, so the default inside",
			stored: func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update: func(baseDirWorld) string { return "" },
		},
		{
			name:    "changed to another folder outside",
			stored:  func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update:  func(w baseDirWorld) string { return w.top + "/outside/other" },
			wantErr: true,
		},
		{
			name:    "changed from inside to outside",
			stored:  func(w baseDirWorld) string { return w.root + "/exp" },
			update:  func(w baseDirWorld) string { return w.top + "/outside/exp" },
			wantErr: true,
		},
		{
			name:   "changed to inside",
			stored: func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update: func(w baseDirWorld) string { return w.root + "/exp" },
		},
		{
			name:    "a rename that rewrites a base directory outside",
			stored:  func(w baseDirWorld) string { return w.top + "/outside/exp" },
			update:  func(w baseDirWorld) string { return w.top + "/outside/exp" },
			newName: "lab",
			wantErr: true,
		},
		{
			name:    "a rename that keeps a base directory outside",
			stored:  func(w baseDirWorld) string { return w.top + "/outside" },
			update:  func(w baseDirWorld) string { return w.top + "/outside" },
			newName: "lab",
		},
		{
			name:    "a rename inside",
			stored:  func(w baseDirWorld) string { return w.root + "/exp" },
			update:  func(w baseDirWorld) string { return w.root + "/exp" },
			newName: "lab",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			w := newBaseDirWorld(t)
			usePhenixBase(t, w.base)

			name := "exp"
			if tc.newName != "" {
				name = tc.newName
			}

			c := hookConfig(name, map[string]any{"experimentName": "exp", "baseDir": tc.update(w)}, map[string]any{})
			c.FilesRoot = BaseDirRoot()
			c.Stored = hookConfig("exp", map[string]any{"experimentName": "exp", "baseDir": tc.stored(w)}, map[string]any{})

			err := experimentConfigHook(t.Context(), "update", c)
			if (err != nil) != tc.wantErr {
				t.Errorf("experimentConfigHook(update, baseDir %q, stored %q) error = %v, want error = %t",
					tc.update(w), tc.stored(w), err, tc.wantErr)
			}

			if err != nil && !errors.Is(err, ErrBaseDirOutside) {
				t.Errorf("experimentConfigHook(update) error = %v, want one that wraps ErrBaseDirOutside", err)
			}
		})
	}
}
