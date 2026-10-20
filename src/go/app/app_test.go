package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"phenix/app"
	"phenix/store"
	"phenix/types"
)

// stubApp is a minimal user app that records lifecycle calls and whose running
// stage returns a configurable error.
type stubApp struct {
	name         string
	err          error
	configureErr error
	calls        *[]app.Action
}

func (a *stubApp) Init(...app.Option) error { return nil }

func (a *stubApp) Name() string { return a.name }

func (a *stubApp) record(action app.Action) {
	if a.calls != nil {
		*a.calls = append(*a.calls, action)
	}
}

func (a *stubApp) Configure(context.Context, *types.Experiment) error {
	a.record(app.ActionConfigure)
	return a.configureErr
}

func (a *stubApp) PreStart(context.Context, *types.Experiment) error {
	a.record(app.ActionPreStart)
	return nil
}

func (a *stubApp) PostStart(context.Context, *types.Experiment) error {
	a.record(app.ActionPostStart)
	return nil
}

func (a *stubApp) Running(context.Context, *types.Experiment) error {
	a.record(app.ActionRunning)
	return a.err
}

func (a *stubApp) Cleanup(context.Context, *types.Experiment) error {
	a.record(app.ActionCleanup)
	return nil
}

// runningStageExperiment builds a minimal experiment whose scenario contains
// the given apps, backed by a store mock so WriteToStore/Reload calls are
// absorbed.
func runningStageExperiment(t *testing.T, appNames ...string) *types.Experiment {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).Return(errors.New("store unavailable")).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	appConfigs := make([]map[string]any, 0, len(appNames))
	for _, name := range appNames {
		appConfigs = append(appConfigs, map[string]any{"name": name})
	}

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "running-stage-test"},
		Spec: map[string]any{
			"experimentName": "running-stage-test",
			"scenario": map[string]any{
				"apps": appConfigs,
			},
		},
	}

	exp, err := types.DecodeExperimentFromConfig(c)
	if err != nil {
		t.Fatalf("decoding experiment from config: %v", err)
	}

	return exp
}

// TestApplyAppsRunningStageErrorPropagates is a regression test for user app
// errors (e.g. failed SOH checks) being silently swallowed during the running
// stage, which caused scorch and `phenix experiment trigger-running` to report
// success even when an app failed.
func TestApplyAppsRunningStageErrorPropagates(t *testing.T) {
	name := "test-running-fails"

	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{name: name, err: errors.New("checks failed")}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionRunning),
		app.FilterApp(name),
	)
	if err == nil {
		t.Fatal("expected running-stage app error to propagate, got nil")
	}
}

// TestApplyAppsRunningStageSuccess verifies a successful running stage still
// returns nil.
func TestApplyAppsRunningStageSuccess(t *testing.T) {
	name := "test-running-succeeds"

	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{name: name, err: nil}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionRunning),
		app.FilterApp(name),
	)
	if err != nil {
		t.Fatalf("expected no error from successful running stage, got: %v", err)
	}
}

func TestApplyAppsFiltersNonRunningLifecycleStage(t *testing.T) {
	var selectedCalls, skippedCalls []app.Action

	selected := "test-cleanup-selected"
	if err := app.RegisterUserApp(selected, func() app.App {
		return &stubApp{name: selected, calls: &selectedCalls}
	}); err != nil {
		t.Fatalf("registering selected user app: %v", err)
	}

	skipped := "test-cleanup-skipped"
	if err := app.RegisterUserApp(skipped, func() app.App {
		return &stubApp{name: skipped, calls: &skippedCalls}
	}); err != nil {
		t.Fatalf("registering skipped user app: %v", err)
	}

	exp := runningStageExperiment(t, selected, skipped)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionCleanup),
		app.FilterApp(selected),
	)
	if err != nil {
		t.Fatalf("expected no error from filtered cleanup stage, got: %v", err)
	}

	if len(selectedCalls) != 1 || selectedCalls[0] != app.ActionCleanup {
		t.Fatalf("expected selected app cleanup call, got: %v", selectedCalls)
	}

	if len(skippedCalls) != 0 {
		t.Fatalf("expected skipped app to receive no calls, got: %v", skippedCalls)
	}
}

func TestApplyAppsQueuesTriggeredAppCleanup(t *testing.T) {
	var calls []app.Action

	name := "test-triggered-cleanup"
	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{name: name, calls: &calls}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)
	exp.Spec.Scenario().App(name).SetDisabled(true)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionConfigure),
		app.FilterApp(name),
		app.Trigger(),
	)
	if err != nil {
		t.Fatalf("expected no error from triggered configure stage, got: %v", err)
	}

	if !exp.Status.AppCleanup()[name] {
		t.Fatal("expected triggered app cleanup to be queued")
	}

	exp.Status.ResetAppStatus()
	if !exp.Status.AppCleanup()[name] {
		t.Fatal("expected cleanup queue to persist across app status reset")
	}

	err = app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionCleanup),
	)
	if err != nil {
		t.Fatalf("expected no error from cleanup stage, got: %v", err)
	}

	if len(calls) != 2 || calls[0] != app.ActionConfigure || calls[1] != app.ActionCleanup {
		t.Fatalf("expected configure and cleanup calls, got: %v", calls)
	}

	if exp.Status.AppCleanup()[name] {
		t.Fatal("expected completed app cleanup to be removed from queue")
	}
}

