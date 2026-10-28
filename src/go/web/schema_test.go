package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"phenix/web/middleware"
	"phenix/web/weberror"
)

// schemaTestRequest serves a GET request for the schema handler with the
// given mux vars, as a user whose role allows everything, and returns the
// recorded response.
func schemaTestRequest(t *testing.T, handler weberror.ErrorHandler, vars map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	req = mux.SetURLVars(req, vars)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, workflowTestRole(true)))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestGetSchemaSpecServesEachVersion proves GET /schemas/{version} serves the
// whole schema file of each version, holding the kinds stored at it, and
// rejects a version that has no file.
func TestGetSchemaSpecServesEachVersion(t *testing.T) {
	tests := []struct {
		version  string
		wantKind string
	}{
		{"v0", "Workflow"},
		{"v1", "Topology"},
		{"v1", "Experiment"},
		{"v2", "Scenario"},
	}

	for _, tc := range tests {
		t.Run(tc.version+" "+tc.wantKind, func(t *testing.T) {
			rec := schemaTestRequest(t, GetSchemaSpec, map[string]string{"version": tc.version})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
			}

			var spec struct {
				Components struct {
					Schemas map[string]any `json:"schemas"`
				} `json:"components"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
				t.Fatalf("decoding spec: %v", err)
			}

			if _, ok := spec.Components.Schemas[tc.wantKind]; !ok {
				t.Errorf("%s spec has no %s schema", tc.version, tc.wantKind)
			}
		})
	}

	if rec := schemaTestRequest(t, GetSchemaSpec, map[string]string{"version": "v9"}); rec.Code != http.StatusBadRequest {
		t.Errorf("v9 status = %d, want 400", rec.Code)
	}
}

// TestGetSchemaServesKindAtItsVersion proves GET /schemas/{kind}/{version}
// serves a kind's schema from the file of the version it is stored at, and
// answers 404 for a version whose file does not hold that kind.
func TestGetSchemaServesKindAtItsVersion(t *testing.T) {
	tests := []struct {
		kind, version string
		wantStatus    int
	}{
		{"topology", "v1", http.StatusOK},
		{"experiment", "v1", http.StatusOK},
		{"image", "v1", http.StatusOK},
		{"user", "v1", http.StatusOK},
		{"role", "v1", http.StatusOK},
		{"scenario", "v2", http.StatusOK},
		{"workflow", "v0", http.StatusOK},
		{"topology", "v2", http.StatusNotFound},
		{"experiment", "v2", http.StatusNotFound},
		{"scenario", "v1", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.kind+" "+tc.version, func(t *testing.T) {
			rec := schemaTestRequest(t, GetSchema, map[string]string{"kind": tc.kind, "version": tc.version})
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
			}
		})
	}
}
