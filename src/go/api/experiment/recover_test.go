package experiment

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
)

// storeExperiment stores an experiment with no nodes or apps of its own and the given status.
func storeExperiment(t *testing.T, name string, status map[string]any) {
	t.Helper()

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"experimentName": name, "baseDir": t.TempDir()},
		Status:   status,
	})
	if err != nil {
		t.Fatalf("store.Create(experiment %s) error = %v", name, err)
	}
}

// experimentNamed reads experiment name back from the store.
func experimentNamed(t *testing.T, name string) *types.Experiment {
	t.Helper()

	c, _ := store.NewConfig("experiment/" + name)
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment %s) error = %v", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(%s) error = %v", name, err)
	}

	return exp
}

func TestResetAppsRunningClearsOnlyRunningExperiments(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	flags := func() map[string]any { return map[string]any{"scorch": true, "soh": false} }

	storeExperiment(t, "running", map[string]any{
		"startTime":             stopTestStart,
		"appRunningStageStatus": flags(),
		"apps":                  map[string]any{"soh": map[string]any{"hosts": 2}},
	})
	storeExperiment(t, "stopped", map[string]any{"startTime": "", "appRunningStageStatus": flags()})

	if err := ResetAppsRunning(); err != nil {
		t.Fatalf("ResetAppsRunning() error = %v", err)
	}

	tests := []struct {
		name string
		want map[string]bool
	}{
		{"running", map[string]bool{"scorch": false, "soh": false}},
		{"stopped", map[string]bool{"scorch": true, "soh": false}},
	}

	for _, test := range tests {
		exp := experimentNamed(t, test.name)

		if diff := cmp.Diff(test.want, exp.Status.AppRunning(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("experiment %s app running flags after ResetAppsRunning() mismatch (-want +got):\n%s", test.name, diff)
		}
	}

	// the rest of the status stays as it was
	running := experimentNamed(t, "running")

	if got := running.Status.StartTime(); got != stopTestStart {
		t.Errorf("running experiment start time after ResetAppsRunning() = %q, want %q", got, stopTestStart)
	}

	if _, ok := running.Status.AppStatus()["soh"]; !ok {
		t.Error("running experiment lost its soh app status in ResetAppsRunning(), want it kept")
	}
}
