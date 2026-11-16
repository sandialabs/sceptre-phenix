package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/app"
	"phenix/util/pubsub"
)

// The stage query parameter picks the lifecycle stage that a trigger runs, running when it is absent, and each
// trigger publication names it.
//
//nolint:paralleltest // replaces the store and subscribes to the trigger topic
func TestTriggerExperimentAppsStage(t *testing.T) {
	tests := []struct {
		name    string
		started string
		query   string
		want    []app.TriggerPublication // the trigger's start and end, around those of the app's run
	}{
		{
			name: "running without a stage", query: "?apps=soh",
			want: []app.TriggerPublication{
				{Experiment: "exp", App: "soh", Stage: "running", State: "start"},
				{Experiment: "exp", App: "soh", Stage: "running", State: "error"},
			},
		},
		{
			name: "a stage other than running of a running experiment", started: "2024-01-01T00:00:00Z",
			query: "?apps=ntp&stage=configure", want: stagePublications("configure"),
		},
		{name: "cleanup of a stopped experiment", query: "?apps=ntp&stage=cleanup", want: stagePublications("cleanup")},
		{name: "a stage by its shorthand", query: "?apps=ntp&stage=clean", want: stagePublications("cleanup")},
		{name: "a stage in another letter case", query: "?apps=ntp&stage=%20Config", want: stagePublications("configure")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", tc.started))

			ch := pubsub.Subscribe("trigger-app")
			t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

			req := roleRequest(http.MethodPost, "/"+tc.query, "", map[string]string{"name": "exp"},
				testRole("experiments/trigger", "create", "exp"))

			if got, rec := serveHandler(TriggerExperimentApps, req); got.Status != http.StatusNoContent {
				t.Fatalf("POST trigger%s = %+v, want 204: %s", tc.query, got, rec.Body)
			}

			got := make([]app.TriggerPublication, 0, len(tc.want))

			for range tc.want {
				pub, _ := receiveTrigger(t, ch).(app.TriggerPublication)
				got = append(got, pub)
			}

			ignoreError := cmpopts.IgnoreFields(app.TriggerPublication{}, "Error")
			if diff := cmp.Diff(tc.want, got, ignoreError); diff != "" {
				t.Errorf("POST trigger%s publications mismatch (-want +got):\n%s", tc.query, diff)
			}
		})
	}
}

// stagePublications are the publications of a trigger of the ntp app's stage that succeeds.
func stagePublications(stage string) []app.TriggerPublication {
	return []app.TriggerPublication{
		{Experiment: "exp", App: "ntp", Stage: stage, State: "start"},
		{Experiment: "exp", App: "ntp", Stage: stage, State: "start"},
		{Experiment: "exp", App: "ntp", Stage: stage, State: "success"},
		{Experiment: "exp", App: "ntp", Stage: stage, State: "success"},
	}
}

// A trigger with a stage refuses, before it runs, a stage that is not valid or that the experiment or an app cannot
// run.
//
//nolint:paralleltest // replaces the store
func TestTriggerExperimentAppsStageErrors(t *testing.T) {
	tests := []struct {
		name    string
		started string
		query   string
		want    int
	}{
		{name: "invalid stage", query: "?apps=ntp&stage=bogus", want: http.StatusBadRequest},
		{name: "invalid stage without apps", query: "?stage=start", want: http.StatusBadRequest},
		{name: "running stage of a stopped experiment", query: "?apps=soh&stage=running", want: http.StatusConflict},
		{
			name: "running stage of a default app", started: "2024-01-01T00:00:00Z",
			query: "?apps=ntp&stage=running", want: http.StatusBadRequest,
		},
		{name: "app the experiment does not have", query: "?apps=soh&stage=configure", want: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", tc.started))

			req := roleRequest(http.MethodPost, "/"+tc.query, "", map[string]string{"name": "exp"},
				testRole("experiments/trigger", "create", "exp"))

			got, rec := serveHandler(TriggerExperimentApps, req)
			if got.Status != tc.want {
				t.Errorf("POST trigger%s = %+v, want %d: %s", tc.query, got, tc.want, rec.Body)
			}

			if got.Message == "" {
				t.Errorf("POST trigger%s = %+v, want a message", tc.query, got)
			}
		})
	}
}
