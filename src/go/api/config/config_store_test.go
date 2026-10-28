package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/editor"
)

var registerFailingDeleteHook sync.Once //nolint:gochecknoglobals // hooks are global; register the test hook once

// errDeleteHook is the error of the "delete" hook for configs named hook-fails.
var errDeleteHook = errors.New("injected delete hook failure")

// useFailingDeleteHook registers a Topology hook that fails the "delete" stage
// for a config named hook-fails, and does nothing for other configs.
func useFailingDeleteHook() {
	registerFailingDeleteHook.Do(func() {
		config.RegisterConfigHook("Topology", func(stage string, c *store.Config) error {
			if stage == "delete" && c.Metadata.Name == "hook-fails" {
				return errDeleteHook
			}

			return nil
		})
	})
}

// storeConfig stores the YAML config doc in the test store, or fails the test.
func storeConfig(t *testing.T, doc string) *store.Config {
	t.Helper()

	c, err := config.Create(config.CreateFromYAML([]byte(doc)))
	if err != nil {
		t.Fatalf("config.Create(%q) error = %v", doc, err)
	}

	return c
}

// fullNames returns the kind/name of each config.
func fullNames(configs store.Configs) []string {
	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.FullName())
	}

	slices.Sort(names)

	return names
}

// useEditor makes EDITOR a shell script with the given body until the test ends.
func useEditor(t *testing.T, body string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("writing the test editor: %v", err)
	}

	t.Setenv("EDITOR", path)
}

// usePhenixBase makes common.PhenixBase a new temporary folder until the test
// ends, and returns its configs folder.
func usePhenixBase(t *testing.T) string {
	t.Helper()

	base := t.TempDir()

	previous := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // configs are read from a temporary tree

	t.Cleanup(func() { common.PhenixBase = previous }) //nolint:reassign // restore

	// Init sets BRANCH_NAME with os.Setenv; t.Setenv restores it afterwards
	t.Setenv("BRANCH_NAME", "")

	return filepath.Join(base, "configs")
}

// writeFiles writes each file under dir, with its parent folders.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		path := filepath.Join(dir, name)

		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating the folder of %s: %v", path, err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}

// Init stores the built-in configs and each valid config under
// <base>/configs, sets BRANCH_NAME to the folder of each file, keeps a stored
// config, and skips files that are not configs.
func TestInitSeedsConfigs(t *testing.T) {
	storetest.UseBolt(t)

	dir := usePhenixBase(t)

	writeFiles(t, dir, map[string]string{
		"foo/topology.yaml": "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${BRANCH_NAME}-topo\nspec:\n  nodes: []\n",
		"bar/scenario.json": `{"apiVersion": "phenix.sandia.gov/v2", "kind": "Scenario", "metadata": {"name": "from-json"}, "spec": {"apps": []}}`,
		"other.yml":         "just: data\n",
		"readme.txt":        "not a config\n",
		"bad/topology.yml":  "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: not valid\nspec:\n  nodes: []\n",
		"keep/topology.yml": "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: kept\n  annotations:\n    from: file\nspec:\n  nodes: []\n",
	})

	storeConfig(
		t,
		"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: kept\n  annotations:\n    from: store\nspec:\n  nodes: []\n",
	)

	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}

	for _, name := range []string{"topology/foo-topo", "scenario/from-json", "role/global-admin", "image/PHENIX-kali"} {
		if _, err := config.Get(name); err != nil {
			t.Errorf("config.Get(%s) after Init error = %v, want nil", name, err)
		}
	}

	if _, err := config.Get("topology/not valid"); err == nil {
		t.Error("config.Get(topology/not valid) after Init error = nil, want an error")
	}

	kept, err := config.Get("topology/kept")
	if err != nil {
		t.Fatalf("config.Get(topology/kept) error = %v", err)
	}

	if got := kept.Metadata.Annotations["from"]; got != "store" {
		t.Errorf("topology/kept annotation from = %q, want store", got)
	}

	if !store.IsInitialized(store.ComponentConfigs) {
		t.Error("store.IsInitialized(configs) after Init = false, want true")
	}

	before, err := config.List("all")
	if err != nil {
		t.Fatalf(`config.List("all") error = %v`, err)
	}

	// A second Init finds each config stored, and stores nothing more.
	if err := config.Init(); err != nil {
		t.Fatalf("second config.Init() error = %v", err)
	}

	after, err := config.List("all")
	if err != nil {
		t.Fatalf(`config.List("all") error = %v`, err)
	}

	if diff := cmp.Diff(fullNames(before), fullNames(after)); diff != "" {
		t.Errorf("configs after a second Init mismatch (-want +got):\n%s", diff)
	}
}

