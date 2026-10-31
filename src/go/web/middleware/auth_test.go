package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthOnlySkipsExactPublicPaths(t *testing.T) {
	tests := map[string]bool{
		"/api/v1/login":                       true,
		"/api/v1/signup":                      true,
		"/login":                              true,
		"/api/v1/experiments/login":           false,
		"/api/v1/users/signup":                false,
		"/api/v1/configs/topology/login":      false,
		"/api/v1/workflow/apply/login":        false,
		"/api/v1/experiments/exp/files/login": false,
		"/api/v1/login/extra":                 false,
	}

	for path, public := range tests {
		t.Run(path, func(t *testing.T) {
			var reached bool

			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			handler := Auth("test-signing-key", "")(next)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if reached != public {
				t.Fatalf("handler reached = %t without a token, want %t (status %d)", reached, public, rec.Code)
			}

			if !public && rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", rec.Code)
			}
		})
	}
}
