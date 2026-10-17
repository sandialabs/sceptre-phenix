package web

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"phenix/util/plog"
)

func TestRouterServesTheUI(t *testing.T) {
	server := serveAs(t, "alice", testRole("options", "list")).URL

	tests := map[string]struct {
		path         string
		wantStatus   int
		wantEncoding string
		wantCache    string
		wantBody     string
	}{
		"hashed asset": {
			path: "/assets/index-abc123.js", wantStatus: http.StatusOK,
			wantEncoding: "gzip", wantCache: immutableCacheControl, wantBody: "console.log('phenix');",
		},
		"docs": {
			path: "/docs/", wantStatus: http.StatusOK, wantEncoding: "gzip", wantBody: "phenix docs",
		},
		// index.html names the build's hashed assets, so it is revalidated
		"page of the UI": {
			path: "/experiments/test-experiment", wantStatus: http.StatusOK,
			wantCache: "no-cache", wantBody: "<title>phenix</title>",
		},
		// not the UI, which would load itself in WebShark's frame
		"WebShark not installed": {
			path: "/webshark/embed", wantStatus: http.StatusNotFound,
			wantBody: "WebShark is not installed",
		},
		// not the UI's page, which an API client would take for data
		"unknown API route": {
			path: "/api/v1/no-such-route", wantStatus: http.StatusNotFound, wantBody: "no such API route",
		},
		// API data is revalidated: the freshness middleware marks it
		"API": {
			path: "/api/v1/options", wantStatus: http.StatusOK,
			wantEncoding: "gzip", wantCache: "no-cache, private", wantBody: `"deploy-mode"`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodGet, server+tt.path, "", "Accept-Encoding", "gzip")

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("GET %s = %d, want %d", tt.path, resp.StatusCode, tt.wantStatus)
			}

			if got := resp.Header.Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("GET %s Content-Encoding = %q, want %q", tt.path, got, tt.wantEncoding)
			}

			if got := resp.Header.Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("GET %s Cache-Control = %q, want %q", tt.path, got, tt.wantCache)
			}

			// a route's responses, not the router's own 404
			if strings.HasPrefix(tt.path, "/api/") && tt.wantStatus == http.StatusOK &&
				resp.Header.Get("X-Phenix-Seq") == "" {
				t.Errorf("GET %s has no X-Phenix-Seq header, want one", tt.path)
			}

			if body := readBody(t, resp); !strings.Contains(body, tt.wantBody) {
				t.Errorf("GET %s = %q, want it to contain %q", tt.path, body, tt.wantBody)
			}
		})
	}
}

// OPTIONS is answered without running the route's handler; with CORS on, the Unix socket's
// routes get preflights too.
func TestRouterAnswersOptions(t *testing.T) {
	for name, cors := range map[string]bool{"without CORS": false, "with CORS": true} {
		t.Run(name, func(t *testing.T) {
			registry := useJobs(t)
			j := waitJob(t, registry, startTestJob(t, registry, "alice", "", nil, "").ID)

			original := o
			t.Cleanup(func() { o = original })

			o.allowCORS = cors

			server := serveAs(t, "alice", testRole("options", "list")).URL

			// as documented: 204, or 200 when CORS is allowed
			want := http.StatusNoContent
			if cors {
				want = http.StatusOK
			}

			for _, path := range []string{"/api/v1/jobs/" + j.ID, "/api/v1/jobs"} {
				resp := do(t, http.MethodOptions, server+path, "")
				if resp.StatusCode != want {
					t.Errorf("OPTIONS %s = %d, want %d", path, resp.StatusCode, want)
				}

				if got := resp.Header.Get("Access-Control-Allow-Origin") != ""; got != cors {
					t.Errorf("OPTIONS %s has Access-Control-Allow-Origin = %t, want %t", path, got, cors)
				}
			}

			if _, ok := registry.Get(j.ID); !ok {
				t.Errorf("Registry.Get(%s) after OPTIONS /api/v1/jobs/%s found no job, want it kept", j.ID, j.ID)
			}
		})
	}
}

func TestRouterServesTheBuilderBundle(t *testing.T) {
	public, err := fs.Sub(publicFS, "public")
	if err != nil {
		t.Fatalf("fs.Sub(publicFS, public) error = %v", err)
	}

	const path = "/grapheditor/builder.bundle.js"

	url := serveRouter(t, public, "").URL + path

	resp := do(t, http.MethodGet, url, "", "Accept-Encoding", "gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("GET %s Content-Encoding = %q, want gzip", path, got)
	}

	if !strings.Contains(readBody(t, resp), "// --- src/js/view/mxGraph.js\n") {
		t.Errorf("GET %s has no src/js/view/mxGraph.js, want the bundle to include it", path)
	}

	// a cached copy is revalidated without resending the bundle
	resp = do(t, http.MethodGet, url, "", "Accept-Encoding", "gzip", "If-None-Match", resp.Header.Get("ETag"))
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("GET %s with its ETag = %d, want %d", path, resp.StatusCode, http.StatusNotModified)
	}
}

// A request for an unknown route, of the UI or the API, is logged without the
// session token its URL may carry.
func TestUnknownRoutesLogNoToken(t *testing.T) {
	server := serveAs(t, "alice", testRole("options", "list")).URL

	var (
		mu   sync.Mutex
		logs strings.Builder
	)

	plog.AddHandler(t.Name(), slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()

		return logs.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	for _, path := range []string{
		"/api/v1/experiments/e/vms/v/files/download?path=/a&token=SECRET-JWT",
		"/no-such-page?token=SECRET-JWT",
	} {
		_ = readBody(t, do(t, http.MethodGet, server+path, ""))
	}

	mu.Lock()
	defer mu.Unlock()

	got := logs.String()

	for _, msg := range []string{"Unknown API route", "Unknown route"} {
		if !strings.Contains(got, msg) {
			t.Errorf("log after GET of unknown routes = %s, want %q", got, msg)
		}
	}

	if strings.Contains(got, "SECRET-JWT") {
		t.Errorf("log after GET of unknown routes = %s, want no token", got)
	}
}