// Init returns an error for a YAML or JSON file under <base>/configs that it
// cannot parse.
func TestInitParseErrors(t *testing.T) {
	for name, body := range map[string]string{
		"bad/topology.yaml": "kind: [unclosed\n",
		"bad/topology.json": `{"kind": `,
	} {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)
			writeFiles(t, usePhenixBase(t), map[string]string{name: body})

			if err := config.Init(); err == nil {
				t.Errorf("config.Init() with %s = nil, want an error", name)
			}
		})
	}
}

// Init returns an error when it cannot read a file under <base>/configs.
func TestInitUnreadableFile(t *testing.T) {
	for _, name := range []string{"topology.yaml", "topology.json"} {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)

			dir := usePhenixBase(t)
			if err := os.MkdirAll(filepath.Join(dir, name), 0o750); err != nil {
				t.Fatalf("creating %s: %v", name, err)
			}

			// a folder with a config name passes the walk, but a symlink to it
			// is a file entry that ReadFile cannot read
			if err := os.Symlink(filepath.Join(dir, name), filepath.Join(dir, "link-"+name)); err != nil {
				t.Fatalf("creating the symlink: %v", err)
			}

			if err := config.Init(); err == nil {
				t.Errorf("config.Init() with an unreadable %s = nil, want an error", name)
			}
		})
	}
}

// List returns the configs of one kind, or of all kinds.
func TestListKinds(t *testing.T) {
	storetest.UseBolt(t)

	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: t1\nspec:\n  nodes: []\n")
	storeConfig(t, "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: s1\nspec:\n  apps: []\n")
	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Role\nmetadata:\n  name: r1\nspec:\n  roleName: R1\n")

	tests := map[string][]string{
		"":           {"Role/r1", "Scenario/s1", "Topology/t1"},
		"all":        {"Role/r1", "Scenario/s1", "Topology/t1"},
		"Topology":   {"Topology/t1"},
		"scenario":   {"Scenario/s1"},
		"experiment": {},
		"image":      {},
		"user":       {},
		"role":       {"Role/r1"},
	}

	for which, want := range tests {
		t.Run(which, func(t *testing.T) {
			got, err := config.List(which)
			if err != nil {
				t.Fatalf("config.List(%q) error = %v", which, err)
			}

			if diff := cmp.Diff(want, fullNames(got)); diff != "" {
				t.Errorf("config.List(%q) mismatch (-want +got):\n%s", which, diff)
			}
		})
	}
}

// Get returns a stored config, and an error for an empty, invalid or unknown
// name.
func TestGet(t *testing.T) {
	storetest.UseBolt(t)

	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: here\nspec:\n  nodes: []\n")

	got, err := config.Get("topology/here")
	if err != nil {
		t.Fatalf("config.Get(topology/here) error = %v", err)
	}

	if got.FullName() != "Topology/here" {
		t.Errorf("config.Get(topology/here) = %s, want Topology/here", got.FullName())
	}

	for _, name := range []string{"", "no-kind", "nokind/x", "topology/missing"} {
		if _, err := config.Get(name); err == nil {
			t.Errorf("config.Get(%q) error = nil, want an error", name)
		}
	}
}

