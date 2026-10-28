package config_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
)

// storeExperiment stores the experiment config name, started at started
// unless it is "", without the config hooks.
func storeExperiment(t *testing.T, name, started string) {
	t.Helper()

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"experimentName": name, "topology": map[string]any{"nodes": []any{}}},
	}

	if started != "" {
		c.Status = map[string]any{"startTime": started}
	}

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(experiment %s) error = %v", name, err)
	}
}

// failureNames are the configs of failures, and whether each failed because
// its experiment runs.
func failureNames(failures []config.DeleteFailure) map[string]bool {
	names := make(map[string]bool, len(failures))
	for _, f := range failures {
		names[f.Config] = errors.Is(f.Err, config.ErrRunningExperiment)
	}

	return names
}

// DeleteKind deletes the configs of one kind, or of every kind, that the
// selection keeps, and does not delete a running experiment.
func TestDeleteKind(t *testing.T) {
	notBeta := func(c store.Config) bool { return c.Metadata.Name != "beta" }

	tests := []struct {
		name       string
		kind       string
		opts       []config.DeleteKindOption
		wantDel    []string
		wantFailed map[string]bool // config -> failed because its experiment runs
		left       []string
	}{
		{
			name: "one kind", kind: "TOPOLOGY", wantDel: []string{"Topology/alpha", "Topology/beta"},
			wantFailed: map[string]bool{}, left: []string{"Experiment/running", "Experiment/stopped"},
		},
		{
			name: "experiments, but not a running one", kind: "experiment", wantDel: []string{"Experiment/stopped"},
			wantFailed: map[string]bool{"Experiment/running": true},
			left:       []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		{
			name: "all, as selected", kind: "all", opts: []config.DeleteKindOption{config.DeleteKindSelected(notBeta)},
			wantDel:    []string{"Topology/alpha", "Experiment/stopped"},
			wantFailed: map[string]bool{"Experiment/running": true},
			left:       []string{"Experiment/running", "Topology/beta"},
		},
		{
			name: "a delete that fails", kind: "topology",
			opts: []config.DeleteKindOption{config.DeleteKindWith(func(name string) error {
				if name == "Topology/beta" {
					return errors.New("boom")
				}

				return config.Delete(name)
			})},
			wantDel: []string{"Topology/alpha"}, wantFailed: map[string]bool{"Topology/beta": false},
			left: []string{"Experiment/running", "Experiment/stopped", "Topology/beta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storetest.UseBolt(t)
			storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: alpha\nspec:\n  nodes: []\n")
			storeConfig(t, "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: beta\nspec:\n  nodes: []\n")
			storeExperiment(t, "running", "2024-01-01T00:00:00Z")
			storeExperiment(t, "stopped", "")

			deleted, failed, err := config.DeleteKind(tt.kind, tt.opts...)
			if err != nil {
				t.Fatalf("DeleteKind(%q) error = %v", tt.kind, err)
			}

			if diff := cmp.Diff(tt.wantDel, deleted); diff != "" {
				t.Errorf("DeleteKind(%q) deleted mismatch (-want +got):\n%s", tt.kind, diff)
			}

			if diff := cmp.Diff(tt.wantFailed, failureNames(failed)); diff != "" {
				t.Errorf("DeleteKind(%q) failures mismatch (-want +got):\n%s", tt.kind, diff)
			}

			configs, err := config.List("all")
			if err != nil {
				t.Fatalf("List(all) error = %v", err)
			}

			if diff := cmp.Diff(tt.left, fullNames(configs)); diff != "" {
				t.Errorf("configs after DeleteKind(%q) mismatch (-want +got):\n%s", tt.kind, diff)
			}
		})
	}
}

// DeleteKind refuses a kind List does not take, and the empty kind, which
// List takes as every kind.
func TestDeleteKindUnknownKind(t *testing.T) {
	storetest.UseBolt(t)

	for _, kind := range []string{"", "vm", "workflows"} {
		if _, _, err := config.DeleteKind(kind); !errors.Is(err, config.ErrUnknownKind) {
			t.Errorf("DeleteKind(%q) error = %v, want %v", kind, err, config.ErrUnknownKind)
		}
	}
}

// DeleteKind takes "all" and each config kind in any letter case, and refuses
// any other kind with ErrUnknownKind.
func TestDeleteKindKindProperty(t *testing.T) {
	storetest.UseBolt(t)

	known := append([]string{"all"}, config.AllKinds...)
	none := config.DeleteKindSelected(func(store.Config) bool { return false })

	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom(known).Draw(t, "known")
		flips := rapid.SliceOfN(rapid.Bool(), len(kind), len(kind)).Draw(t, "flips")

		recased := []rune(kind)
		for i, flip := range flips {
			if flip {
				recased[i] = unicode.ToUpper(recased[i])
			}
		}

		if _, _, err := config.DeleteKind(string(recased), none); err != nil {
			t.Fatalf("DeleteKind(%q) error = %v, want nil", string(recased), err)
		}

		other := rapid.String().Draw(t, "other")
		for _, k := range known {
			if strings.EqualFold(k, other) {
				return
			}
		}

		if _, _, err := config.DeleteKind(other, none); !errors.Is(err, config.ErrUnknownKind) {
			t.Fatalf("DeleteKind(%q) error = %v, want %v", other, err, config.ErrUnknownKind)
		}
	})
}