func TestApplyAppsQueuesCleanupBeforeTriggeredAppFailure(t *testing.T) {
	name := "test-triggered-cleanup-failure"
	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{
			name:         name,
			configureErr: errors.New("configure failed"),
		}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)
	exp.Spec.Scenario().App(name).SetDisabled(true)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionConfigure),
		app.FilterApp(name),
		app.Trigger(),
	)
	if err == nil {
		t.Fatal("expected triggered configure stage to fail")
	}

	if !exp.Status.AppCleanup()[name] {
		t.Fatal("expected cleanup to remain queued after triggered app failure")
	}
}

func TestApplyAppsTriggeredPreStartPreservesAppStatus(t *testing.T) {
	name := "test-triggered-pre-start"
	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{name: name}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)
	exp.Status.SetAppStatus("existing-app", "status")
	exp.Status.SetAppFrequency("existing-app", "1m")
	exp.Status.SetAppRunning("existing-app", true)

	err := app.ApplyApps(
		context.Background(),
		exp,
		app.Stage(app.ActionPreStart),
		app.FilterApp(name),
		app.Trigger(),
	)
	if err != nil {
		t.Fatalf("expected triggered pre-start stage to succeed, got: %v", err)
	}

	if exp.Status.AppStatus()["existing-app"] != "status" {
		t.Fatal("expected triggered pre-start to preserve existing app status")
	}
	if exp.Status.AppFrequency()["existing-app"] != "1m" {
		t.Fatal("expected triggered pre-start to preserve existing app frequency")
	}
	if !exp.Status.AppRunning()["existing-app"] {
		t.Fatal("expected triggered pre-start to preserve existing app running state")
	}
}

func TestExperimentAppsIncludesDefaultAndScenarioApps(t *testing.T) {
	name := "test-experiment-apps-scenario"
	if err := app.RegisterUserApp(name, func() app.App {
		return &stubApp{name: name}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	exp := runningStageExperiment(t, name)

	names := app.ExperimentApps(exp)

	seen := make(map[string]bool)
	for _, n := range names {
		seen[n] = true
	}

	if !seen[name] {
		t.Fatalf("expected experiment apps to include scenario app %s, got: %v", name, names)
	}

	for _, d := range app.DefaultApps() {
		if !seen[d] {
			t.Fatalf("expected experiment apps to include default app %s, got: %v", d, names)
		}
	}
}

func TestStageApplicableExcludesRunningForDefaultApps(t *testing.T) {
	for _, d := range app.DefaultApps() {
		if app.StageApplicable(app.ActionRunning, d) {
			t.Fatalf("expected running stage to not be applicable for default app %s", d)
		}

		if !app.StageApplicable(app.ActionConfigure, d) {
			t.Fatalf("expected configure stage to be applicable for default app %s", d)
		}
	}
}

func TestStageApplicableAllowsRunningForUserApps(t *testing.T) {
	if !app.StageApplicable(app.ActionRunning, "some-user-app") {
		t.Fatal("expected running stage to be applicable for a non-default app")
	}
}

// statusApp records each run in its own app status, as SOH does.
type statusApp struct{ stubApp }

func (a *statusApp) Running(_ context.Context, exp *types.Experiment) error {
	return exp.UpdateAppStatus(a.name, func(status map[string]any) {
		status["ran"] = true
	})
}

// TestPeriodicallyRunAppsIsolatesWorkers: periodic workers run concurrently, so
// each must keep to its own in-memory status. Run with -race.
func TestPeriodicallyRunAppsIsolatesWorkers(t *testing.T) {
	names := []string{"test-periodic-a", "test-periodic-b"}

	appConfigs := make([]map[string]any, 0, len(names))

	for _, name := range names {
		if err := app.RegisterUserApp(name, func() app.App {
			return &statusApp{stubApp{name: name}}
		}); err != nil {
			t.Fatalf("registering user app: %v", err)
		}

		appConfigs = append(appConfigs, map[string]any{"name": name, "runPeriodically": "1ms"})
	}

	var (
		mu     sync.Mutex
		stored = store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: store.ConfigMetadata{Name: "periodic-test"},
			Spec: map[string]any{
				"experimentName": "periodic-test",
				"scenario":       map[string]any{"apps": appConfigs},
			},
		}
	)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		mu.Lock()
		defer mu.Unlock()

		*c = stored

		return nil
	}).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		mu.Lock()
		defer mu.Unlock()

		stored = *c

		return nil
	}).AnyTimes()

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore store

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	exp, err := types.DecodeExperimentFromConfig(stored)
	if err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	var (
		wg          sync.WaitGroup
		ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	)

	defer cancel()

	if err := app.PeriodicallyRunApps(ctx, &wg, exp); err != nil {
		t.Fatalf("PeriodicallyRunApps: %v", err)
	}

	wg.Wait()

	final, err := types.DecodeExperimentFromConfig(stored)
	if err != nil {
		t.Fatalf("decoding stored experiment: %v", err)
	}

	for _, name := range names {
		var status map[string]any

		if err := final.Status.ParseAppStatus(name, &status); err != nil || status["ran"] != true {
			t.Fatalf("%s status = %v (%v), want its run recorded", name, status, err)
		}
	}
}
