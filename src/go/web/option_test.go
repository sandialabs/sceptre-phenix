package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"phenix/util/common"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// getOptionsTestResponse serves GET /api/v1/options through
// [weberror.ErrorHandler], with role and a test user in the request context,
// and returns the recorded response.
func getOptionsTestResponse(t *testing.T, role rbac.Role) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/options", nil)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(GetOptions).ServeHTTP(rec, req.WithContext(ctx))

	return rec
}

func TestGetOptionsReportsWorkflowSettings(t *testing.T) {
	original := common.InjectsBase
	t.Cleanup(func() { common.InjectsBase = original }) //nolint:reassign // restore test fixture

	common.InjectsBase = "/srv/phenix/injects" //nolint:reassign // monkey patching for test

	// Mirrors the global-admin role that the unix socket's NoAuth middleware
	// installs.
	rec := getOptionsTestResponse(t, workflowTestRole(true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != mimeJSON {
		t.Errorf("Content-Type = %q, want %q", got, mimeJSON)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	want := map[string]any{
		"bridge-mode":      string(common.BridgeMode),
		"deploy-mode":      string(common.DeployMode),
		"use-gre-mesh":     common.UseGREMesh,
		"base-dir.injects": "/srv/phenix/injects",
		"workflow-dry-run": true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("GET /options body = %v, want %v", got, want)
	}
}

func TestGetOptionsForbidden(t *testing.T) {
	// A non-nil Spec is required: Role.Allowed dereferences Spec.Policies.
	rec := getOptionsTestResponse(t, workflowTestRole(false))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "workflow-dry-run") || !strings.Contains(body, "listing options not allowed for test-user") {
		t.Errorf("body = %s, want only the forbidden error", body)
	}
}
