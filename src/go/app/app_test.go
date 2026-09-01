package app_test

import (
	"context"
	"errors"
	"testing"

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
