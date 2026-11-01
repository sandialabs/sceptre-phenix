package scorch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// roleFor may take the given verbs on the given resources of experiment exp.
func roleFor(resourceVerbs ...string) *rbac.Role {
	role := &rbac.Role{Spec: &v1.RoleSpec{}}

	for i := 0; i < len(resourceVerbs); i += 2 {
		role.Spec.Policies = append(role.Spec.Policies, &v1.PolicySpec{
			Resources: []string{resourceVerbs[i]}, ResourceNames: []string{"exp"}, Verbs: []string{resourceVerbs[i+1]},
		})
	}

	return role
}

// serveScorch serves a request through the SCORCH terminal and component
// output handlers as a user with role, or with no role when role is nil.
func serveScorch(method, path string, role *rbac.Role) *httptest.ResponseRecorder {
	router := mux.NewRouter()

	for route, handler := range map[string]weberror.ErrorHandler{
		"/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}":    GetComponentOutput,
		"/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}/ws": StreamComponentOutput,
		"/experiments/{name}/scorch/terminals":                                GetTerminals,
		"/experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}":     ConnectTerminal,
		"/experiments/{name}/scorch/terminals/{pid}/ws/{id}":                  StreamTerminal,
		"/experiments/{name}/scorch/terminals/{pid}/exit/{id}":                ExitTerminal,
	} {
		router.Handle(route, handler)
	}

	req := httptest.NewRequest(method, path, nil)

	if role != nil {
		ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, *role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")
		req = req.WithContext(ctx)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// Only a role that may get an experiment sees its SCORCH component output and
// terminals, and only a role that may trigger it exits a terminal.
func TestViewingOutputNeedsExperimentGet(t *testing.T) {
	requests := map[string]struct{ method, path string }{
		"component output":        {http.MethodGet, "/experiments/exp/scorch/components/0/0/start/cc"},
		"component output stream": {http.MethodGet, "/experiments/exp/scorch/components/0/0/start/cc/ws"},
		"terminals":               {http.MethodGet, "/experiments/exp/scorch/terminals"},
		"component terminal":      {http.MethodGet, "/experiments/exp/scorch/terminals/0/0/start/cc"},
		"terminal stream":         {http.MethodGet, "/experiments/exp/scorch/terminals/1/ws/id"},
		"terminal exit":           {http.MethodPost, "/experiments/exp/scorch/terminals/1/exit/id"},
	}

	other := roleFor("experiments", "get")
	other.Spec.Policies[0].ResourceNames = []string{"other"}

	denied := map[string]*rbac.Role{
		"no role":                        nil,
		"a role for other experiments":   other,
		"a role that may only list them": roleFor("experiments", "list"),
	}

	for name, req := range requests {
		for who, role := range denied {
			if rec := serveScorch(req.method, req.path, role); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s (%s) as %s = %d, want 403", req.method, req.path, name, who, rec.Code)
			}
		}
	}

	exit := "/experiments/exp/scorch/terminals/1/exit/id"
	if rec := serveScorch(http.MethodPost, exit, roleFor("experiments", "get")); rec.Code != http.StatusForbidden {
		t.Errorf("POST %s as a viewer = %d, want 403", exit, rec.Code)
	}
}
