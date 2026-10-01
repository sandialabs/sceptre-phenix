package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// upsertConfigTestRequest builds a POST /workflow/configs/{branch} request
// whose YAML body repeats a metadata key built from ${PHENIX_TEST_SECRET}, so
// parsing it fails with an error that quotes the substituted value.
func upsertConfigTestRequest(t *testing.T, role rbac.Role) *http.Request {
	t.Helper()

	body := strings.Join([]string{
		"apiVersion: phenix.sandia.gov/v1",
		"kind: Topology",
		"metadata:",
		"  name: probe",
		`  "${PHENIX_TEST_SECRET}": a`,
		`  "${PHENIX_TEST_SECRET}": b`,
		"spec:",
		"  nodes: []",
		"",
	}, "\n")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/configs/main", strings.NewReader(body))
	req.Header.Set("Content-Type", mimeYAML)
	req = mux.SetURLVars(req, map[string]string{"branch": "main"})

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

func configsRole(verbs ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{"configs"},
					ResourceNames: []string{"*"},
					Verbs:         verbs,
				},
			},
		},
	}
}

// A caller who may not create or update configs is refused before the body
// is parsed, so no environment variable reaches the response.
func TestWorkflowUpsertConfigChecksPermissionBeforeParsing(t *testing.T) {
	const secret = "s3cret-value-for-test"

	t.Setenv("PHENIX_TEST_SECRET", secret)

	tests := []struct {
		name      string
		role      rbac.Role
		forbidden bool
	}{
		{name: "no policies", role: rbac.Role{Spec: &v1.RoleSpec{}}, forbidden: true},
		{name: "read only", role: configsRole("get", "list"), forbidden: true},
		{name: "create", role: configsRole("create")},
		{name: "update", role: configsRole("update")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(rec, upsertConfigTestRequest(t, tt.role))

			if tt.forbidden {
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
				}

				if strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("response quotes the environment variable: %s", rec.Body.String())
				}

				return
			}

			// Callers who may write configs still get the parse error, which
			// they could read through the configs endpoints anyway.
			if rec.Code == http.StatusForbidden {
				t.Fatalf("status = %d, want the parse error: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
