package idempotency

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/web/middleware"
)

// jobStarter answers 202 with a new job's location and body, counting the
// jobs it started, or the status the request's ?status= names.
type jobStarter struct {
	started atomic.Int64
}

func (j *jobStarter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	if status := r.URL.Query().Get("status"); status != "" {
		code, _ := strconv.Atoi(status)
		http.Error(w, "refused", code)

		return
	}

	id := strconv.FormatInt(j.started.Add(1), 10)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/api/v1/jobs/"+id)
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"id":"` + id + `","body":"` + string(body) + `"}`))
}

// answer is what a request got back.
type answer struct {
	Status   int
	Location string
	Replayed string
	Body     string
}

// send sends a POST of body to target as user, with key when it is set.
func send(t interface{ Helper() }, h http.Handler, user, target, key, body string) answer {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, user))

	if key != "" {
		req.Header.Set(Header, key)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return answer{
		Status:   rec.Code,
		Location: rec.Header().Get("Location"),
		Replayed: rec.Header().Get(ReplayedHeader),
		Body:     strings.TrimSpace(rec.Body.String()),
	}
}

const actions = "/api/v1/experiments/exp/vms/actions"

func TestRetryReplaysTheFirstAnswer(t *testing.T) {
	var (
		jobs jobStarter
		h    = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
	)

	first := send(t, h, "alice", actions, "k1", "start")
	want := answer{Status: http.StatusAccepted, Location: "/api/v1/jobs/1", Body: `{"id":"1","body":"start"}`}

	if diff := cmp.Diff(want, first); diff != "" {
		t.Fatalf("first POST with Idempotency-Key k1 mismatch (-want +got):\n%s", diff)
	}

	want.Replayed = "true"
	if diff := cmp.Diff(want, send(t, h, "alice", actions, "k1", "start")); diff != "" {
		t.Errorf("retried POST with Idempotency-Key k1 mismatch (-want +got):\n%s", diff)
	}

	if got := jobs.started.Load(); got != 1 {
		t.Errorf("jobs started = %d, want 1", got)
	}
}

func TestKeysAreScopedToUserMethodAndPath(t *testing.T) {
	var (
		jobs jobStarter
		h    = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
	)

	send(t, h, "alice", actions, "k1", "start")

	tests := []struct {
		name, user, target, key string
		want                    string
	}{
		{name: "another user's same key", user: "bob", target: actions, key: "k1", want: "/api/v1/jobs/2"},
		{name: "the same key on another path", user: "alice", target: "/api/v1/experiments/other/vms/actions", key: "k1",
			want: "/api/v1/jobs/3"},
		{name: "another key", user: "alice", target: actions, key: "k2", want: "/api/v1/jobs/4"},
		{name: "no key", user: "alice", target: actions, key: "", want: "/api/v1/jobs/5"},
	}

	for _, tt := range tests {
		got := send(t, h, tt.user, tt.target, tt.key, "start")
		if got.Status != http.StatusAccepted || got.Location != tt.want || got.Replayed != "" {
			t.Errorf("%s: POST = %d at %q (replayed %q), want a new job at %s", tt.name, got.Status, got.Location, got.Replayed,
				tt.want)
		}
	}
}

func TestKeyReusedForAnotherRequestIsRefused(t *testing.T) {
	var (
		jobs jobStarter
		h    = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
	)

	send(t, h, "alice", actions+"?disk=a", "k1", "start")

	for name, target := range map[string]string{"another body": actions + "?disk=a", "another query": actions + "?disk=b"} {
		body := "start"
		if name == "another body" {
			body = "stop"
		}

		if got := send(t, h, "alice", target, "k1", body); got.Status != http.StatusUnprocessableEntity {
			t.Errorf("POST of %s with a used Idempotency-Key = %d, want %d", name, got.Status, http.StatusUnprocessableEntity)
		}
	}

	if got := jobs.started.Load(); got != 1 {
		t.Errorf("jobs started = %d, want 1", got)
	}
}

func TestOnlyAcceptedAnswersAreKept(t *testing.T) {
	var (
		jobs jobStarter
		h    = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
	)

	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		target := actions + "?status=" + strconv.Itoa(status)
		send(t, h, "alice", target, "k-"+strconv.Itoa(status), "start")

		// the retry is served again rather than answered from the first
		if got := send(t, h, "alice", target, "k-"+strconv.Itoa(status), "start"); got.Status != status || got.Replayed != "" {
			t.Errorf("retry of a POST answered %d = %d (replayed %q), want it served again", status, got.Status, got.Replayed)
		}
	}
}

func TestKeyBeingServedIsConflict(t *testing.T) {
	var (
		entered = make(chan struct{})
		release = make(chan struct{})
		store   = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes)
		h       = store.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusAccepted)
		}))
		done = make(chan answer)
	)

	go func() { done <- send(t, h, "alice", actions, "k1", "start") }()

	<-entered

	if got := send(t, h, "alice", actions, "k1", "start"); got.Status != http.StatusConflict {
		t.Errorf("POST with the Idempotency-Key of a request still being served = %d, want %d", got.Status, http.StatusConflict)
	}

	close(release)

	if got := <-done; got.Status != http.StatusAccepted {
		t.Errorf("first POST = %d, want %d", got.Status, http.StatusAccepted)
	}
}

func TestKeysExpire(t *testing.T) {
	var (
		jobs  jobStarter
		now   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		store = New(time.Hour, DefaultMaxEntries, DefaultMaxBytes)
		h     = store.Wrap(&jobs)
	)

	store.now = func() time.Time { return now }

	send(t, h, "alice", actions, "k1", "start")

	now = now.Add(59 * time.Minute)
	if got := send(t, h, "alice", actions, "k1", "start"); got.Replayed != "true" {
		t.Errorf("retry within the TTL replayed = %q, want true", got.Replayed)
	}

	now = now.Add(time.Minute)
	if got := send(t, h, "alice", actions, "k1", "start"); got.Replayed != "" || got.Location != "/api/v1/jobs/2" {
		t.Errorf("retry after the TTL = %q (replayed %q), want a new job", got.Location, got.Replayed)
	}
}

func TestStoreIsBounded(t *testing.T) {
	tests := []struct {
		name                 string
		maxEntries, maxBytes int
	}{
		{name: "by keys", maxEntries: 2, maxBytes: DefaultMaxBytes},
		{name: "by bytes", maxEntries: DefaultMaxEntries, maxBytes: 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				jobs  jobStarter
				now   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				store = New(time.Hour, tt.maxEntries, tt.maxBytes)
				h     = store.Wrap(&jobs)
			)

			store.now = func() time.Time { now = now.Add(time.Second); return now }

			// each answer is 25 bytes, so a third answer is one too many either way
			for _, key := range []string{"k1", "k2", "k3"} {
				send(t, h, "alice", actions, key, "start")
			}

			if got := send(t, h, "alice", actions, "k1", "start"); got.Replayed != "" {
				t.Errorf("retry of the oldest key once the store is full replayed = %q, want it forgotten", got.Replayed)
			}

			if got := send(t, h, "alice", actions, "k3", "start"); got.Replayed != "true" {
				t.Errorf("retry of the newest key replayed = %q, want true", got.Replayed)
			}

			store.mu.Lock()
			defer store.mu.Unlock()

			if len(store.entries) > tt.maxEntries || store.bytes > tt.maxBytes {
				t.Errorf("store holds %d keys and %d bytes, want at most %d and %d",
					len(store.entries), store.bytes, tt.maxEntries, tt.maxBytes)
			}
		})
	}
}

func TestInvalidKeyIsRefused(t *testing.T) {
	var (
		jobs jobStarter
		h    = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
	)

	for _, key := range []string{strings.Repeat("k", MaxKeyLength+1), "tab\tkey", "café"} {
		if got := send(t, h, "alice", actions, key, "start"); got.Status != http.StatusBadRequest {
			t.Errorf("POST with Idempotency-Key %q = %d, want %d", key, got.Status, http.StatusBadRequest)
		}
	}

	if got := jobs.started.Load(); got != 0 {
		t.Errorf("jobs started = %d, want 0", got)
	}
}

// A key is valid exactly when it is 1 to 255 printable ASCII characters.
func TestValidKeyProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		key := rapid.OneOf(
			rapid.StringMatching(`[ -~]{1,255}`),
			rapid.String(),
			rapid.StringN(MaxKeyLength, MaxKeyLength+5, -1),
		).Draw(t, "key")

		printable := key != "" && len(key) <= MaxKeyLength && strings.IndexFunc(key, func(r rune) bool {
			return r < ' ' || r > '~'
		}) < 0

		if got := ValidKey(key); got != printable {
			t.Fatalf("ValidKey(%q) = %t, want %t", key, got, printable)
		}
	})
}

// Whatever keys and bodies one user sends, a key starts at most one job, and every answer for a key is that job's.
func TestOneJobPerKeyProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var (
			jobs      jobStarter
			h         = New(DefaultTTL, DefaultMaxEntries, DefaultMaxBytes).Wrap(&jobs)
			first     = map[string]answer{}
			firstBody = map[string]string{}
		)

		for range rapid.IntRange(1, 20).Draw(t, "requests") {
			key := rapid.SampledFrom([]string{"a", "b", "c"}).Draw(t, "key")
			body := rapid.SampledFrom([]string{"start", "stop"}).Draw(t, "body")
			got := send(t, h, "alice", actions, key, body)

			kept, seen := first[key]

			switch {
			case !seen:
				first[key], firstBody[key] = got, body
			case body != firstBody[key]:
				if got.Status != http.StatusUnprocessableEntity {
					t.Fatalf("key %s reused for another body = %d, want %d", key, got.Status, http.StatusUnprocessableEntity)
				}
			case got.Location != kept.Location || got.Body != kept.Body || got.Replayed != "true":
				t.Fatalf("key %s answered %+v, want the first answer %+v replayed", key, got, kept)
			}
		}

		if got := int(jobs.started.Load()); got != len(first) {
			t.Fatalf("jobs started = %d, want %d, one per key", got, len(first))
		}
	})
}
