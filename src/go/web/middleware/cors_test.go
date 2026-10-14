package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A preflight allows the auth and precondition headers, and every response
// lets cross-origin scripts read the revision and freshness headers.
func TestAllowCORSHeaders(t *testing.T) {
	handler := AllowCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a preflight reached the handler")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/configs", nil))

	for _, h := range []string{"X-Phenix-Auth-Token", "If-Match", "If-None-Match"} {
		if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), h) {
			t.Errorf("Access-Control-Allow-Headers lacks %s", h)
		}
	}

	for _, h := range []string{"Date", "ETag", "X-Phenix-Seq", "X-Phenix-Revision", "X-Phenix-Observed-At", "X-Phenix-Stale"} {
		if !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), h) {
			t.Errorf("Access-Control-Expose-Headers lacks %s", h)
		}
	}
}
