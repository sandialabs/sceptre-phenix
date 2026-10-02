package web

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
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
		"API": {
			path: "/api/v1/options", wantStatus: http.StatusOK,
			wantEncoding: "gzip", wantBody: `"deploy-mode"`,
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

			if body := readBody(t, resp); !strings.Contains(body, tt.wantBody) {
				t.Errorf("body %q does not have %q", body, tt.wantBody)
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
