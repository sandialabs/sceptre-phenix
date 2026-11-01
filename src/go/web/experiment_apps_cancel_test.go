package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/app"
	"phenix/util/pubsub"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// Canceling a triggered app announces it as canceled, not as a success, so the
// app is not shown as having run successfully.
func TestCancelTriggeredAppsPublishesCanceled(t *testing.T) {
	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/experiments/exp/trigger?apps=one,two", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "exp"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole,
		testRole("experiments/trigger", "delete", "exp")))

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CancelTriggeredExperimentApps).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	got := make([]app.TriggerPublication, 0, 2)
	for range 2 {
		pub, _ := (<-ch).(app.TriggerPublication)
		got = append(got, pub)
	}

	want := []app.TriggerPublication{
		{Experiment: "exp", Verb: "delete", App: "one", State: app.TriggerStateCanceled},
		{Experiment: "exp", Verb: "delete", App: "two", State: app.TriggerStateCanceled},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("publications (-want +got):\n%s", diff)
	}
}
