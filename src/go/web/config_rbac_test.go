package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// useTestStore points the default store at a throw-away BoltDB file.
func useTestStore(t *testing.T) {
	t.Helper()

	previous := store.DefaultStore

	endpoint := "bolt://" + filepath.Join(t.TempDir(), "phenix.bdb")
	if err := store.Init(store.Endpoint(endpoint)); err != nil {
		t.Fatalf("initializing test store: %v", err)
	}

	t.Cleanup(func() {
		store.DefaultStore = previous //nolint:reassign // restoring test store
	})
}

// roleRequest returns a request made by a role with the given policies.
func roleRequest(method, target, body string, vars map[string]string, policies ...*v1.PolicySpec) *http.Request {
	role := rbac.Role{Spec: &v1.RoleSpec{Policies: policies}}
	ctx := context.WithValue(context.Background(), middleware.ContextKeyRole, role)
	req := httptest.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", mimeJSON)

	return mux.SetURLVars(req, vars)
}

// status returns the HTTP status a weberror handler produced.
func status(t *testing.T, rec *httptest.ResponseRecorder, err error) int {
	t.Helper()

	var webErr *weberror.WebError
	if errors.As(err, &webErr) {
		return webErr.Status
	}

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return rec.Code
}

// topologyConfigPolicy mirrors the Builder role's config access.
func topologyConfigPolicy(verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     []string{"configs"},
		ResourceNames: []string{"Topology/*", "Scenario/*"},
		Verbs:         verbs,
	}
}

func configJSON(kind, name string) string {
	return `{"apiVersion":"phenix.sandia.gov/v1","kind":"` + kind + `","metadata":{"name":"` + name + `"},"spec":{}}`
}

// TestCreateConfigChecksKind verifies configs create is checked against the
// new config's kind and name, so a role limited to topologies cannot create
// User or Role configs.
func TestCreateConfigChecksKind(t *testing.T) {
	useTestStore(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "user config", body: configJSON("User", "intruder"), want: http.StatusForbidden},
		{name: "lowercase user kind", body: configJSON("user", "intruder"), want: http.StatusForbidden},
		{name: "role config", body: configJSON("Role", "intruder"), want: http.StatusForbidden},
		// An allowed kind passes the check and fails schema validation instead.
		{name: "topology config", body: configJSON("Topology", "topo"), want: http.StatusBadRequest},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		req := roleRequest(http.MethodPost, "/configs", test.body, nil, topologyConfigPolicy("create"))

		if got := status(t, rec, CreateConfig(rec, req)); got != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, got, test.want)
		}
	}

	if _, err := rbac.GetUser("intruder"); err == nil {
		t.Fatal("forbidden request created a user")
	}
}

// TestWorkflowUpsertConfigChecksKind verifies the workflow config route checks
// a new config's kind and name like POST /configs does.
func TestWorkflowUpsertConfigChecksKind(t *testing.T) {
	useTestStore(t)

	vars := map[string]string{"branch": "main"}

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "user config", body: configJSON("User", "intruder"), want: http.StatusForbidden},
		{name: "role config", body: configJSON("Role", "intruder"), want: http.StatusForbidden},
		// An allowed kind passes the check and fails schema validation instead.
		{name: "topology config", body: configJSON("Topology", "topo"), want: http.StatusBadRequest},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		req := roleRequest(http.MethodPost, "/workflow/configs/main", test.body, vars, topologyConfigPolicy("create"))

		if got := status(t, rec, WorkflowUpsertConfig(rec, req)); got != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, got, test.want)
		}
	}

	if _, err := rbac.GetUser("intruder"); err == nil {
		t.Fatal("forbidden request created a user")
	}
}

// TestUpdateConfigChecksRename verifies an update that renames a config, or
// changes its kind, needs permission to create the new config.
func TestUpdateConfigChecksRename(t *testing.T) {
	useTestStore(t)

	vars := map[string]string{"kind": "topology", "name": "topo"}
	policies := []*v1.PolicySpec{topologyConfigPolicy("update", "create")}

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "change kind to User", body: configJSON("User", "intruder"), want: http.StatusForbidden},
		{name: "change kind to Role", body: configJSON("Role", "topo"), want: http.StatusForbidden},
		// Same name passes the check and fails because the topology is missing.
		{name: "same name", body: configJSON("Topology", "topo"), want: http.StatusBadRequest},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		req := roleRequest(http.MethodPut, "/configs/topology/topo", test.body, vars, policies...)

		if got := status(t, rec, UpdateConfig(rec, req)); got != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, got, test.want)
		}
	}
}

// TestEnsureServiceRolesCreatesOnce verifies existing installs get the Builder
// and Scorch roles, and that a deleted role is not created again.
func TestEnsureServiceRolesCreatesOnce(t *testing.T) {
	useTestStore(t)

	if err := ensureServiceRoles(); err != nil {
		t.Fatalf("creating service roles: %v", err)
	}

	for _, name := range []string{"builder", "scorch-viewer", "scorch-admin"} {
		if _, err := rbac.RoleFromConfig(name); err != nil {
			t.Fatalf("role %s was not created: %v", name, err)
		}
	}

	if _, err := rbac.RoleFromConfig("experiment-user"); err == nil {
		t.Fatal("other default roles were created")
	}

	builder, err := store.NewConfig("role/builder")
	if err != nil {
		t.Fatalf("building config name: %v", err)
	}

	if err := store.Delete(builder); err != nil {
		t.Fatalf("deleting builder role: %v", err)
	}

	if err := ensureServiceRoles(); err != nil {
		t.Fatalf("rerunning service roles: %v", err)
	}

	if _, err := rbac.RoleFromConfig("builder"); err == nil {
		t.Fatal("deleted builder role was created again")
	}
}

// TestUpdateExperimentFromBuilderChecksName verifies Builder updates are
// scoped to the experiment being updated, and need experiments create when
// the experiment does not exist yet.
func TestUpdateExperimentFromBuilderChecksName(t *testing.T) {
	useTestStore(t)

	var (
		builderPut = &v1.PolicySpec{Resources: []string{"builder"}, ResourceNames: nil, Verbs: []string{"put"}}
		updateExpA = &v1.PolicySpec{
			Resources:     []string{"experiments"},
			ResourceNames: []string{"exp-a"},
			Verbs:         []string{"update"},
		}
		create = &v1.PolicySpec{Resources: []string{"experiments"}, ResourceNames: nil, Verbs: []string{"create"}}
	)

	tests := []struct {
		name     string
		exp      string
		policies []*v1.PolicySpec
		want     int
	}{
		{
			name:     "other experiment",
			exp:      "exp-b",
			policies: []*v1.PolicySpec{builderPut, updateExpA},
			want:     http.StatusForbidden,
		},
		{
			name:     "new experiment without create",
			exp:      "exp-a",
			policies: []*v1.PolicySpec{builderPut, updateExpA},
			want:     http.StatusForbidden,
		},
		// With create, the request passes the checks and fails because the
		// topology is missing.
		{
			name:     "new experiment with create",
			exp:      "exp-a",
			policies: []*v1.PolicySpec{builderPut, updateExpA, create},
			want:     http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		body := `{"name":"` + test.exp + `","topology":{},"builderXML":"<x/>"}`
		req := roleRequest(http.MethodPut, "/experiments/builder", body, nil, test.policies...)

		if got := status(t, rec, UpdateExperimentFromBuilder(rec, req)); got != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, got, test.want)
		}
	}
}
