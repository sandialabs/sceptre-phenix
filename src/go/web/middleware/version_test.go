package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"phenix/version"
)

// Every response names the server's build, as the served openapi.yml does, whatever the handler answers; the
// handler sees the header already set and its own response reaches the client.
func TestVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		body   string
	}{
		"no response written": {status: http.StatusOK},
		"handler's response":  {status: http.StatusCreated, body: "created"},
		"handler's error":     {status: http.StatusNotFound, body: "not found"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var seen string

			handler := Version(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				seen = w.Header().Get(HeaderVersion)

				if tc.body != "" {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}
			}))

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

			want := version.API()
			if got := w.Header().Get("X-Phenix-Version"); got != want || seen != want {
				t.Errorf("GET /api/v1/experiments: X-Phenix-Version = %q (handler saw %q), want %q", got, seen, want)
			}

			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Errorf("GET /api/v1/experiments = %d %q, want %d %q", w.Code, w.Body.String(), tc.status, tc.body)
			}
		})
	}
}
