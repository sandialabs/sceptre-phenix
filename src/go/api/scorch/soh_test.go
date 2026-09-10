package scorch

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/scorch/scorchmd"
	"phenix/api/soh"
	"phenix/store"
	"phenix/types"
)

// sohExperimentConfig is an experiment with the soh app configured,
// initialised, and flagged as running its checks.
func sohExperimentConfig(name string) store.Config {
	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"experimentName": name,
			"scenario": map[string]any{
				"apps": []map[string]any{{"name": "soh", "metadata": map[string]any{}}},
			},
		},
		Status: map[string]any{
			"apps":                  map[string]any{"soh": map[string]any{"initialized": true}},
			"appRunningStageStatus": map[string]any{"soh": true},
		},
	}
}

// TestSOHCheckHonoursFailOnErrorWhenAlreadyRunning: the component used to
// consult failOnError before decoding its metadata, so SoH checks already in
// progress never failed the stage.
func TestSOHCheckHonoursFailOnErrorWhenAlreadyRunning(t *testing.T) {
	const name = "soh-already-running"

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = sohExperimentConfig(name)

		return nil
	}).AnyTimes()

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore store

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	exp, err := types.DecodeExperimentFromConfig(sohExperimentConfig(name))
	if err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	if !soh.Running(exp) {
		t.Fatal("fixture must flag the soh app as running")
	}

	for _, failOnError := range []bool{false, true} {
		cmp := new(SOH)

		_ = cmp.Init(
			Experiment(*exp),
			Name("soh-cmp"),
			Type("soh"),
			Metadata(scorchmd.ComponentMetadata{"failOnError": failOnError}),
		)

		err := cmp.check(context.Background(), ActionStart)

		if failOnError && err == nil {
			t.Fatal("failOnError set: expected an error while SoH checks are already running")
		}

		if !failOnError && err != nil {
			t.Fatalf("failOnError unset: unexpected error: %v", err)
		}
	}
}
