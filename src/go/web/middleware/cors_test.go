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
		t.Error("OPTIONS /api/v1/configs reached the handler, want AllowCORS to answer it")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/configs", nil))

	for _, h := range []string{"X-Phenix-Auth-Token", "If-Match", "If-None-Match"} {
		if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, h) {
			t.Errorf("OPTIONS /api/v1/configs: Access-Control-Allow-Headers = %q, want it to include %s", got, h)
		}
	}

	for _, h := range []string{"Date", "ETag", "X-Phenix-Seq", "X-Phenix-Revision", "X-Phenix-Observed-At", "X-Phenix-Stale"} {
		if got := w.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, h) {
			t.Errorf("OPTIONS /api/v1/configs: Access-Control-Expose-Headers = %q, want it to include %s", got, h)
		}
	}
}
