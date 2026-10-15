package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Every response carries a sequence number that increases from one request
// to the next, and a handler's own Cache-Control wins.
func TestFreshness(t *testing.T) {
	handler := Freshness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("store") {
			w.Header().Set("Cache-Control", "no-store")
		}
	}))

	var last uint64

	for range 3 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

		seq, err := strconv.ParseUint(w.Header().Get("X-Phenix-Seq"), 10, 64)
		if err != nil || seq <= last {
			t.Fatalf("X-Phenix-Seq = %q after %d, want a larger number", w.Header().Get("X-Phenix-Seq"), last)
		}

		last = seq

		if got := w.Header().Get("Cache-Control"); got != "no-cache, private" {
			t.Errorf("Cache-Control = %q, want no-cache, private", got)
		}
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments?store", nil))

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want the handler's no-store", got)
	}
}
