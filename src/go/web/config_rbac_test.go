package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
)

// topologyConfigs is a role that may act on Topology and Scenario configs only.
func topologyConfigs(verbs ...string) rbac.Role {
	roles := make([]rbac.Role, 0, len(verbs))
	for _, verb := range verbs {
		roles = append(roles, testRole("configs", verb, "Topology/*", "Scenario/*"))
	}

	return combinedRole(roles...)
}

// adminPolicies are the policies of a role that may do anything.
const adminPolicies = `[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]`

// configJSON is a valid User or Role config granting every permission.
func configJSON(kind, name string) string {
	spec := `{"roleName":"` + name + `","policies":` + adminPolicies + `}`
	if strings.EqualFold(kind, "User") {
		spec = `{"username":"` + name + `","first_name":"i","last_name":"i","password":"x","rbac":` + spec + `}`
	}

	return `{"apiVersion":"phenix.sandia.gov/v1","kind":"` + kind + `","metadata":{"name":"` + name + `"},"spec":` + spec + `}`
}

// renamedTopology is the demo topology under another name.
func renamedTopology(name string) string {
	return strings.Replace(revisionTestTopology, `"name":"demo"`, `"name":"`+name+`"`, 1)
}

// handlerStatus is the status a weberror handler answered with.
func handlerStatus(t *testing.T, w *httptest.ResponseRecorder, err error) int {
	t.Helper()

	if status, _ := webErrorStatus(t, err); status != 0 {
		return status
	}

	return w.Code
}

// wantNoConfig fails the test when the store holds the named config.
func wantNoConfig(t *testing.T, name string) {
	t.Helper()

	c, _ := store.NewConfig(name)
	if c != nil && store.Get(c) == nil {
		t.Errorf("%s was stored", name)
	}
}

// POST /configs checks configs create against the new config's Kind/name, so
// a role limited to topologies cannot create User or Role configs.
//
//nolint:paralleltest // replaces the store
func TestCreateConfigChecksKind(t *testing.T) {
	storetest.Use(t)

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"User config", configJSON("User", "intruder"), http.StatusForbidden},
		{"lowercase user kind", configJSON("user", "intruder"), http.StatusForbidden},
		{"Role config", configJSON("Role", "intruder"), http.StatusForbidden},
		{"topology", revisionTestTopology, http.StatusCreated},
	} {
		w := httptest.NewRecorder()
		req := roleRequest(http.MethodPost, "/api/v1/configs", tc.body, nil, topologyConfigs("create"))

		if got := handlerStatus(t, w, CreateConfig(w, req)); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	wantNoConfig(t, "user/intruder")
	wantNoConfig(t, "role/intruder")
}

// POST /workflow/configs/{branch} checks a new config's Kind/name as POST
// /configs does.
//
//nolint:paralleltest // replaces the store
func TestWorkflowUpsertConfigChecksKind(t *testing.T) {
	storetest.Use(t)

	vars := map[string]string{"branch": "main"}

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"User config", configJSON("User", "intruder"), http.StatusForbidden},
		{"Role config", configJSON("Role", "intruder"), http.StatusForbidden},
		{"topology", revisionTestTopology, http.StatusCreated},
	} {
		for _, dryRun := range []bool{true, false} {
			target := "/api/v1/workflow/configs/main"
			if dryRun {
				target += "?dryRun=true"
			}

			w := httptest.NewRecorder()
			req := roleRequest(http.MethodPost, target, tc.body, vars, topologyConfigs("create"))

			want := tc.want
			if dryRun && want == http.StatusCreated {
				want = http.StatusOK
			}

			if got := handlerStatus(t, w, WorkflowUpsertConfig(w, req)); got != want {
				t.Errorf("%s (dry run %t): status %d, want %d", tc.name, dryRun, got, want)
			}
		}
	}

	wantNoConfig(t, "user/intruder")
	wantNoConfig(t, "role/intruder")
}

// A PUT that renames a config, or changes its kind, needs configs create for
// the new Kind/name as well as configs update for the old one.
//
//nolint:paralleltest // replaces the store
func TestUpdateConfigChecksRename(t *testing.T) {
	vars := map[string]string{"kind": "topology", "name": "demo"}

	for _, tc := range []struct {
		name, body string
		role       rbac.Role
		want       int
	}{
		{"kind changed to User", configJSON("User", "intruder"), topologyConfigs("update", "create"), http.StatusForbidden},
		{"kind changed to Role", configJSON("Role", "demo"), topologyConfigs("update", "create"), http.StatusForbidden},
		{"renamed without create", renamedTopology("other"), topologyConfigs("update"), http.StatusForbidden},
		{"renamed", renamedTopology("other"), topologyConfigs("update", "create"), http.StatusNoContent},
		{"same name without create", revisionTestTopology, topologyConfigs("update"), http.StatusNoContent},
	} {
		useRevisionTestStore(t)

		w := httptest.NewRecorder()
		req := roleRequest(http.MethodPut, "/api/v1/configs/topology/demo", tc.body, vars, tc.role)

		if got := handlerStatus(t, w, UpdateConfig(w, req)); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}

		wantNoConfig(t, "user/intruder")
		wantNoConfig(t, "role/demo")
	}
}
