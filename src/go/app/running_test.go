package app_test

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
	"phenix/util/notes"
	"phenix/util/plog/plogtest"
)

// runningWait is how long a test waits for a periodic run before it fails.
const runningWait = 30 * time.Second

// funcApp is a user app whose running stage calls running.
type funcApp struct {
	stubApp

	running func(context.Context, *types.Experiment) error
}

func (a *funcApp) Running(ctx context.Context, exp *types.Experiment) error {
	return a.running(ctx, exp)
}

// registerFuncApp registers user app name, whose running stage calls running,
// until the test ends.
func registerFuncApp(t *testing.T, name string, running func(context.Context, *types.Experiment) error) {
	t.Helper()

	err := registerUserApp(t, name, func() app.App {
		return &funcApp{stubApp: stubApp{name: name}, running: running}
	})
	if err != nil {
		t.Fatalf("RegisterUserApp(%s) error = %v", name, err)
	}
}

// runningStoredExperiment stores a started experiment named running-test with
// the given scenario apps, and returns it as read back from the store.
func runningStoredExperiment(t *testing.T, apps ...map[string]any) *types.Experiment {
	t.Helper()

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "running-test"},
		Spec: map[string]any{
			"experimentName": "running-test",
			"scenario":       map[string]any{"apps": apps},
		},
		Status: map[string]any{"startTime": "2024-01-01T00:00:00Z"},
	})
	if err != nil {
		t.Fatalf("store.Create(running-test) error = %v", err)
	}

	return runningStored(t)
}

// runningStored returns experiment running-test as the store holds it.
func runningStored(t *testing.T) *types.Experiment {
	t.Helper()

	exp := types.NewExperiment(store.ConfigMetadata{Name: "running-test"})
	if err := exp.Reload(); err != nil {
		t.Fatalf("Reload() of running-test error = %v", err)
	}

	return exp
}

// runningSetFlag sets the running flag of app in the stored status, as another
// run of app does.
func runningSetFlag(t *testing.T, app string, running bool) {
	t.Helper()

	err := runningStored(t).UpdateStatus(func(s ifaces.ExperimentStatus) { s.SetAppRunning(app, running) })
	if err != nil {
		t.Fatalf("UpdateStatus(%s running %t) error = %v", app, running, err)
	}
}

// The running stage skips an app whose running flag another run set in the
// store, though the caller's copy of the experiment is older, and leaves the
// flag to that run.
func TestApplyAppsRunningStageSkipsAStoredRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	var ran bool

	registerFuncApp(t, "test-held", func(context.Context, *types.Experiment) error {
		ran = true

		return nil
	})

	exp := runningStoredExperiment(t, map[string]any{"name": "test-held"})
	runningSetFlag(t, "test-held", true)

	ctx := notes.Context(context.Background(), false)

	if err := app.ApplyApps(ctx, exp, app.Stage(app.ActionRunning), app.FilterApp("test-held")); err != nil {
		t.Fatalf("ApplyApps(running) error = %v", err)
	}

	if ran {
		t.Error("ApplyApps(running) ran the app that another run had flagged as running")
	}

	if !runningStored(t).Status.AppRunning()["test-held"] {
		t.Error("ApplyApps(running) cleared the running flag of the other run")
	}

	want := []string{"app test-held is currently already executing its running stage -- skipping"}
	if diff := cmp.Diff(want, notes.Info(ctx, true)); diff != "" {
		t.Errorf("ApplyApps(running) info notes mismatch (-want +got):\n%s", diff)
	}
}

