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

// stubApp is a minimal user app whose running stage returns a configurable
// error.
type stubApp struct {
	name string
	err  error
}

func (a *stubApp) Init(...app.Option) error { return nil }

func (a *stubApp) Name() string { return a.name }

func (a *stubApp) Configure(context.Context, *types.Experiment) error { return nil }

func (a *stubApp) PreStart(context.Context, *types.Experiment) error { return nil }

func (a *stubApp) PostStart(context.Context, *types.Experiment) error { return nil }

func (a *stubApp) Running(context.Context, *types.Experiment) error { return a.err }

func (a *stubApp) Cleanup(context.Context, *types.Experiment) error { return nil }

// runningStageExperiment builds a minimal experiment whose scenario contains
// the single given app, backed by a store mock so WriteToStore/Reload calls
// made during the running stage are absorbed.
func runningStageExperiment(t *testing.T, appName string) *types.Experiment {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).Return(errors.New("store unavailable")).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "running-stage-test"},
		Spec: map[string]any{
			"experimentName": "running-stage-test",
			"scenario": map[string]any{
				"apps": []map[string]any{{"name": appName}},
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
