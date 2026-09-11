package experiment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/store"
	"phenix/types"
)

// noopApp is a minimal app.App implementation used to register scenario apps
// for Trigger tests without pulling in a real app's side effects.
type noopApp struct{ name string }

func (a *noopApp) Init(...app.Option) error                           { return nil }
func (a *noopApp) Name() string                                       { return a.name }
func (a *noopApp) Configure(context.Context, *types.Experiment) error { return nil }
func (a *noopApp) PreStart(context.Context, *types.Experiment) error  { return nil }
func (a *noopApp) PostStart(context.Context, *types.Experiment) error { return nil }
func (a *noopApp) Running(context.Context, *types.Experiment) error   { return nil }
func (a *noopApp) Cleanup(context.Context, *types.Experiment) error   { return nil }

func TestList(t *testing.T) {
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

	store.DefaultStore = m //nolint:reassign // monkey patching for test

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

func TestTriggerRejectsAppNotInExperiment(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := triggerTestConfig("trigger-missing-app", "known-app")

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	err := experiment.Trigger(context.Background(), "trigger-missing-app", app.ActionConfigure, "unknown-app")
	if err == nil {
		t.Fatal("expected error triggering an app not part of the experiment")
	}

	if !strings.Contains(err.Error(), "not part of experiment") {
		t.Fatalf("expected error to mention app not part of experiment, got: %v", err)
	}
}

func TestTriggerRejectsInapplicableStageForDefaultApp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := triggerTestConfig("trigger-default-app-running")

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	defaultApps := app.DefaultApps()
	if len(defaultApps) == 0 {
		t.Skip("no default apps registered")
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

func TestTriggerRejectsAllAppsWhenNoneSupportStage(t *testing.T) {
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

	store.DefaultStore = m //nolint:reassign // monkey patching for test

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

func TestTriggerAllowsAllAppsWhenAtLeastOneSupportsStage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	name := "test-all-apps-running-supported"
	if err := app.RegisterUserApp(name, func() app.App {
		return &noopApp{name: name}
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

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	err := experiment.Trigger(
		context.Background(),
		"trigger-all-apps-running-supported",
		app.ActionRunning,
	)
	if err != nil {
		t.Fatalf("expected no error triggering running stage when a scenario app supports it, got: %v", err)
	}
}