// An app that reports its running stage already in progress does not have its
// running flag cleared: the flag belongs to the run in progress.
func TestApplyAppsRunningStageKeepsTheFlagOfARunInProgress(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	registerFuncApp(t, "test-busy", func(context.Context, *types.Experiment) error {
		return fmt.Errorf("checks of running-test: %w", app.ErrAlreadyRunning)
	})

	exp := runningStoredExperiment(t, map[string]any{"name": "test-busy"})
	ctx := notes.Context(context.Background(), false)

	if err := app.ApplyApps(ctx, exp, app.Stage(app.ActionRunning), app.FilterApp("test-busy")); err != nil {
		t.Fatalf("ApplyApps(running) of an app already in progress error = %v, want nil", err)
	}

	if !runningStored(t).Status.AppRunning()["test-busy"] {
		t.Error("ApplyApps(running) cleared the running flag of the run in progress")
	}

	want := []string{"app test-busy is currently already executing its running stage -- skipping"}
	if diff := cmp.Diff(want, notes.Info(ctx, true)); diff != "" {
		t.Errorf("ApplyApps(running) info notes mismatch (-want +got):\n%s", diff)
	}
}

// The running stage sets the stored running flag while the app runs and clears
// it after, and keeps what other writers stored in the meantime.
func TestApplyAppsRunningStageFlagsTheRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	var flagged bool

	registerFuncApp(t, "test-flagged", func(_ context.Context, exp *types.Experiment) error {
		flagged = runningStored(t).Status.AppRunning()["test-flagged"]

		return exp.UpdateAppStatus("test-flagged", func(status map[string]any) { status["ran"] = true })
	})

	exp := runningStoredExperiment(t, map[string]any{"name": "test-flagged"})

	other := runningStored(t)
	other.Status.SetAppStatus("other", map[string]any{"kept": true})

	if err := other.WriteToStore(true); err != nil {
		t.Fatalf("WriteToStore(true) error = %v", err)
	}

	if err := app.ApplyApps(context.Background(), exp, app.Stage(app.ActionRunning), app.FilterApp("test-flagged")); err != nil {
		t.Fatalf("ApplyApps(running) error = %v", err)
	}

	if !flagged {
		t.Error("running flag in the store while the app ran = false, want true")
	}

	stored := runningStored(t)

	if stored.Status.AppRunning()["test-flagged"] {
		t.Error("running flag in the store after the run = true, want false")
	}

	got := map[string]any{}

	for _, name := range []string{"other", "test-flagged"} {
		var status map[string]any

		_ = stored.Status.ParseAppStatus(name, &status)
		got[name] = status
	}

	want := map[string]any{"other": map[string]any{"kept": true}, "test-flagged": map[string]any{"ran": true}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("stored app status after the run mismatch (-want +got):\n%s", diff)
	}
}

// overlayStore is a store that, while held, answers each Get with the running
// flag of app set, as when a manual trigger of app runs. It counts the Gets.
type overlayStore struct {
	store.Store

	app string

	mu      sync.Mutex
	held    bool
	gets    int
	waiters map[int][]chan struct{}
}

func (s *overlayStore) Get(c *store.Config) error {
	err := s.Store.Get(c)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil && s.held {
		running, _ := c.Status["appRunningStageStatus"].(map[string]any)
		running = maps.Clone(running)

		if running == nil {
			running = map[string]any{}
		}

		running[s.app] = true
		c.Status["appRunningStageStatus"] = running
	}

	s.gets++

	for _, ch := range s.waiters[s.gets] {
		close(ch)
	}

	delete(s.waiters, s.gets)

	return err //nolint:wrapcheck // test double
}

// hold starts or ends the manual trigger.
func (s *overlayStore) hold(held bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.held = held
}

// isHeld tells if the manual trigger runs.
func (s *overlayStore) isHeld() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.held
}

// afterGets returns a channel that closes once n more Gets have been answered.
func (s *overlayStore) afterGets(n int) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan struct{})

	if s.waiters == nil {
		s.waiters = map[int][]chan struct{}{}
	}

	s.waiters[s.gets+n] = append(s.waiters[s.gets+n], ch)

	return ch
}

// waitFor fails the test unless ch closes within runningWait.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(runningWait):
		t.Fatalf("waited %v for %s", runningWait, what)
	}
}

