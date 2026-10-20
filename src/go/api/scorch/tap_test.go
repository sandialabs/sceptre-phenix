package scorch

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/types"
	"phenix/util/tap"
)

// TestSetScorchStatusKeepsStoredStatus: a run's experiment copy is taken when
// the run starts, so writing it whole would drop what components such as SOH
// stored since.
func TestSetScorchStatusKeepsStoredStatus(t *testing.T) {
	const name = "scorch-tap-status"

	config := func(apps map[string]any) store.Config {
		return store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: store.ConfigMetadata{Name: name},
			Spec:     map[string]any{"experimentName": name},
			Status:   map[string]any{"apps": apps},
		}
	}

	snapshot, err := types.DecodeExperimentFromConfig(config(map[string]any{
		"soh":    map[string]any{"initialized": true},
		"scorch": map[string]any{"runID": 0, "taps": map[string]any{}},
	}))
	if err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	stored := config(map[string]any{
		"soh":    map[string]any{"initialized": true, "hosts": []any{"vm1"}},
		"scorch": map[string]any{"runID": 0, "taps": map[string]any{}},
	})

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = stored

		return nil
	}).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		stored = *c

		return nil
	}).Times(1)

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore store

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	// Components each hold a copy of the run's experiment sharing one status.
	component, later := *snapshot, *snapshot

	status := scorchmd.ScorchStatus{Taps: map[string]*tap.Tap{"cmp": {Name: "t"}}}

	if err := setScorchStatus(&component, status); err != nil {
		t.Fatalf("setScorchStatus: %v", err)
	}

	exp, err := types.DecodeExperimentFromConfig(stored)
	if err != nil {
		t.Fatalf("decoding stored experiment: %v", err)
	}

	var soh map[string]any

	_ = exp.Status.ParseAppStatus("soh", &soh)
	if soh["hosts"] == nil {
		t.Fatalf("stored soh status = %v, want its hosts kept", soh)
	}

	for what, e := range map[string]*types.Experiment{"stored": exp, "shared": &later} {
		var got scorchmd.ScorchStatus

		_ = e.Status.ParseAppStatus("scorch", &got)
		if got.Taps["cmp"] == nil {
			t.Fatalf("%s scorch status = %+v, want the tap", what, got)
		}
	}
}
