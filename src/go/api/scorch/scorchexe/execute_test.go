package scorchexe

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/tap"
)

// AddCanceler gives a run a context that its canceler ends; GetCanceler
// returns the canceler once, and GetExperimentCancelers returns those of the
// named experiment only.
//
//nolint:paralleltest // uses the package cancelers
func TestCancelers(t *testing.T) {
	ctx := AddCanceler(context.Background(), "cancel-a", 1)
	_ = AddCanceler(context.Background(), "cancel-a", 2)
	other := AddCanceler(context.Background(), "cancel-ab", 1)

	t.Cleanup(func() {
		for _, cancel := range GetExperimentCancelers("cancel-ab") {
			cancel()
		}
	})

	for _, run := range []int{1, 2} {
		if !HasCanceler("cancel-a", run) {
			t.Errorf("HasCanceler(cancel-a, %d) = false, want true", run)
		}
	}

	if HasCanceler("cancel-a", 3) {
		t.Errorf("HasCanceler(cancel-a, 3) = true, want false")
	}

	cancel := GetCanceler("cancel-a", 1)
	if cancel == nil {
		t.Fatalf("GetCanceler(cancel-a, 1) = nil, want a canceler")
	}

	cancel()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("run context error = %v after its canceler, want %v", ctx.Err(), context.Canceled)
	}

	if HasCanceler("cancel-a", 1) || GetCanceler("cancel-a", 1) != nil {
		t.Errorf("GetCanceler(cancel-a, 1) keeps the canceler, want it removed")
	}

	if got := GetExperimentCancelers("cancel-a"); len(got) != 1 {
		t.Errorf("GetExperimentCancelers(cancel-a) = %d cancelers, want 1 (not those of cancel-ab)", len(got))
	}

	if HasCanceler("cancel-a", 2) {
		t.Errorf("GetExperimentCancelers(cancel-a) keeps run 2, want it removed")
	}

	if other.Err() != nil || !HasCanceler("cancel-ab", 1) {
		t.Errorf("GetExperimentCancelers(cancel-a) touched cancel-ab")
	}

	if got := GetExperimentCancelers("cancel-none"); got != nil {
		t.Errorf("GetExperimentCancelers(cancel-none) = %v, want nil", got)
	}
}

// SetRunID and SetCleanupOnly mark a context; an unmarked one has run 0 and
// is not cleanup only.
func TestContextValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if MustRunID(ctx) != 0 || CleanupOnly(ctx) {
		t.Errorf("MustRunID, CleanupOnly(background) = %d, %t; want 0, false", MustRunID(ctx), CleanupOnly(ctx))
	}

	ctx = SetCleanupOnly(SetRunID(ctx, 4))

	if MustRunID(ctx) != 4 || !CleanupOnly(ctx) {
		t.Errorf("MustRunID, CleanupOnly(marked) = %d, %t; want 4, true", MustRunID(ctx), CleanupOnly(ctx))
	}
}

// executeApp is the SCORCH app for Execute tests; running is what its Running
// stage does.
type executeApp struct{}

var (
	executeRegister sync.Once                                      //nolint:gochecknoglobals // one registration per test binary
	executeRunning  func(context.Context, *types.Experiment) error //nolint:gochecknoglobals // set by each test
)

func (executeApp) Init(...app.Option) error                           { return nil }
func (executeApp) Name() string                                       { return "scorch" }
func (executeApp) Configure(context.Context, *types.Experiment) error { return nil }
func (executeApp) PreStart(context.Context, *types.Experiment) error  { return nil }
func (executeApp) PostStart(context.Context, *types.Experiment) error { return nil }
func (executeApp) Cleanup(context.Context, *types.Experiment) error   { return nil }
func (executeApp) Running(ctx context.Context, e *types.Experiment) error {
	return executeRunning(ctx, e)
}

// executeMM fakes the minimega tap commands; each fails with err.
type executeMM struct {
	mm.Minimega

	calls int
	err   error
}

func (f *executeMM) TapVLAN(...mm.TapOption) error {
	f.calls++

	return f.err
}

func (*executeMM) Headnode() string { return "headnode" }

// executeExperiment stores experiment exp, with a SCORCH app when scorch is
// set and started when started is set, and returns it as read from the store.
func executeExperiment(t *testing.T, scorch, started bool) *types.Experiment {
	t.Helper()

	storetest.UseBolt(t)

	t.Setenv("PATH", t.TempDir())

	executeRegister.Do(func() {
		if err := app.RegisterUserApp("scorch", func() app.App { return executeApp{} }); err != nil {
			t.Fatalf("app.RegisterUserApp(scorch) error = %v", err)
		}
	})

	var apps []any
	if scorch {
		apps = []any{map[string]any{"name": "scorch"}}
	}

	status := map[string]any{}
	if started {
		status["startTime"] = "2026-11-04T00:00:00Z"
	}

	c := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"topology":       map[string]any{"nodes": []any{}},
			"scenario":       map[string]any{"apps": apps},
		},
		Status: status,
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("store.Create(experiment/exp) error = %v", err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	return exp
}

