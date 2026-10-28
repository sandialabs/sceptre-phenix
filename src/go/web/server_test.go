package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"

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
			path: "/api/v1/no-such-route", wantStatus: http.StatusNotFound, wantBody: "no API route matches this request",
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

// OPTIONS is answered with 204, before authentication, without running the
// route's handler, including on the routes the Unix socket also serves.
func TestRouterAnswersOptions(t *testing.T) {
	registry := useJobs(t)
	j := waitJob(t, registry, startTestJob(t, registry, "alice", "", nil, "").ID)

	servers := map[string]string{
		"as alice":      serveAs(t, "alice", testRole("options", "list")).URL,
		"without token": serveRouter(t, testPublic(), testJWTKey).URL,
	}

	for name, server := range servers {
		for _, path := range []string{"/api/v1/jobs/" + j.ID, "/api/v1/jobs"} {
			if resp := do(t, http.MethodOptions, server+path, ""); resp.StatusCode != http.StatusNoContent {
				t.Errorf("OPTIONS %s %s = %d, want %d", path, name, resp.StatusCode, http.StatusNoContent)
			}
		}
	}

	if _, ok := registry.Get(j.ID); !ok {
		t.Errorf("Registry.Get(%s) after OPTIONS /api/v1/jobs/%s found no job, want it kept", j.ID, j.ID)
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

// spaIndexBody stands in for the SPA index the router level NotFoundHandler serves in production.
const spaIndexBody = "<!doctype html><title>phenix</title>"

// newNotFoundRouter returns a router wired as [Start] wires it: the SPA index
// at the root and [apiNotFoundHandler] under "/api/v1", with one API route.
func newNotFoundRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(spaIndexBody))
	})

	api := router.PathPrefix("/api/v1").Subrouter()
	api.NotFoundHandler = apiNotFoundHandler()

	api.HandleFunc("/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).Methods(http.MethodGet)

	return router
}

// TestUnknownAPIRoutesAnswerJSON asserts an unmatched request under /api/v1
// is answered by [apiNotFoundHandler] with a JSON 404, never by the SPA index.
func TestUnknownAPIRoutesAnswerJSON(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/no-such-route"},
		{method: http.MethodPost, path: "/api/v1/no-such-route"},
		{method: http.MethodGet, path: "/api/v1/known/no-such-route"},
		{method: http.MethodDelete, path: "/api/v1/experiments/exp/no-such-route"},
	}

	router := newNotFoundRouter()

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.path, nil))

			if recorder.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
			}

			if contentType := recorder.Header().Get("Content-Type"); contentType != mimeJSON {
				t.Errorf("Content-Type = %q, want %q", contentType, mimeJSON)
			}

			if strings.Contains(recorder.Body.String(), spaIndexBody) {
				t.Errorf("the SPA index answered an API request")
			}

			var body struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding the body %q: %v", recorder.Body, err)
			}

			if want := "no API route matches this request"; body.Message != want {
				t.Errorf("message = %q, want %q", body.Message, want)
			}
		})
	}
}

// TestNonAPIRoutesStillServeTheSPA asserts the SPA index still answers every
// path outside /api/v1, and a known API route is still served by its handler.
func TestNonAPIRoutesStillServeTheSPA(t *testing.T) {
	router := newNotFoundRouter()

	for _, path := range []string{"/", "/experiments", "/configs/topology/foo", "/api", "/api/v2/experiments"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusOK || recorder.Body.String() != spaIndexBody {
			t.Errorf("GET %s = %d %q, want %d and the SPA index", path, recorder.Code, recorder.Body, http.StatusOK)
		}
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/known", nil))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("GET /api/v1/known: status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
