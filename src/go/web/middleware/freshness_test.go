package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
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

	for range 2 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

		seq, err := strconv.ParseUint(w.Header().Get("X-Phenix-Seq"), 10, 64)
		if err != nil || seq <= last {
			t.Errorf("GET /api/v1/experiments: X-Phenix-Seq = %q after %d, want a larger number", w.Header().Get("X-Phenix-Seq"), last)
		}

		last = seq

		if got := w.Header().Get("Cache-Control"); got != "no-cache, private" {
			t.Errorf("GET /api/v1/experiments: Cache-Control = %q, want no-cache, private", got)
		}
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments?store", nil))

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET /api/v1/experiments?store: Cache-Control = %q, want the handler's no-store", got)
	}
}

// Concurrent requests never share a sequence number, and each request's
// numbers keep increasing.
func TestFreshnessConcurrent(t *testing.T) {
	const (
		goroutines = 32
		requests   = 200
	)

	var (
		handler = Freshness(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		seen    sync.Map
		wg      sync.WaitGroup
	)

	for range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			var last uint64

			for range requests {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

				seq, err := strconv.ParseUint(w.Header().Get("X-Phenix-Seq"), 10, 64)
				if err != nil || seq <= last {
					t.Errorf("X-Phenix-Seq = %q after %d, want a larger number", w.Header().Get("X-Phenix-Seq"), last)

					return
				}

				if _, dup := seen.LoadOrStore(seq, true); dup {
					t.Errorf("X-Phenix-Seq = %d, which another response carried", seq)

					return
				}

				last = seq
			}
		}()
	}

	wg.Wait()
}