// Execute fails before it runs SCORCH for an experiment without a SCORCH app,
// one that is not running, or one whose SCORCH app already runs.
//
//nolint:paralleltest // replaces the store and PATH
func TestExecuteRefuses(t *testing.T) {
	tests := map[string]struct {
		scorch, started, running bool
	}{
		"no SCORCH app":   {started: true},
		"not running":     {scorch: true},
		"already running": {scorch: true, started: true, running: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			exp := executeExperiment(t, tc.scorch, tc.started)
			exp.Status.SetAppRunning("scorch", tc.running)

			executeRunning = func(context.Context, *types.Experiment) error {
				t.Errorf("Execute(%s) ran SCORCH, want it refused", name)

				return nil
			}

			if err := Execute(context.Background(), exp, 0); err == nil {
				t.Errorf("Execute(%s) = nil, want an error", name)
			}
		})
	}
}

// Execute fails when it cannot record in the store that SCORCH runs.
//
//nolint:paralleltest // replaces the store and PATH
func TestExecuteWithoutStoredExperiment(t *testing.T) {
	exp := executeExperiment(t, true, true)

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Delete(c); err != nil {
		t.Fatalf("store.Delete(experiment/exp) error = %v", err)
	}

	executeRunning = func(context.Context, *types.Experiment) error {
		t.Errorf("Execute() ran SCORCH, want it refused")

		return nil
	}

	if err := Execute(context.Background(), exp, 0); err == nil {
		t.Errorf("Execute(experiment not stored) = nil, want an error")
	}
}

// Execute runs SCORCH for the run with the app marked running, then deletes
// the taps that the run left, and stores the app as not running and without
// status. It reports a failed run, a tap it cannot delete, and a status it
// cannot store.
//
//nolint:paralleltest // replaces the store, PATH and minimega
func TestExecute(t *testing.T) {
	tests := map[string]struct {
		runErr    error
		tapErr    error
		deleteExp bool
		wantErr   bool
		wantCalls int
	}{
		"run":                    {wantCalls: 1},
		"run fails":              {runErr: errors.New("component failed"), wantErr: true, wantCalls: 1},
		"tap not deleted":        {tapErr: errors.New("no tap"), wantErr: true, wantCalls: 1},
		"experiment gone at end": {deleteExp: true, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			exp := executeExperiment(t, true, true)

			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			fake := &executeMM{err: tc.tapErr}
			mm.DefaultMM = fake //nolint:reassign // install test double

			executeRunning = func(ctx context.Context, e *types.Experiment) error {
				if got := MustRunID(ctx); got != 2 {
					t.Errorf("SCORCH ran with run ID %d, want 2", got)
				}

				if !e.Status.AppRunning()["scorch"] {
					t.Errorf("SCORCH ran without the app marked running")
				}

				if tc.deleteExp {
					c, _ := store.NewConfig("experiment/exp")

					return store.Delete(c)
				}

				e.Status.SetAppStatus("scorch", scorchmd.ScorchStatus{
					RunID: 2, Taps: map[string]*tap.Tap{"brk": {Name: "t1-tapbrk", VLAN: "EXP"}},
				})

				if err := e.WriteToStore(true); err != nil {
					t.Errorf("WriteToStore() error = %v", err)
				}

				return tc.runErr
			}

			err := Execute(context.Background(), exp, 2)
			if (err != nil) != tc.wantErr {
				t.Errorf("Execute() error = %v, want error %t", err, tc.wantErr)
			}

			if fake.calls != tc.wantCalls {
				t.Errorf("Execute() ran %d tap deletes, want %d", fake.calls, tc.wantCalls)
			}

			if tc.deleteExp {
				return
			}

			if err := exp.Reload(); err != nil {
				t.Fatalf("Reload() error = %v", err)
			}

			if diff := cmp.Diff(map[string]bool{"scorch": false}, exp.Status.AppRunning()); diff != "" {
				t.Errorf("stored app running mismatch (-want +got):\n%s", diff)
			}

			var status scorchmd.ScorchStatus
			if err := exp.Status.ParseAppStatus("scorch", &status); err == nil {
				t.Errorf("stored SCORCH status = %+v, want none", status)
			}
		})
	}
}
