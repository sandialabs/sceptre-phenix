package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"phenix/version"
)

// Every response names the server's build, as the served openapi.yml does.
func TestVersion(t *testing.T) {
	handler := Version(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

	if got, want := w.Header().Get("X-Phenix-Version"), version.API(); got != want {
		t.Errorf("GET /api/v1/experiments: X-Phenix-Version = %q, want %q", got, want)
	}
}
