package types

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/store"
)

func experimentConfig(name string, status map[string]any) store.Config {
	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"experimentName": name},
		Status:   status,
	}
}

// TestUpdateAppStatusMergesStoredStatus: an app finishing a long run must not
// overwrite what other apps stored meanwhile, which WriteToStore does because
// it writes the caller's whole (stale) status.
func TestUpdateAppStatusMergesStoredStatus(t *testing.T) {
	const name = "merge-status"

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	// The copy taken before the run: only soh is known.
	exp, err := DecodeExperimentFromConfig(experimentConfig(name, map[string]any{
		"apps": map[string]any{"soh": map[string]any{"initialized": true}},
	}))
	if err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	// What the store holds now: another app has written since.
	stored := experimentConfig(name, map[string]any{
		"apps": map[string]any{
			"soh":   map[string]any{"initialized": true},
			"other": map[string]any{"count": 1},
		},
		"appRunningStageStatus": map[string]any{"other": true},
	})

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

	err = exp.UpdateAppStatus("soh", func(status map[string]any) {
		status["hosts"] = []string{"vm1"}
	})
	if err != nil {
		t.Fatalf("UpdateAppStatus: %v", err)
	}

	apps, _ := stored.Status["apps"].(map[string]any)

	soh, _ := apps["soh"].(map[string]any)
	if soh["initialized"] != true || soh["hosts"] == nil {
		t.Fatalf("soh status = %v, want initialized kept and hosts added", soh)
	}

	if _, ok := apps["other"]; !ok {
		t.Fatalf("apps = %v, want the other app's status kept", apps)
	}

	running, _ := stored.Status["appRunningStageStatus"].(map[string]bool)
	if !running["other"] {
		t.Fatalf("appRunningStageStatus = %v, want the other app's flag kept", running)
	}

	// The in-memory copy follows the store so later whole-status writes carry
	// the merge forward.
	if _, ok := exp.Status.AppStatus()["other"]; !ok {
		t.Fatalf("in-memory apps = %v, want refreshed from the store", exp.Status.AppStatus())
	}
}
