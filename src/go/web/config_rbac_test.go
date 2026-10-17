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

// isStored reports whether the store holds the named config.
func isStored(name string) bool {
	c, _ := store.NewConfig(name)

	return c != nil && store.Get(c) == nil
}

// POST /configs and the workflow upsert check configs create against the new
// config's Kind/name, so a role limited to topologies cannot create User or Role configs.
//
//nolint:paralleltest // replaces the store
func TestCreateConfigChecksKind(t *testing.T) {
	vars := map[string]string{"branch": "main"}

	for _, route := range []struct {
		name, target string
		handler      func(http.ResponseWriter, *http.Request) error
		created      int
	}{
		{"POST /configs", "/api/v1/configs", CreateConfig, http.StatusCreated},
		{"workflow upsert dry run", "/api/v1/workflow/configs/main?dryRun=true", WorkflowUpsertConfig, http.StatusOK},
		{"workflow upsert", "/api/v1/workflow/configs/main", WorkflowUpsertConfig, http.StatusCreated},
	} {
		t.Run(route.name, func(t *testing.T) {
			storetest.Use(t)

			for _, tc := range []struct {
				name, body string
				want       int
			}{
				{"User config", configJSON("User", "intruder"), http.StatusForbidden},
				{"lowercase user kind", configJSON("user", "intruder"), http.StatusForbidden},
				{"Role config", configJSON("Role", "intruder"), http.StatusForbidden},
				{"topology", revisionTestTopology, route.created},
			} {
				w := httptest.NewRecorder()
				req := roleRequest(http.MethodPost, route.target, tc.body, vars, topologyConfigs("create"))

				if got := handlerStatus(t, w, route.handler(w, req)); got != tc.want {
					t.Errorf("%s of a %s = %d, want %d", route.name, tc.name, got, tc.want)
				}
			}

			for _, name := range []string{"user/intruder", "role/intruder"} {
				if isStored(name) {
					t.Errorf("%s by a topology-only role stored %s, want it refused", route.name, name)
				}
			}
		})
	}
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
		t.Run(tc.name, func(t *testing.T) {
			useRevisionTestStore(t)

			w := httptest.NewRecorder()
			req := roleRequest(http.MethodPut, "/api/v1/configs/topology/demo", tc.body, vars, tc.role)

			if got := handlerStatus(t, w, UpdateConfig(w, req)); got != tc.want {
				t.Errorf("UpdateConfig(PUT topology/demo, %s) = %d, want %d", tc.name, got, tc.want)
			}

			for _, name := range []string{"user/intruder", "role/demo"} {
				if isStored(name) {
					t.Errorf("UpdateConfig(PUT topology/demo, %s) stored %s, want it refused", tc.name, name)
				}
			}
		})
	}
}
