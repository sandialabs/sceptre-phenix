package config_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
)

const (
	applyTopology = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: demo\n" +
		"spec:\n  nodes:\n  - type: VirtualMachine\n    general:\n      hostname: host-00\n" +
		"    hardware:\n      os_type: linux\n      drives:\n      - image: ubuntu.qc2\n"

	// applyServerAnnotation is an annotation registered as one phenix sets.
	applyServerAnnotation = "phenix-test/server-set"
)

var (
	registerApplyHooks sync.Once //nolint:gochecknoglobals // hooks are global; register them once

	// errRaceHook makes the race hook skip its write.
	errRaceHook = errors.New("race hook not armed")

	registerContextHook sync.Once //nolint:gochecknoglobals // hooks are global; register it once
)

// hookStagesKey is the context key of a *[]string, to which the context hook
// adds each stage that it runs with that context.
type hookStagesKey struct{}

// useContextHook registers a Topology hook that adds its stage to the list in
// the hookStagesKey value of its context, when there is one.
func useContextHook() {
	registerContextHook.Do(func() {
		config.RegisterConfigHook("Topology", func(ctx context.Context, stage string, _ *store.Config) error {
			if stages, ok := ctx.Value(hookStagesKey{}).(*[]string); ok {
				*stages = append(*stages, stage)
			}

			return nil
		})
	})
}