// startPeriodic runs the periodic apps of exp until the test ends, and stops
// them when the returned function is called.
func startPeriodic(t *testing.T, exp *types.Experiment) func() {
	t.Helper()

	var (
		wg          sync.WaitGroup
		ctx, cancel = context.WithCancel(context.Background())
		stopped     = make(chan struct{})
	)

	app.PeriodicallyRunApps(ctx, &wg, exp)

	stop := sync.OnceFunc(func() {
		cancel()

		go func() {
			wg.Wait()
			close(stopped)
		}()

		waitFor(t, stopped, "the periodic workers to stop")
	})

	t.Cleanup(stop)

	return stop
}

// A periodic tick that finds the app run by a manual trigger, which the store
// knows of but the worker's copy does not, is skipped, and the next ticks
// still come: the app runs again once the manual run ends.
func TestPeriodicallyRunAppsSkipsATickAndRunsAgain(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	st := &overlayStore{Store: storetest.NewBolt(t), app: "test-periodic"}
	storetest.Use(t, st)

	var (
		mu        sync.Mutex
		runs      int
		overlap   bool
		ranAgain  = make(chan struct{})
		firstDone = make(chan struct{})
	)

	registerFuncApp(t, "test-periodic", func(context.Context, *types.Experiment) error {
		mu.Lock()
		defer mu.Unlock()

		runs++

		switch {
		case st.isHeld():
			overlap = true
		case runs == 1:
			// a manual trigger starts as this run ends
			st.hold(true)
			close(firstDone)
		case runs == 2:
			close(ranAgain)
		}

		return nil
	})

	exp := runningStoredExperiment(t, map[string]any{"name": "test-periodic", "runPeriodically": "1ms"})
	stop := startPeriodic(t, exp)

	waitFor(t, firstDone, "the first periodic run")

	// The release of the first run, then two ticks: the second tick shows that
	// the skipped tick re-armed the timer.
	waitFor(t, st.afterGets(3), "two periodic ticks during the manual run")

	st.hold(false)
	runningSetFlag(t, "test-periodic", false)

	waitFor(t, ranAgain, "a periodic run after the manual run")
	stop()

	mu.Lock()
	defer mu.Unlock()

	if overlap {
		t.Error("PeriodicallyRunApps() ran the app while a manual trigger ran it")
	}
}

// A periodic run that the app refuses as already in progress leaves the
// running flag to the run in progress, and the next ticks still come.
func TestPeriodicallyRunAppsSkipsARunInProgress(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	st := &overlayStore{Store: storetest.NewBolt(t), app: "test-periodic"}
	storetest.Use(t, st)

	var (
		mu          sync.Mutex
		runs        int
		released    bool
		early       bool
		refused     = make(chan struct{})
		ranAgain    = make(chan struct{})
		flagRefused bool
	)

	registerFuncApp(t, "test-periodic", func(context.Context, *types.Experiment) error {
		mu.Lock()
		defer mu.Unlock()

		runs++

		if runs == 1 {
			close(refused)

			return fmt.Errorf("checks: %w", app.ErrAlreadyRunning)
		}

		if !released {
			early = true
		}

		if runs == 2 {
			close(ranAgain)
		}

		return nil
	})

	exp := runningStoredExperiment(t, map[string]any{"name": "test-periodic", "runPeriodically": "1ms"})
	stop := startPeriodic(t, exp)

	waitFor(t, refused, "the refused periodic run")
	waitFor(t, st.afterGets(2), "two periodic ticks after the refused run")

	mu.Lock()
	flagRefused = runningStored(t).Status.AppRunning()["test-periodic"]
	released = true
	mu.Unlock()

	runningSetFlag(t, "test-periodic", false)

	waitFor(t, ranAgain, "a periodic run after the run in progress")
	stop()

	mu.Lock()
	defer mu.Unlock()

	if !flagRefused {
		t.Error("running flag after a refused periodic run = false, want the flag of the run in progress kept")
	}

	if early {
		t.Error("PeriodicallyRunApps() ran the app again before the run in progress ended")
	}
}

