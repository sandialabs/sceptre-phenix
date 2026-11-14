package experiment_test

import (
	"context"
	"crypto/rand"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
)

// noopApp is a minimal app.App implementation used to register scenario apps
// for Trigger tests without pulling in a real app's side effects. It counts
// the calls of its running stage in running.
type noopApp struct {
	name    string
	running *atomic.Int32
}

func (a *noopApp) Init(...app.Option) error                           { return nil }
func (a *noopApp) Name() string                                       { return a.name }
func (a *noopApp) Configure(context.Context, *types.Experiment) error { return nil }
func (a *noopApp) PreStart(context.Context, *types.Experiment) error  { return nil }
func (a *noopApp) PostStart(context.Context, *types.Experiment) error { return nil }
func (a *noopApp) Cleanup(context.Context, *types.Experiment) error   { return nil }

func (a *noopApp) Running(context.Context, *types.Experiment) error {
	a.running.Add(1)

	return nil
}

// List returns the experiment configs that the store holds.
func TestList(t *testing.T) { //nolint:paralleltest // replaces the store
	configs := store.Configs(
		[]store.Config{
			{
				Version: "phenix.sandia.gov/v1",
				Kind:    "Experiment",
				Metadata: store.ConfigMetadata{
					Name: "test-experiment",
				},
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().List(gomock.Eq("Experiment")).Return(configs, nil)

	storetest.Use(t, m)

	c, err := experiment.List()
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	if len(c) != 1 {
		t.Log("expecting 1 config")
		t.FailNow()
	}
}

func triggerTestConfig(name string, appNames ...string) store.Config {
	appConfigs := make([]map[string]any, 0, len(appNames))
	for _, n := range appNames {
		appConfigs = append(appConfigs, map[string]any{"name": n})
	}

	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"experimentName": name,
			"scenario": map[string]any{
				"apps": appConfigs,
			},
		},
		Status: map[string]any{
			"startTime": "2024-01-01T00:00:00Z",
		},
	}
}

// Trigger refuses an app that is not part of the experiment.
func TestTriggerRejectsAppNotInExperiment(t *testing.T) { //nolint:paralleltest // replaces the store
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := triggerTestConfig("trigger-missing-app", "known-app")

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()

	storetest.Use(t, m)

	err := experiment.Trigger(context.Background(), "trigger-missing-app", app.ActionConfigure, "unknown-app")
	if err == nil {
		t.Fatal("expected error triggering an app not part of the experiment")
	}

	if !strings.Contains(err.Error(), "not part of experiment") {
		t.Fatalf("expected error to mention app not part of experiment, got: %v", err)
	}
}

// Trigger refuses the running stage for a default app, which has no running stage.
func TestTriggerRejectsInapplicableStageForDefaultApp(t *testing.T) { //nolint:paralleltest // replaces the store
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := triggerTestConfig("trigger-default-app-running")

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()

	storetest.Use(t, m)

	defaultApps := app.DefaultApps()
	if len(defaultApps) == 0 {
		t.Fatal("app.DefaultApps() is empty, want the default apps that the app package registers")
	}

	err := experiment.Trigger(
		context.Background(),
		"trigger-default-app-running",
		app.ActionRunning,
		defaultApps[0],
	)
	if err == nil {
		t.Fatal("expected error triggering running stage for a default app")
	}

	if !strings.Contains(err.Error(), "not applicable") {
		t.Fatalf("expected error to mention stage not applicable, got: %v", err)
	}
}

// Trigger for all apps refuses a stage that no app of the experiment supports.
func TestTriggerRejectsAllAppsWhenNoneSupportStage(t *testing.T) { //nolint:paralleltest // replaces the store
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// No scenario apps, so the experiment only has default apps, none of which
	// support the running stage.
	cfg := triggerTestConfig("trigger-all-apps-running-unsupported")

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()

	storetest.Use(t, m)

	err := experiment.Trigger(
		context.Background(),
		"trigger-all-apps-running-unsupported",
		app.ActionRunning,
	)
	if err == nil {
		t.Fatal("expected error triggering running stage for all apps when none support it")
	}

	if !strings.Contains(err.Error(), "not applicable") {
		t.Fatalf("expected error to mention stage not applicable, got: %v", err)
	}
}

// Trigger for all apps runs the stage of each app that supports it, when one app does.
func TestTriggerAllowsAllAppsWhenAtLeastOneSupportsStage(t *testing.T) { //nolint:paralleltest // replaces the store
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// the registry keeps each name, so each run (-count) registers a new one
	var running atomic.Int32

	name := "test-all-apps-running-" + strings.ToLower(rand.Text())
	if err := app.RegisterUserApp(name, func() app.App {
		return &noopApp{name: name, running: &running}
	}); err != nil {
		t.Fatalf("registering user app: %v", err)
	}

	cfg := triggerTestConfig("trigger-all-apps-running-supported", name)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()
	m.EXPECT().Update(gomock.Any()).Return(nil).AnyTimes()

	storetest.Use(t, m)

	err := experiment.Trigger(
		context.Background(),
		"trigger-all-apps-running-supported",
		app.ActionRunning,
	)
	if err != nil {
		t.Fatalf("expected no error triggering running stage when a scenario app supports it, got: %v", err)
	}

	if got := running.Load(); got != 1 {
		t.Errorf("Trigger(running) ran the running stage of %s %d times, want 1", name, got)
	}
}

// Trigger refuses a stage that is not a lifecycle stage, and the running stage
// of an experiment that is not running.
func TestTriggerRejectsBadStageOrStoppedExperiment(t *testing.T) { //nolint:paralleltest // replaces the store
	stopped := triggerTestConfig("trigger-stopped")
	stopped.Status = nil

	tests := []struct {
		name  string
		cfg   store.Config
		stage app.Action
		want  string // the error
	}{
		{
			name:  "a stage that does not exist",
			cfg:   triggerTestConfig("trigger-stopped"),
			stage: app.Action("bogus"),
			want:  `invalid app lifecycle stage "bogus"`,
		},
		{name: "the running stage of a stopped experiment", cfg: stopped, stage: app.ActionRunning, want: "experiment is not running"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			m := store.NewMockStore(ctrl)
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				*c = tc.cfg
				return nil
			}).AnyTimes()

			storetest.Use(t, m)

			err := experiment.Trigger(context.Background(), "trigger-stopped", tc.stage)
			if err == nil || err.Error() != tc.want {
				t.Errorf("Trigger(trigger-stopped, %s) error = %v, want %q", tc.stage, err, tc.want)
			}
		})
	}
}