// Create stores a config from a path, YAML or a config, and stops on a
// failed check, validation, name hook or store write.
func TestCreateSourcesAndErrors(t *testing.T) {
	storetest.UseBolt(t)

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"t.yaml":  "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: from-path\nspec:\n  nodes: []\n",
		"t.txt":   "kind: Topology\n",
		"bad.yml": "kind: [unclosed\n",
	})

	errCheck := errors.New("injected check failure")

	fromConfig, err := store.NewConfig("topology/from-config")
	if err != nil {
		t.Fatalf("store.NewConfig error = %v", err)
	}

	fromConfig.Spec = map[string]any{"nodes": []any{}}

	tests := []struct {
		name    string
		opts    []config.CreateOption
		wantErr bool
		is      error
	}{
		{
			name: "from path",
			opts: []config.CreateOption{config.CreateFromPath(filepath.Join(dir, "t.yaml")), config.CreateWithValidation()},
		},
		{name: "from config", opts: []config.CreateOption{config.CreateFromConfig(fromConfig)}},
		{name: "checked", opts: []config.CreateOption{
			config.CreateFromYAML(
				[]byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: checked\nspec:\n  nodes: []\n"),
			),
			config.CreateWithCheck(func(*store.Config) error { return nil }),
		}},
		{name: "no source", wantErr: true},
		{name: "missing path", opts: []config.CreateOption{config.CreateFromPath(filepath.Join(dir, "missing.yaml"))}, wantErr: true},
		{name: "wrong extension", opts: []config.CreateOption{config.CreateFromPath(filepath.Join(dir, "t.txt"))}, wantErr: true},
		{name: "bad YAML", opts: []config.CreateOption{config.CreateFromYAML([]byte("kind: [unclosed\n"))}, wantErr: true},
		{name: "check fails", opts: []config.CreateOption{
			config.CreateFromPath(filepath.Join(dir, "t.yaml")),
			config.CreateWithCheck(func(*store.Config) error { return errCheck }),
		}, wantErr: true, is: errCheck},
		{name: "invalid spec", opts: []config.CreateOption{
			config.CreateFromYAML(
				[]byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: invalid\nspec:\n  nodes: 7\n"),
			),
			config.CreateWithValidation(),
		}, wantErr: true, is: types.ErrValidationFailed},
		{name: "invalid name", opts: []config.CreateOption{
			config.CreateFromYAML(
				[]byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: not valid\nspec:\n  nodes: []\n"),
			),
			config.CreateWithValidation(),
		}, wantErr: true},
		{name: "already stored", opts: []config.CreateOption{config.CreateFromPath(filepath.Join(dir, "t.yaml"))}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Create(test.opts...)

			switch {
			case test.is != nil && !errors.Is(err, test.is):
				t.Errorf("config.Create(%s) error = %v, want %v", test.name, err, test.is)
			case test.wantErr && err == nil:
				t.Errorf("config.Create(%s) error = nil, want an error", test.name)
			case !test.wantErr && err != nil:
				t.Errorf("config.Create(%s) error = %v, want nil", test.name, err)
			}
		})
	}
}

