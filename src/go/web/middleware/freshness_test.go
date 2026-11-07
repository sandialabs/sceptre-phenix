package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"phenix/util/freshness"
)

// Every response carries a sequence number that increases from one request
// to the next, and a handler's own Cache-Control wins.
func TestFreshness(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// The sequence number is taken before the handler runs, so it is lower than any the handler takes, and the
// handler's response reaches the client unchanged.
func TestFreshnessStampsBeforeHandler(t *testing.T) {
	t.Parallel()

	var inside uint64

	handler := Freshness(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if w.Header().Get(freshness.HeaderSeq) == "" {
			t.Error("handler saw no X-Phenix-Seq header, want it set before the handler runs")
		}

		inside = freshness.Seq()

		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body"))
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/experiments", nil))

	seq, err := strconv.ParseUint(w.Header().Get(freshness.HeaderSeq), 10, 64)
	if err != nil || seq >= inside {
		t.Errorf("X-Phenix-Seq = %q, want a number below the handler's %d", w.Header().Get(freshness.HeaderSeq), inside)
	}

	if w.Code != http.StatusTeapot || w.Body.String() != "body" {
		t.Errorf("POST /api/v1/experiments = %d %q, want the handler's %d %q", w.Code, w.Body.String(), http.StatusTeapot, "body")
	}
}