// Each periodic worker runs the app with its own copy of the experiment, and a
// stopped worker clears the frequency and running flag of its app.
func TestPeriodicallyRunAppsGivesEachWorkerItsOwnCopy(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	var (
		mu     sync.Mutex
		copies = map[string]*types.Experiment{}
		done   = map[string]chan struct{}{"test-a": make(chan struct{}), "test-b": make(chan struct{})}
	)

	for name := range done {
		registerFuncApp(t, name, func(_ context.Context, exp *types.Experiment) error {
			mu.Lock()
			defer mu.Unlock()

			if _, ok := copies[name]; !ok {
				copies[name] = exp
				close(done[name])
			}

			return nil
		})
	}

	exp := runningStoredExperiment(t,
		map[string]any{"name": "test-a", "runPeriodically": "1ms"},
		map[string]any{"name": "test-b", "runPeriodically": "1ms"},
	)
	stop := startPeriodic(t, exp)

	for name, ch := range done {
		waitFor(t, ch, "a periodic run of "+name)
	}

	stop()

	mu.Lock()
	defer mu.Unlock()

	if copies["test-a"] == copies["test-b"] || copies["test-a"] == exp || copies["test-b"] == exp {
		t.Errorf("PeriodicallyRunApps() experiments of test-a, test-b and the caller = %p, %p, %p; want three copies",
			copies["test-a"], copies["test-b"], exp)
	}

	stored := runningStored(t)

	for name := range done {
		if f, r := stored.Status.AppFrequency()[name], stored.Status.AppRunning()[name]; f != "" || r {
			t.Errorf("stored frequency, running of %s after the stop = %q, %t; want empty, false", name, f, r)
		}
	}
}

// A periodic worker whose store fails logs each failed write and keeps the
// running flag in its own copy, so the app still runs on each tick.
func TestPeriodicallyRunAppsWithoutTheStore(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	logs := plogtest.Capture(t)

	var (
		mu    sync.Mutex
		runs  int
		twice = make(chan struct{})
	)

	registerFuncApp(t, "test-unstored", func(_ context.Context, exp *types.Experiment) error {
		mu.Lock()
		defer mu.Unlock()

		if !exp.Status.AppRunning()["test-unstored"] {
			t.Error("running flag in the worker's copy while the app runs = false, want true")
		}

		if runs++; runs == 2 {
			close(twice)
		}

		return nil
	})

	exp := types.NewExperiment(store.ConfigMetadata{Name: "unstored"})
	exp.Spec.SetScenario(&v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "test-unstored", RunPeriodicallyF: "1ms"}}})

	stop := startPeriodic(t, exp)

	waitFor(t, twice, "two periodic runs without the store")
	stop()

	if got := logs.Records(t, plogtest.Message("[✗] error updating store with experiment")); len(got) < 4 {
		t.Errorf("logged %d store errors for the start, two runs and the stop, want 4 or more", len(got))
	}
}

// ScheduledApps names the apps that PeriodicallyRunApps runs: not a default
// app, and not one without a schedule or with one that is not a duration.
func TestScheduledApps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scenario *v2.ScenarioSpec
		want     []string
	}{
		{name: "no scenario", want: nil},
		{
			name: "apps with and without schedules",
			scenario: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
				{NameF: "soh", RunPeriodicallyF: "5m"},
				{NameF: "scorch"},
				{NameF: "ntp", RunPeriodicallyF: "1m"},
				{NameF: "broken", RunPeriodicallyF: "often"},
				{NameF: "other", RunPeriodicallyF: "30s"},
			}},
			want: []string{"soh", "other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := types.NewExperiment(store.ConfigMetadata{Name: "scheduled"})
			if tt.scenario != nil {
				exp.Spec.SetScenario(tt.scenario)
			}

			if diff := cmp.Diff(tt.want, app.ScheduledApps(exp)); diff != "" {
				t.Errorf("ScheduledApps(%s) mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}
