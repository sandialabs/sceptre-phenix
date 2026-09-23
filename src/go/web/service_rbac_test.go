package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

func TestAppsIncludeScorch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		want   bool
	}{
		{filter: "", want: false},
		{filter: "ntp", want: false},
		{filter: "scorch", want: true},
		{filter: "ntp,scorch", want: true},
		{filter: "ntp, Scorch ", want: true},
		{filter: "scorch-reporter", want: false},
	}

	for _, test := range tests {
		if got := appsIncludeScorch(test.filter); got != test.want {
			t.Errorf("appsIncludeScorch(%q): got %t, want %t", test.filter, got, test.want)
		}
	}
}

// triggerRequest returns a trigger endpoint request for experiment exp-a made
// by a role with the given policies.
func triggerRequest(method, apps string, policies ...*v1.PolicySpec) *http.Request {
	role := rbac.Role{Spec: &v1.RoleSpec{Policies: policies}}
	ctx := context.WithValue(context.Background(), middleware.ContextKeyRole, role)
	target := "/experiments/exp-a/trigger?apps=" + url.QueryEscape(apps)
	req := httptest.NewRequestWithContext(ctx, method, target, nil)

	return mux.SetURLVars(req, map[string]string{"name": "exp-a"})
}

func triggerPolicy(verb string) *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     []string{"experiments/trigger"},
		ResourceNames: []string{"exp-a"},
		Verbs:         []string{verb},
	}
}

func scorchPolicy(verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{"scorch"}, ResourceNames: nil, Verbs: verbs}
}

// TestTriggerEndpointsRequireScorchPermission verifies the generic app trigger
// endpoints cannot start or cancel Scorch runs without the Scorch service
// permissions the Scorch pipeline routes require. Allowed requests start
// background work that uses the store, so only rejected requests are tested.
func TestTriggerEndpointsRequireScorchPermission(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		method   string
		apps     string
		policies []*v1.PolicySpec
		want     int
	}{
		{
			name:     "trigger Scorch without scorch post",
			handler:  TriggerExperimentApps,
			method:   http.MethodPost,
			apps:     "ntp,scorch",
			policies: []*v1.PolicySpec{triggerPolicy("create"), scorchPolicy("get")},
			want:     http.StatusForbidden,
		},
		{
			name:     "scorch post without trigger permission",
			handler:  TriggerExperimentApps,
			method:   http.MethodPost,
			apps:     "scorch",
			policies: []*v1.PolicySpec{scorchPolicy("post")},
			want:     http.StatusForbidden,
		},
		{
			name:     "cancel Scorch without scorch delete",
			handler:  CancelTriggeredExperimentApps,
			method:   http.MethodDelete,
			apps:     "scorch",
			policies: []*v1.PolicySpec{triggerPolicy("delete"), scorchPolicy("get", "post")},
			want:     http.StatusForbidden,
		},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		test.handler(rec, triggerRequest(test.method, test.apps, test.policies...))

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, rec.Code, test.want)
		}
	}
}

func TestSaveBuilderTopologyHeaders(t *testing.T) {
	t.Parallel()

	form := url.Values{
		"filename": {`topology".xml`},
		"xml":      {"<mxGraphModel/>"},
	}
	req := httptest.NewRequest(http.MethodPost, "/builder/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	SaveBuilderTopology(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusOK)
	}

	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="topology\".xml"`; got != want {
		t.Errorf("unexpected Content-Disposition: got %q, want %q", got, want)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("unexpected X-Content-Type-Options: %q", got)
	}

	if got := rec.Body.String(); got != "<mxGraphModel/>" {
		t.Errorf("unexpected body: %q", got)
	}
}
