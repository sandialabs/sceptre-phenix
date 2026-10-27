package web

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

// TestGetSchemaStatus asserts GET /schemas/{kind}/{version} answers 404 for a
// kind or version that has no schema.
func TestGetSchemaStatus(t *testing.T) {
	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
		Resources:     []string{"schemas"},
		ResourceNames: []string{"*"},
		Verbs:         []string{"get"},
	}}}}

	tests := map[string]struct {
		kind, version string
		want          int
	}{
		"a known kind and version": {"topology", "v1", http.StatusOK},
		"an unknown kind":          {"nonesuch", "v1", http.StatusNotFound},
		"an unknown version":       {"topology", "v99", http.StatusNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/"+tc.kind+"/"+tc.version, nil)
			req = mux.SetURLVars(req, map[string]string{"kind": tc.kind, "version": tc.version})
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(GetSchema).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("GET /schemas/%s/%s status = %d, want %d: %s", tc.kind, tc.version, rec.Code, tc.want, rec.Body)
			}
		})
	}
}
