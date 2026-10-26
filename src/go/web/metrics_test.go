package web

import (
	"net/http"
	"strings"
	"testing"

	"phenix/util/metrics/metricstest"
)

// GET /api/v1/metrics needs a held session whose role has metrics get: no
// token is 403, an untrusted one 401, and a role without it 403.
//
//nolint:paralleltest // replaces the server options and store
func TestGetMetricsAuth(t *testing.T) {
	tests := []struct {
		name   string
		header func(t *testing.T) []string
		want   int
	}{
		{"no token", func(*testing.T) []string { return nil }, http.StatusForbidden},
		{
			"malformed token",
			func(*testing.T) []string { return []string{"X-Phenix-Auth-Token", "Bearer not.a.jwt"} },
			http.StatusUnauthorized,
		},
		{
			"role without metrics get",
			func(t *testing.T) []string {
				t.Helper()

				return []string{"X-Phenix-Auth-Token", "Bearer " + useTestUser(t, testRole("experiments", "get"))}
			},
			http.StatusForbidden,
		},
		{
			"role with metrics get",
			func(t *testing.T) []string {
				t.Helper()

				return []string{"X-Phenix-Auth-Token", "Bearer " + useTestUser(t, testRole("metrics", "get"))}
			},
			http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := serveContract(t, func(o *serverOptions) { o.jwtKey = testJWTKey })

			resp := do(t, "GET", server.URL+"/api/v1/metrics", "", tt.header(t)...)
			body := readBody(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("GET /api/v1/metrics with %s = %d, want %d: %s", tt.name, resp.StatusCode, tt.want, body)
			}

			if tt.want != http.StatusOK {
				return
			}

			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("GET /api/v1/metrics Content-Type = %q, want text/plain", ct)
			}

			for _, name := range []string{
				"go_goroutines", "phenix_http_requests_total", "phenix_websocket_clients", "phenix_jobs_running",
			} {
				if !strings.Contains(body, "\n# TYPE "+name+" ") {
					t.Errorf("GET /api/v1/metrics does not report %s:\n%s", name, body)
				}
			}
		})
	}
}

// Requests count by the template of the route they matched, "ui" for a UI
// page or "unmatched", and the names in their paths never become labels.
//
//nolint:paralleltest // replaces the server options and store
func TestMetricsRecordRequests(t *testing.T) {
	server := serveContractAs(t, testRole("metrics", "get"), nil).URL

	tests := []struct {
		path, route string
	}{
		{"/api/v1/experiments/secret-experiment", "/api/v1/experiments/{name}"},
		{"/api/v1/experiments/secret-experiment/vms/secret-vm", "/api/v1/experiments/{exp}/vms/{name}"},
		{"/api/v1/no-such-secret-route", routeUnmatched},
		{"/experiments/secret-experiment", routeUI},
	}

	for _, tt := range tests {
		labels := map[string]string{"route": tt.route, "method": "GET"}
		before := metricstest.Value(t, "phenix_http_requests_total", labels)
		timed := metricstest.Value(t, "phenix_http_request_duration_seconds", labels)

		readBody(t, do(t, "GET", server+tt.path, ""))

		if got := metricstest.Value(t, "phenix_http_requests_total", labels) - before; got != 1 {
			t.Errorf("GET %s: phenix_http_requests_total %v moved by %v, want 1", tt.path, labels, got)
		}

		if got := metricstest.Value(t, "phenix_http_request_duration_seconds", labels) - timed; got != 1 {
			t.Errorf("GET %s: phenix_http_request_duration_seconds %v moved by %v, want 1", tt.path, labels, got)
		}
	}

	refused := map[string]string{"route": "/api/v1/experiments/{name}", "method": "GET", "code": "4xx"}
	if got := metricstest.Value(t, "phenix_http_requests_total", refused); got == 0 {
		t.Errorf("phenix_http_requests_total %v = 0, want the refused request counted", refused)
	}

	body := readBody(t, do(t, "GET", server+"/api/v1/metrics", ""))
	if strings.Contains(body, "secret") {
		t.Errorf("GET /api/v1/metrics names a path's experiment, VM or route:\n%s", body)
	}
}

// With ui.metrics off, /api/v1/metrics is not served and requests are not
// counted.
//
//nolint:paralleltest // replaces the server options and store
func TestMetricsDisabled(t *testing.T) {
	server := serveContractAs(t, testRole("metrics", "get"), func(o *serverOptions) { o.metrics = false }).URL

	labels := map[string]string{"route": routeUnmatched}
	before := metricstest.Value(t, "phenix_http_requests_total", labels)

	resp := do(t, "GET", server+"/api/v1/metrics", "")
	readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/v1/metrics with ui.metrics off = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	if got := metricstest.Value(t, "phenix_http_requests_total", labels) - before; got != 0 {
		t.Errorf("phenix_http_requests_total %v with ui.metrics off moved by %v, want 0", labels, got)
	}
}
