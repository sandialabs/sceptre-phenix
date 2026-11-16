package app_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/util/pubsub"
)

// progressExperiment is an experiment whose scenario has the user apps on and off, off being disabled.
func progressExperiment(t *testing.T, on, off string) *types.Experiment {
	t.Helper()

	// for its store, which absorbs the app status writes
	runningStageExperiment(t)

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "progress-test"},
		Spec: map[string]any{
			"experimentName": "progress-test",
			"scenario": map[string]any{
				"apps": []map[string]any{{"name": on}, {"name": off, "disabled": true}},
			},
		},
	}

	exp, err := types.DecodeExperimentFromConfig(c)
	if err != nil {
		t.Fatalf("decoding experiment from config: %v", err)
	}

	return exp
}

// ApplyApps calls OnApp before each app it runs, in order, with the number of apps that ran before it, and
// PlannedApps lists the same apps: the default apps, then the enabled scenario apps, or only the apps a filter
// names, disabled or not. The running stage has no default apps.
func TestOnAppMatchesPlannedApps(t *testing.T) {
	const on, off = "test-progress-on", "test-progress-off"

	for _, name := range []string{on, off} {
		if err := registerUserApp(t, name, func() app.App { return &stubApp{name: name} }); err != nil {
			t.Fatalf("registering user app %s: %v", name, err)
		}
	}

	tests := []struct {
		name string
		opts []app.Option
		want []string
	}{
		{"cleanup", []app.Option{app.Stage(app.ActionCleanup)}, append(app.DefaultApps(), on)},
		{"post-start", []app.Option{app.Stage(app.ActionPostStart)}, append(app.DefaultApps(), on)},
		{"a filter of a disabled app", []app.Option{app.Stage(app.ActionCleanup), app.FilterApp(off)}, []string{off}},
		{"the running stage", []app.Option{app.Stage(app.ActionRunning), app.FilterApp(on)}, []string{on}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exp := progressExperiment(t, on, off)

			type call struct {
				Ran  int
				Name string
			}

			var got []call

			opts := append(slices.Clone(tc.opts), app.OnApp(func(ran int, name string) { got = append(got, call{ran, name}) }))

			if err := app.ApplyApps(context.Background(), exp, opts...); err != nil {
				t.Fatalf("ApplyApps(%s) error = %v", tc.name, err)
			}

			want := make([]call, 0, len(tc.want))
			for i, name := range tc.want {
				want = append(want, call{i, name})
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("ApplyApps(%s) OnApp calls mismatch (-want +got):\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(tc.want, app.PlannedApps(exp, tc.opts...)); diff != "" {
				t.Errorf("PlannedApps(%s) mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// ApplyApps names the lifecycle stage in each publication of a lifecycle run, not only of a trigger.
func TestApplyAppsPublishesTheStage(t *testing.T) {
	const name = "test-progress-stage"

	if err := registerUserApp(t, name, func() app.App { return &stubApp{name: name} }); err != nil {
		t.Fatalf("registering user app %s: %v", name, err)
	}

	exp := progressExperiment(t, name, "test-progress-stage-off")

	ch := pubsub.Subscribe("trigger-app")

	var (
		got        []app.TriggerPublication
		stop, done = make(chan struct{}), make(chan struct{})
	)

	go func() {
		defer close(done)

		for {
			select {
			case <-stop:
				return
			case pub := <-ch:
				if p, ok := pub.(app.TriggerPublication); ok && p.Experiment == "progress-test" {
					got = append(got, p)
				}
			}
		}
	}()

	// Publish returns once the subscriber reads, so every publication is read when ApplyApps returns
	err := app.ApplyApps(context.Background(), exp, app.Stage(app.ActionPostStart), app.FilterApp(name))

	pubsub.Unsubscribe("trigger-app", ch)
	close(stop)
	<-done

	if err != nil {
		t.Fatalf("ApplyApps(post-start) error = %v", err)
	}

	want := []app.TriggerPublication{
		{Experiment: "progress-test", App: name, Stage: "post-start", State: "start"},
		{Experiment: "progress-test", App: name, Stage: "post-start", State: "success"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ApplyApps(post-start) publications mismatch (-want +got):\n%s", diff)
	}
}
