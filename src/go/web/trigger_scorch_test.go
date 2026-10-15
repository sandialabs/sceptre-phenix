package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"phenix/web/rbac"
)

func TestAppsIncludeScorch(t *testing.T) {
	t.Parallel()

	for filter, want := range map[string]bool{
		"":                true, // every app
		" ":               true,
		"ntp":             false,
		scorchApp:         true,
		"ntp,scorch":      true,
		"ntp, Scorch ":    true,
		"scorch-reporter": false,
	} {
		if got := appsIncludeScorch(filter); got != want {
			t.Errorf("appsIncludeScorch(%q) = %t, want %t", filter, got, want)
		}
	}
}

// The app trigger endpoints start or cancel SCORCH, when their apps name it or
// name no app, only for a role that may also post or delete scorch on the
// experiment, as the SCORCH pipeline routes need. A request let through starts
// work in the background, so only refused ones are made.
func TestTriggerEndpointsRequireScorchPermission(t *testing.T) {
	t.Parallel()

	const exp = "exp-a"

	var (
		triggerCreate = testRole("experiments/trigger", "create", exp)
		triggerDelete = testRole("experiments/trigger", "delete", exp)
		scorch        = func(verb string, names ...string) rbac.Role { return testRole(scorchApp, verb, names...) }
	)

	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		method  string
		apps    string
		role    rbac.Role
	}{
		"trigger SCORCH without scorch post": {
			TriggerExperimentApps, http.MethodPost, "ntp,scorch", combinedRole(triggerCreate, scorch("get", exp)),
		},
		"trigger every app without scorch post": {
			TriggerExperimentApps, http.MethodPost, "", combinedRole(triggerCreate, scorch("get", exp)),
		},
		"trigger SCORCH with scorch post elsewhere": {
			TriggerExperimentApps, http.MethodPost, "scorch", combinedRole(triggerCreate, scorch("post", "exp-b")),
		},
		"trigger SCORCH without trigger": {
			TriggerExperimentApps, http.MethodPost, "scorch", scorch("post", exp),
		},
		"cancel SCORCH without scorch delete": {
			CancelTriggeredExperimentApps, http.MethodDelete, "scorch", combinedRole(triggerDelete, scorch("post", exp)),
		},
		"cancel every app without scorch delete": {
			CancelTriggeredExperimentApps, http.MethodDelete, "", combinedRole(triggerDelete, scorch("post", exp)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			target := "/experiments/" + exp + "/trigger?apps=" + url.QueryEscape(tc.apps)
			tc.handler(rec, roleRequest(tc.method, target, "", map[string]string{"name": exp}, tc.role))

			if rec.Code != http.StatusForbidden {
				t.Errorf("status %d, want 403", rec.Code)
			}
		})
	}
}
