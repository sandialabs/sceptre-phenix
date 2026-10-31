package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

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