// Create and Apply give the context of their option to the "create" stage of
// the config hooks.
func TestCreateHooksGetTheContext(t *testing.T) {
	useContextHook()

	tests := []struct {
		name   string
		create func(context.Context) error
	}{
		{
			name: "create",
			create: func(ctx context.Context) error {
				_, err := config.Create(config.CreateFromYAML([]byte(applyTopology)), config.CreateWithContext(ctx))

				return err
			},
		},
		{
			name: "apply",
			create: func(ctx context.Context) error {
				_, err := config.Apply(decodeYAML(t, applyTopology), config.ApplyWithContext(ctx))

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storetest.UseBolt(t)

			var stages []string

			if err := test.create(context.WithValue(t.Context(), hookStagesKey{}, &stages)); err != nil {
				t.Fatalf("%s(topology demo) error = %v", test.name, err)
			}

			if diff := cmp.Diff([]string{"create"}, stages); diff != "" {
				t.Errorf("%s(topology demo) hook stages with its context mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}
}

// DeleteAtRevision gives the context of DeleteWithContext to the "delete" stage
// of the config hooks, also when it deletes all configs.
func TestDeleteHooksGetTheContext(t *testing.T) {
	useContextHook()

	for _, name := range []string{"topology/demo", "all"} {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)

			if _, err := config.Create(config.CreateFromYAML([]byte(applyTopology))); err != nil {
				t.Fatalf("config.Create(topology demo) error = %v", err)
			}

			var stages []string

			ctx := context.WithValue(t.Context(), hookStagesKey{}, &stages)
			if err := config.DeleteAtRevision(name, 0, config.DeleteWithContext(ctx)); err != nil {
				t.Fatalf("config.DeleteAtRevision(%s) error = %v", name, err)
			}

			if diff := cmp.Diff([]string{"delete"}, stages); diff != "" {
				t.Errorf("config.DeleteAtRevision(%s) hook stages with its context mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// useApplyHooks registers the test's server annotation, and a Topology hook
// that, for a config named races, writes the stored config again during an
// update, as another client would.
func useApplyHooks() {
	registerApplyHooks.Do(func() {
		config.RegisterServerAnnotation(applyServerAnnotation)

		config.RegisterConfigHook("Topology", func(_ context.Context, stage string, c *store.Config) error {
			if stage != "update" || c.Metadata.Name != "races" || c.Stored == nil {
				return nil
			}

			other := *c.Stored
			other.IfRevision = 0

			if err := store.Update(&other); err != nil {
				return errors.Join(errRaceHook, err)
			}

			return nil
		})
	})
}

// decodeYAML returns the config of the YAML doc, as a file read gives it.
func decodeYAML(t *testing.T, doc string) *store.Config {
	t.Helper()

	c, err := store.DecodeConfigYAML([]byte(doc))
	if err != nil {
		t.Fatalf("store.DecodeConfigYAML(%q) error = %v", doc, err)
	}

	return c
}

// Apply creates a new config, keeps one that is the same, and updates one
// that differs; a dry run reports the same actions and changes nothing.
func TestApplyActions(t *testing.T) {
	changed := strings.Replace(applyTopology, "ubuntu.qc2", "kali.qc2", 1)

	tests := []struct {
		name   string
		opts   []config.ApplyOption
		docs   []string
		want   []config.ApplyAction
		stored string // the file that the store then holds
	}{
		{
			name:   "apply",
			docs:   []string{applyTopology, applyTopology, changed, changed},
			want:   []config.ApplyAction{config.ApplyCreated, config.ApplyUnchanged, config.ApplyUpdated, config.ApplyUnchanged},
			stored: changed,
		},
		{
			name:   "dry run",
			opts:   []config.ApplyOption{config.ApplyDryRun()},
			docs:   []string{applyTopology, changed, changed},
			want:   []config.ApplyAction{config.ApplyUnchanged, config.ApplyUpdated, config.ApplyUpdated},
			stored: applyTopology,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storetest.UseBolt(t)

			if len(test.opts) > 0 {
				storeConfig(t, applyTopology)
			}

			got := make([]config.ApplyAction, 0, len(test.docs))

			for _, doc := range test.docs {
				action, err := config.Apply(decodeYAML(t, doc), test.opts...)
				if err != nil {
					t.Fatalf("Apply(%q) error = %v", doc, err)
				}

				got = append(got, action)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Apply actions mismatch (-want +got):\n%s", diff)
			}

			cmp, err := config.Compare(decodeYAML(t, test.stored))
			if err != nil || cmp.Changed() {
				t.Errorf("Compare(%q) after Apply = changed %t, %v, want unchanged", test.stored, cmp != nil && cmp.Changed(), err)
			}
		})
	}
}

// A dry run of a new config does not store it.
func TestApplyDryRunCreate(t *testing.T) {
	storetest.UseBolt(t)

	got, err := config.Apply(decodeYAML(t, applyTopology), config.ApplyDryRun())
	if err != nil || got != config.ApplyCreated {
		t.Fatalf("Apply(dry run) = %q, %v, want %q, nil", got, err, config.ApplyCreated)
	}

	if _, err := config.Get("topology/demo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(topology/demo) after a dry run error = %v, want %v", err, store.ErrNotExist)
	}
}

// Compare ignores what phenix sets: created, updated, revision, status, the
// experiment name and the registered server annotations. It does not ignore
// other annotations.
func TestCompareIgnoresServerFields(t *testing.T) {
	useApplyHooks()

	const experiment = "apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: exp\n" +
		"  annotations:\n    topology: demo\nspec:\n  baseDir: /phenix/experiments/exp\n"

	stored := decodeYAML(t, experiment)
	stored.Spec["experimentName"] = "exp"
	stored.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z"}
	stored.Metadata.Annotations[applyServerAnnotation] = "set by phenix"

	tests := []struct {
		name        string
		doc         string
		wantChanged bool
	}{
		{name: "the same file", doc: experiment, wantChanged: false},
		{name: "a file with metadata and status", doc: strings.Replace(experiment, "  name: exp\n",
			"  name: exp\n  created: x\n  updated: y\n  revision: 7\n", 1) + "status:\n  startTime: z\n", wantChanged: false},
		{name: "a file with another annotation", doc: strings.Replace(experiment, "topology: demo",
			"topology: demo\n    owner: me", 1), wantChanged: true},
		{name: "a file with another spec", doc: strings.Replace(experiment, "/exp\n", "/other\n", 1), wantChanged: true},
		{name: "a file with another kind spelling", doc: strings.Replace(experiment, "kind: Experiment", "kind: experiment", 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := storetest.NewBolt(t)
			storetest.Use(t, db)

			c := *stored
			if err := db.Create(&c); err != nil {
				t.Fatalf("store.Create(experiment/exp) error = %v", err)
			}

			cmp, err := config.Compare(decodeYAML(t, test.doc))
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}

			if got := cmp.Changed(); got != test.wantChanged {
				t.Errorf("Compare().Changed() = %t, want %t; diff:\n%s", got, test.wantChanged, cmp.Diff("stored", "file"))
			}

			if cmp.Name != "Experiment/exp" {
				t.Errorf("Compare().Name = %q, want %q", cmp.Name, "Experiment/exp")
			}
		})
	}
}

// Compare and Apply refuse a User config and a config of an unknown kind.
func TestApplyRefusesKinds(t *testing.T) {
	storetest.UseBolt(t)

	for _, doc := range []string{
		"apiVersion: phenix.sandia.gov/v1\nkind: User\nmetadata:\n  name: alice\nspec:\n  username: alice\n",
		"apiVersion: phenix.sandia.gov/v1\nkind: Widget\nmetadata:\n  name: w\nspec: {}\n",
	} {
		if _, err := config.Apply(decodeYAML(t, doc)); !errors.Is(err, config.ErrApplyKind) {
			t.Errorf("Apply(%q) error = %v, want %v", doc, err, config.ErrApplyKind)
		}
	}

	if _, err := config.Compare(decodeYAML(t, "kind: Topology\nmetadata: {}\n")); err == nil {
		t.Error("Compare(no name) error = nil, want an error")
	}
}

// Apply refuses to update the config of a running experiment, and keeps the
// stored status and experiment name in an update of a stopped one.
func TestApplyExperiment(t *testing.T) {
	const experiment = "apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: exp\n" +
		"  annotations:\n    topology: demo\nspec:\n  baseDir: /phenix/experiments/exp\n  topology:\n    nodes: []\n"

	changed := strings.Replace(experiment, "/exp\n", "/moved\n", 1)

	for _, test := range []struct {
		name    string
		status  map[string]any
		wantErr error
	}{
		{name: "running", status: map[string]any{"startTime": "2026-01-01T00:00:00Z"}, wantErr: config.ErrRunningExperiment},
		{name: "stopped", status: map[string]any{"endTime": "2026-01-02T00:00:00Z"}, wantErr: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := storetest.UseBolt(t)

			c := decodeYAML(t, experiment)
			c.Spec["experimentName"] = "exp-original"
			c.Status = test.status

			if err := db.Create(c); err != nil {
				t.Fatalf("store.Create(experiment/exp) error = %v", err)
			}

			_, err := config.Apply(decodeYAML(t, changed))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Apply(changed experiment) error = %v, want %v", err, test.wantErr)
			}

			stored, err := config.Get("experiment/exp")
			if err != nil {
				t.Fatalf("config.Get(experiment/exp) error = %v", err)
			}

			if diff := cmp.Diff(test.status, stored.Status); diff != "" {
				t.Errorf("stored status after Apply mismatch (-want +got):\n%s", diff)
			}

			if got := stored.Spec["experimentName"]; got != "exp-original" {
				t.Errorf("stored experimentName after Apply = %v, want exp-original", got)
			}
		})
	}
}

// Apply refuses an update when the stored config changes after Apply read it.
func TestApplyDetectsConflicts(t *testing.T) {
	useApplyHooks()
	storetest.UseBolt(t)

	doc := strings.Replace(applyTopology, "name: demo", "name: races", 1)
	storeConfig(t, doc)

	_, err := config.Apply(decodeYAML(t, strings.Replace(doc, "ubuntu.qc2", "kali.qc2", 1)))
	if !errors.Is(err, store.ErrConflict) {
		t.Errorf("Apply(races) error = %v, want %v", err, store.ErrConflict)
	}
}

// ApplyOrder puts topologies before scenarios, experiments, images and roles,
// and keeps the order within a kind.
func TestApplyOrder(t *testing.T) {
	kinds := []string{"Role", "Experiment", "user", "Image", "scenario", "Topology", "topology"}
	want := []string{"Topology", "topology", "scenario", "Experiment", "Image", "Role", "user"}

	got := slices.Clone(kinds)
	slices.SortStableFunc(got, config.ApplyOrder)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SortStableFunc(%q, ApplyOrder) mismatch (-want +got):\n%s", kinds, diff)
	}
}
