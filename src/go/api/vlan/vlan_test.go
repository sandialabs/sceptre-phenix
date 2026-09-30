package vlan

import (
	"errors"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"

	"phenix/store"
	"phenix/types"
)

// useExperiments points the store at a mock that serves the given experiments
// by name and reports any other name as missing.
func useExperiments(t *testing.T, exps ...*types.Experiment) {
	t.Helper()

	configs := make(map[string]store.Config, len(exps))

	for _, exp := range exps {
		configs[exp.Metadata.Name] = store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: exp.Metadata,
			Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
			Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
		}
	}

	ctrl := gomock.NewController(t)
	m := store.NewMockStore(ctrl)

	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		cfg, ok := configs[c.Metadata.Name]
		if !ok {
			return errors.New("config does not exist")
		}

		*c = cfg

		return nil
	}).AnyTimes()

	orig := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = orig }) //nolint:reassign // restore after test
}

func newExperiment(name string, aliases map[string]int) *types.Experiment {
	exp := types.NewExperiment(store.ConfigMetadata{Name: name})
	exp.Spec.SetExperimentName(name)
	exp.Spec.VLANs().SetAliases(aliases)

	return exp
}

func TestAliasID(t *testing.T) {
	stopped := newExperiment("stopped", map[string]int{"MYVLAN": 105, "AUTO": 0})

	// A running experiment reports the VLAN IDs minimega assigned, which are
	// recorded in its status rather than its spec.
	running := newExperiment("running", map[string]int{"AUTO": 0})
	running.Status.SetStartTime("2024-01-01T00:00:00Z")
	running.Status.SetVLANs(map[string]int{"AUTO": 201})

	useExperiments(t, stopped, running)

	tests := []struct {
		name    string
		exp     string
		alias   string
		want    int
		wantErr error
		anyErr  bool
	}{
		{name: "stopped experiment", exp: "stopped", alias: "MYVLAN", want: 105},
		{name: "running experiment", exp: "running", alias: "AUTO", want: 201},
		{name: "missing alias", exp: "stopped", alias: "OTHER", wantErr: ErrAliasNotFound},
		{name: "alias is case sensitive", exp: "stopped", alias: "myvlan", wantErr: ErrAliasNotFound},
		{name: "unassigned alias", exp: "stopped", alias: "AUTO", wantErr: ErrAliasUnassigned},
		{name: "missing experiment", exp: "missing", alias: "MYVLAN", anyErr: true},
		{name: "no experiment", alias: "MYVLAN", anyErr: true},
		{name: "no alias", exp: "stopped", anyErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := AliasID(Experiment(test.exp), Alias(test.alias))

			switch {
			case test.wantErr != nil:
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("expected error %v, got %v", test.wantErr, err)
				}
			case test.anyErr:
				if err == nil {
					t.Fatal("expected an error")
				}

				if errors.Is(err, ErrAliasNotFound) || errors.Is(err, ErrAliasUnassigned) {
					t.Fatalf("expected an error other than a missing or unassigned alias, got %v", err)
				}
			case err != nil:
				t.Fatalf("expected no error, got %v", err)
			case got != test.want:
				t.Fatalf("expected VLAN ID %d, got %d", test.want, got)
			}
		})
	}
}
