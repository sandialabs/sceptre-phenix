package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/app"
	"phenix/util/polltest"
	"phenix/util/pubsub"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// receiveTrigger returns the next message on ch, and fails the test if no
// message comes within a few seconds.
func receiveTrigger(t *testing.T, ch chan any) any {
	t.Helper()

	select {
	case msg := <-ch:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no trigger-app publication within 5s")

		return nil
	}
}

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
		pub, _ := receiveTrigger(t, ch).(app.TriggerPublication)
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

// experimentAppsCancelRequest is a DELETE of the triggered apps of exp as a
// user with role.
func experimentAppsCancelRequest(t *testing.T, apps string, role rbac.Role) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/experiments/exp/trigger?apps="+apps, nil)
	req = mux.SetURLVars(req, map[string]string{"name": "exp"})

	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))
}

// Canceling triggered apps cancels the runs registered for each app named,
// and only those, and is audited.
func TestCancelTriggeredAppsCancelsRuns(t *testing.T) {
	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	one, cancelOne := context.WithCancel(t.Context())
	other, cancelOther := context.WithCancel(t.Context())

	addCanceler("exp/one", cancelOne)
	kept := addCanceler("exp/other", cancelOther)

	t.Cleanup(func() { dropCanceler("exp/other", kept) })

	events := captureAudit(t)

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CancelTriggeredExperimentApps).ServeHTTP(
		rec, experimentAppsCancelRequest(t, "one", testRole("experiments/trigger", "delete", "exp")),
	)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	receiveTrigger(t, ch)

	polltest.Within(t, "canceling app one's run", func() { <-one.Done() })

	if other.Err() != nil {
		t.Errorf("app other's run = %v, want it left running", other.Err())
	}

	commonMu.Lock()
	_, registered := cancelers["exp/one"]
	commonMu.Unlock()

	if registered {
		t.Error(`cancelers["exp/one"] still registered, want it removed`)
	}

	wantAudit(t, events(), "app-trigger-cancel", "success", map[string]any{
		"phenix.experiment.name": "exp", "phenix.app.names": "one",
	})
}

// A role that may not cancel the apps is refused, audited, and cancels and
// announces nothing.
func TestCancelTriggeredAppsForbidden(t *testing.T) {
	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	run, cancel := context.WithCancel(t.Context())
	c := addCanceler("exp/one", cancel)

	t.Cleanup(func() { dropCanceler("exp/one", c) })

	events := captureAudit(t)

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CancelTriggeredExperimentApps).ServeHTTP(
		rec, experimentAppsCancelRequest(t, "one", testRole("experiments/trigger", "create", "exp")),
	)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	wantDenied(t, events(), "app-trigger-cancel")

	if run.Err() != nil {
		t.Errorf("app one's run after a refused cancel = %v, want it left running", run.Err())
	}

	select {
	case pub := <-ch:
		t.Errorf("a refused cancel published %v, want nothing", pub)
	default:
	}
}

// Without the apps query parameter, a cancel cancels every app that a trigger
// runs for the experiment, and not the apps of another experiment or the
// experiment's periodic runs.
func TestCancelTriggeredAppsWithoutAppsCancelsEveryApp(t *testing.T) {
	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	one, cancelOne := context.WithCancel(t.Context())
	two, cancelTwo := context.WithCancel(t.Context())
	other, cancelOther := context.WithCancel(t.Context())
	periodic, cancelPeriodic := context.WithCancel(t.Context())

	addCanceler("every/one", cancelOne)
	addCanceler("every/two", cancelTwo)
	keptOther := addCanceler("everyother/one", cancelOther)
	keptPeriodic := addCanceler("every", cancelPeriodic)

	t.Cleanup(func() {
		dropCanceler("everyother/one", keptOther)
		dropCanceler("every", keptPeriodic)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/experiments/every/trigger", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "every"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole,
		testRole("experiments/trigger", "delete", "every")))

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CancelTriggeredExperimentApps).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	got := make([]app.TriggerPublication, 0, 2)
	for range 2 {
		pub, _ := receiveTrigger(t, ch).(app.TriggerPublication)
		got = append(got, pub)
	}

	want := []app.TriggerPublication{
		{Experiment: "every", Verb: "delete", App: "one", State: app.TriggerStateCanceled},
		{Experiment: "every", Verb: "delete", App: "two", State: app.TriggerStateCanceled},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("publications (-want +got):\n%s", diff)
	}

	polltest.Within(t, "canceling the runs of apps one and two", func() {
		<-one.Done()
		<-two.Done()
	})

	if other.Err() != nil || periodic.Err() != nil {
		t.Errorf("other experiment's app = %v, periodic run = %v; want both left running", other.Err(), periodic.Err())
	}
}

// An empty apps query parameter names no app, and is refused.
func TestCancelTriggeredAppsWithEmptyApps(t *testing.T) {
	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CancelTriggeredExperimentApps).ServeHTTP(
		rec, experimentAppsCancelRequest(t, "", testRole("experiments/trigger", "delete", "exp")),
	)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE /experiments/exp/trigger?apps= status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
