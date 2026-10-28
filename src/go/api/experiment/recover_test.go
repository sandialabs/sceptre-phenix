package experiment

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/plog/plogtest"
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

// recoverStore is a BoltDB whose List or Update fails while listErr or updateErr is set, counting the updates.
type recoverStore struct {
	*store.BoltDB

	listErr, updateErr error
	updates            atomic.Int64
}

func (s *recoverStore) List(kinds ...string) (store.Configs, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	return s.BoltDB.List(kinds...) //nolint:wrapcheck // the store's own error
}

func (s *recoverStore) Update(c *store.Config) error {
	s.updates.Add(1)

	if s.updateErr != nil {
		return s.updateErr
	}

	return s.BoltDB.Update(c) //nolint:wrapcheck // the store's own error
}

// useRecoverStore makes a new recoverStore the phenix store until the test ends.
func useRecoverStore(t *testing.T) *recoverStore {
	t.Helper()

	s := &recoverStore{BoltDB: storetest.NewBolt(t)}
	storetest.Use(t, s)

	return s
}

// ResetAppsRunning writes only the experiments it changes, and reports a store that fails.
func TestResetAppsRunningErrors(t *testing.T) { //nolint:paralleltest // replaces the package store
	failed := errors.New("store is read-only")

	tests := []struct {
		name        string
		status      map[string]any
		listErr     error
		updateErr   error
		wantErr     []string // parts of the error, none for no error
		wantUpdates int64
	}{
		{
			name:   "no flag set writes nothing",
			status: map[string]any{"startTime": stopTestStart, "appRunningStageStatus": map[string]any{"soh": false}},
		},
		{
			name:   "no flags at all writes nothing",
			status: map[string]any{"startTime": stopTestStart},
		},
		{
			name:        "a set flag is written",
			status:      map[string]any{"startTime": stopTestStart, "appRunningStageStatus": map[string]any{"soh": true}},
			wantUpdates: 1,
		},
		{
			name:    "the experiments cannot be listed",
			status:  map[string]any{"startTime": stopTestStart},
			listErr: failed,
			wantErr: []string{"listing running experiments", failed.Error()},
		},
		{
			name:        "the cleared flags cannot be written",
			status:      map[string]any{"startTime": stopTestStart, "appRunningStageStatus": map[string]any{"soh": true}},
			updateErr:   failed,
			wantErr:     []string{"clearing app running flags of experiment exp", failed.Error()},
			wantUpdates: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			storeExperiment(t, "exp", tc.status)

			s.listErr, s.updateErr = tc.listErr, tc.updateErr

			err := ResetAppsRunning()

			switch {
			case len(tc.wantErr) == 0 && err != nil:
				t.Errorf("ResetAppsRunning() error = %v, want nil", err)
			case len(tc.wantErr) > 0 && (err == nil || !errors.Is(err, failed)):
				t.Errorf("ResetAppsRunning() error = %v, want one wrapping %v", err, failed)
			}

			for _, part := range tc.wantErr {
				if err != nil && !strings.Contains(err.Error(), part) {
					t.Errorf("ResetAppsRunning() error = %q, want it to contain %q", err, part)
				}
			}

			if got := s.updates.Load(); got != tc.wantUpdates {
				t.Errorf("ResetAppsRunning() store updates = %d, want %d", got, tc.wantUpdates)
			}
		})
	}
}

// A failure writing one experiment does not keep the others from being cleared.
func TestResetAppsRunningGoesOnAfterAFailure(t *testing.T) { //nolint:paralleltest // replaces the package store
	s := useRecoverStore(t)

	for _, name := range []string{"a", "b"} {
		storeExperiment(t, name, map[string]any{"startTime": stopTestStart, "appRunningStageStatus": map[string]any{"soh": true}})
	}

	s.updateErr = errors.New("store is read-only")

	err := ResetAppsRunning()

	if diff := cmp.Diff([]string{
		"clearing app running flags of experiment a: saving experiment config: store is read-only",
		"clearing app running flags of experiment b: saving experiment config: store is read-only",
	}, errorTexts(err)); diff != "" {
		t.Errorf("ResetAppsRunning() errors mismatch (-want +got):\n%s", diff)
	}

	if got := s.updates.Load(); got != 2 {
		t.Errorf("ResetAppsRunning() store updates = %d, want 2", got)
	}
}

// A stored experiment config that does not decode does not stop the recovery passes for the other experiments. Each
// pass logs a warning that names the config it skips.
func TestRecoverySkipsAConfigThatDoesNotDecode(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)
	useMarkerShell(t)

	logs := plogtest.Capture(t)

	broken := hookConfig("broken", map[string]any{"experimentName": "broken"}, map[string]any{"startTime": stopTestStart})
	broken.Version = "phenix.sandia.gov/v9"

	if err := store.Create(broken); err != nil {
		t.Fatalf("store.Create(experiment broken) error = %v", err)
	}

	dead := ifaces.StartMarker{Since: stopTestStart, PID: 4243, Host: thisHost(t)}
	status := markerStatus(dead)
	status["startTime"] = stopTestStart
	status["appRunningStageStatus"] = map[string]any{"soh": true}
	status["postStartPending"] = true
	storeExperiment(t, "good", status)

	var pending []string

	tests := []struct {
		pass string
		run  func() error
	}{
		{"app running flag reset", ResetAppsRunning},
		{"start marker cleanup", ClearStaleStartMarkers},
		{"pending start scan", func() error {
			var err error

			pending, err = PendingStarts()

			return err
		}},
	}

	for _, tc := range tests {
		if err := tc.run(); err != nil {
			t.Errorf("%s error = %v, want nil", tc.pass, err)
		}

		var got []string
		for _, r := range logs.Take(t, plogtest.Message(tc.pass+" skips an experiment config that does not decode")) {
			got = append(got, fmt.Sprint(r["exp"]))
		}

		if diff := cmp.Diff([]string{"broken"}, got); diff != "" {
			t.Errorf("%s skipped configs mismatch (-want +got):\n%s", tc.pass, diff)
		}
	}

	if diff := cmp.Diff([]string{"good"}, pending); diff != "" {
		t.Errorf("PendingStarts() mismatch (-want +got):\n%s", diff)
	}

	good := experimentNamed(t, "good")

	if diff := cmp.Diff(map[string]bool{"soh": false}, good.Status.AppRunning()); diff != "" {
		t.Errorf("experiment good app running flags mismatch (-want +got):\n%s", diff)
	}

	if m := good.Status.Starting(); !m.IsZero() {
		t.Errorf("experiment good start marker = %+v, want none", m)
	}
}