// Edit saves the config the editor changed, and returns an error when the
// name is wrong, the editor fails or makes no change, or the YAML is bad.
func TestEditStoredConfig(t *testing.T) {
	storetest.UseBolt(t)

	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: edit-me\nspec:\n  nodes: []\n")

	t.Run("changed", func(t *testing.T) {
		useEditor(t, `sed 's/name: edit-me/name: edit-me\n    annotations:\n        edited: "yes"/' "$1" > "$1.new" && mv "$1.new" "$1"`)

		got, err := config.Edit("topology/edit-me", false)
		if err != nil {
			t.Fatalf("config.Edit(topology/edit-me) error = %v", err)
		}

		if got.Metadata.Annotations["edited"] != "yes" {
			t.Errorf("config.Edit(topology/edit-me) annotations = %v, want edited: yes", got.Metadata.Annotations)
		}

		stored, err := config.Get("topology/edit-me")
		if err != nil {
			t.Fatalf("config.Get(topology/edit-me) error = %v", err)
		}

		if stored.Metadata.Annotations["edited"] != "yes" {
			t.Errorf("stored annotations = %v, want edited: yes", stored.Metadata.Annotations)
		}
	})

	t.Run("not modified", func(t *testing.T) {
		useEditor(t, "true")

		_, err := config.Edit("topology/edit-me", false)
		if !config.IsConfigNotModified(err) {
			t.Errorf("config.Edit(topology/edit-me) with no change error = %v, want %v", err, editor.ErrNoChange)
		}
	})

	for _, test := range []struct{ name, config, editor string }{
		{name: "no name", editor: "true"},
		{name: "invalid name", config: "nokind/x", editor: "true"},
		{name: "missing config", config: "topology/missing", editor: "true"},
		{name: "editor fails", config: "topology/edit-me", editor: "exit 1"},
		{name: "bad YAML", config: "topology/edit-me", editor: `printf 'kind: [unclosed\n' > "$1"`},
		{name: "invalid spec", config: "topology/edit-me", editor: `sed 's/nodes: \[\]/nodes: 7/' "$1" > "$1.new" && mv "$1.new" "$1"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			useEditor(t, test.editor)

			if _, err := config.Edit(test.config, false); err == nil {
				t.Errorf("config.Edit(%q) error = nil, want an error", test.config)
			}
		})
	}
}

// Edit refuses a running experiment unless forced, and keeps the
// experiment name the editor cannot change.
func TestEditExperiment(t *testing.T) {
	storetest.UseBolt(t)

	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: running\n"+
		"spec:\n  experimentName: running\n  topology:\n    nodes: []\nstatus:\n  startTime: now\n")
	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: bad\nspec:\n  experimentName: 7\n")

	useEditor(t, `printf '# edited\n' >> "$1"`)

	if _, err := config.Edit("experiment/running", false); err == nil {
		t.Error("config.Edit(experiment/running, false) error = nil, want an error")
	}

	if _, err := config.Edit("experiment/bad", false); err == nil {
		t.Error("config.Edit(experiment/bad) with a spec that does not decode error = nil, want an error")
	}

	if _, err := config.Edit("experiment/running", true); err != nil {
		t.Fatalf("config.Edit(experiment/running, true) error = %v", err)
	}

	got, err := config.Get("experiment/running")
	if err != nil {
		t.Fatalf("config.Get(experiment/running) error = %v", err)
	}

	if got.Spec["experimentName"] != "running" {
		t.Errorf("forced edit stored experimentName = %v, want running", got.Spec["experimentName"])
	}
}

// Update stores a changed config, and returns an error for a name that is
// not stored or a config that fails validation.
func TestUpdateStoredConfig(t *testing.T) {
	storetest.UseBolt(t)

	stored := storeConfig(t, "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: s\nspec:\n  apps: []\n")

	changed := *stored
	changed.Metadata.Annotations = store.Annotations{"changed": "yes"}

	if err := config.Update("scenario/s", &changed); err != nil {
		t.Fatalf("config.Update(scenario/s) error = %v", err)
	}

	got, err := config.Get("scenario/s")
	if err != nil {
		t.Fatalf("config.Get(scenario/s) error = %v", err)
	}

	if diff := cmp.Diff(stored.Metadata.Created, got.Metadata.Created); diff != "" {
		t.Errorf("updated config created mismatch (-want +got):\n%s", diff)
	}

	if got.Metadata.Annotations["changed"] != "yes" {
		t.Errorf("updated annotations = %v, want changed: yes", got.Metadata.Annotations)
	}

	invalid := *got
	invalid.Spec = map[string]any{"apps": 7}

	if err := config.Update("scenario/s", &invalid); !errors.Is(err, types.ErrValidationFailed) {
		t.Errorf("config.Update(scenario/s, invalid) error = %v, want %v", err, types.ErrValidationFailed)
	}

	renamed := *got
	renamed.Metadata.Name = "not valid"

	if err := config.Update("scenario/s", &renamed); err == nil {
		t.Error("config.Update(scenario/s, invalid name) error = nil, want an error")
	}

	for _, name := range []string{"nokind/x", "scenario/missing"} {
		if err := config.Update(name, got); err == nil {
			t.Errorf("config.Update(%q) error = nil, want an error", name)
		}
	}
}

// DeleteAtRevision deletes one config at the given revision, or all configs,
// and returns the errors of a wrong name, revision or failed hook.
func TestDeleteAtRevision(t *testing.T) {
	storetest.UseBolt(t)
	useFailingDeleteHook()

	one := storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: one\nspec:\n  nodes: []\n")
	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: hook-fails\nspec:\n  nodes: []\n")

	if err := config.DeleteAtRevision("", 0); err == nil {
		t.Error(`config.DeleteAtRevision("") error = nil, want an error`)
	}

	if err := config.Delete("topology/missing"); err == nil {
		t.Error("config.Delete(topology/missing) error = nil, want an error")
	}

	if err := config.DeleteAtRevision("topology/one", one.Metadata.Revision+1); !errors.Is(err, store.ErrConflict) {
		t.Errorf("config.DeleteAtRevision(topology/one, stale) error = %v, want %v", err, store.ErrConflict)
	}

	if err := config.DeleteAtRevision("topology/one", one.Metadata.Revision); err != nil {
		t.Errorf("config.DeleteAtRevision(topology/one, current) error = %v, want nil", err)
	}

	if err := config.Delete("topology/hook-fails"); !errors.Is(err, errDeleteHook) {
		t.Errorf("config.Delete(topology/hook-fails) error = %v, want %v", err, errDeleteHook)
	}

	storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: hook-fails\nspec:\n  nodes: []\n")
	storeConfig(t, "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: s\nspec:\n  apps: []\n")

	if err := config.Delete("all"); !errors.Is(err, errDeleteHook) {
		t.Errorf(`config.Delete("all") error = %v, want %v`, err, errDeleteHook)
	}

	left, err := config.List("all")
	if err != nil {
		t.Fatalf(`config.List("all") error = %v`, err)
	}

	if len(left) != 0 {
		t.Errorf(`configs after config.Delete("all") = %v, want none`, fullNames(left))
	}
}
