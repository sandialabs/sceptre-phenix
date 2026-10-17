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
				t.Fatalf("status %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			if got := resp.Header.Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("Content-Encoding %q, want %q", got, tt.wantEncoding)
			}

			if got := resp.Header.Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control %q, want %q", got, tt.wantCache)
			}

			// a route's responses, not the router's own 404
			if strings.HasPrefix(tt.path, "/api/") && tt.wantStatus == http.StatusOK &&
				resp.Header.Get("X-Phenix-Seq") == "" {
				t.Error("an API response has no X-Phenix-Seq")
			}

			if body := readBody(t, resp); !strings.Contains(body, tt.wantBody) {
				t.Errorf("body %q does not have %q", body, tt.wantBody)
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
					t.Errorf("OPTIONS %s: status %d, want %d", path, resp.StatusCode, want)
				}

				if got := resp.Header.Get("Access-Control-Allow-Origin") != ""; got != cors {
					t.Errorf("OPTIONS %s: CORS headers %t, want %t", path, got, cors)
				}
			}

			if _, ok := registry.Get(j.ID); !ok {
				t.Error("OPTIONS on DELETE /jobs/{id} dismissed the job")
			}
		})
	}
}

func TestRouterServesTheBuilderBundle(t *testing.T) {
	public, err := fs.Sub(publicFS, "public")
	if err != nil {
		t.Fatal(err)
	}

	url := serveRouter(t, public, "").URL + "/grapheditor/builder.bundle.js"

	resp := do(t, http.MethodGet, url, "", "Accept-Encoding", "gzip")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, encoding %q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}

	if !strings.Contains(readBody(t, resp), "// --- src/js/view/mxGraph.js\n") {
		t.Fatal("bundle is missing mxGraph")
	}

	// a cached copy is revalidated without resending the bundle
	resp = do(t, http.MethodGet, url, "", "Accept-Encoding", "gzip", "If-None-Match", resp.Header.Get("ETag"))
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation status %d, want %d", resp.StatusCode, http.StatusNotModified)
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

	if got := logs.String(); !strings.Contains(got, "Unknown API route") || !strings.Contains(got, "Unknown route") {
		t.Fatalf("unknown routes not logged: %s", got)
	} else if strings.Contains(got, "SECRET-JWT") {
		t.Errorf("logged the token: %s", got)
	}
}
