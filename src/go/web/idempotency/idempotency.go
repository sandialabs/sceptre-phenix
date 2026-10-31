// Package idempotency replays a user's first 202 for an Idempotency-Key reused on the same method and path, so a retry
// starts no second job; a changed body or query gets 422, a key in flight 409. Only 202s are kept, in memory.
package idempotency

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"sync"
	"time"

	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

const (
	// Header is the request header naming a request's idempotency key.
	Header = "Idempotency-Key"
	// ReplayedHeader is set to true on an answer replayed for a retry.
	ReplayedHeader = "Idempotent-Replayed"

	// MaxKeyLength bounds a key, which must be printable ASCII.
	MaxKeyLength = 255

	// DefaultTTL is how long a key is kept: a job's own record lasts an hour
	// after it ends, so a retry after that could not see the job anyway.
	DefaultTTL = time.Hour
	// DefaultMaxEntries bounds how many keys are kept; the oldest go first.
	DefaultMaxEntries = 1000
	// DefaultMaxBytes bounds the answers kept, in body bytes.
	DefaultMaxBytes = 16 << 20

	// maxBodyBytes is the largest request body fingerprinted; a larger one
	// is served without a key, and the job routes refuse it anyway.
	maxBodyBytes = 1 << 20
)

// entry is a key's first request, and its answer once it has one.
type entry struct {
	fingerprint [sha256.Size]byte
	created     time.Time
	done        bool
	status      int
	location    string
	contentType string
	body        []byte
}

// Store keeps the answers given to requests with an idempotency key.
type Store struct {
	ttl        time.Duration
	maxEntries int
	maxBytes   int
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
	bytes   int
}

// New returns a Store keeping at most maxEntries keys, and answers of at most
// maxBytes in all, each for ttl.
func New(ttl time.Duration, maxEntries, maxBytes int) *Store {
	return &Store{
		ttl:        ttl,
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		now:        time.Now,
		mu:         sync.Mutex{},
		entries:    map[string]*entry{},
		bytes:      0,
	}
}

// outcome is what a request's key says to do with it.
type outcome int

const (
	serve outcome = iota
	replay
	mismatch
	inFlight
)

// ValidKey reports whether key may be used: 1 to MaxKeyLength printable
// ASCII characters.
func ValidKey(key string) bool {
	if key == "" || len(key) > MaxKeyLength {
		return false
	}

	for i := range len(key) {
		if key[i] < ' ' || key[i] > '~' {
			return false
		}
	}

	return true
}

// Wrap serves next, honoring the requests' Idempotency-Key headers.
func (s *Store) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(Header)
		if key == "" {
			next.ServeHTTP(w, r)

			return
		}

		if !ValidKey(key) {
			weberror.Error(w, "Idempotency-Key must be 1 to 255 printable ASCII characters", http.StatusBadRequest)

			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			weberror.Error(w, "reading request body: "+err.Error(), http.StatusBadRequest)

			return
		}

		if len(body) > maxBodyBytes {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}

			next.ServeHTTP(w, r)

			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))

		var (
			user        = middleware.UserFromContext(r.Context())
			scope       = user + "\x00" + r.Method + " " + r.URL.Path + "\x00" + key
			fingerprint = sha256.Sum256([]byte(r.URL.RawQuery + "\x00" + string(body)))
		)

		got, kept := s.reserve(scope, fingerprint)

		switch got {
		case replay:
			plog.Debug(plog.TypeSystem, "replaying a request's answer for its Idempotency-Key",
				"user", user, "method", r.Method, "path", r.URL.Path)

			if kept.contentType != "" {
				w.Header().Set("Content-Type", kept.contentType)
			}

			if kept.location != "" {
				w.Header().Set("Location", kept.location)
			}

			w.Header().Set(ReplayedHeader, "true")
			w.WriteHeader(kept.status)
			_, _ = w.Write(kept.body) //nolint:gosec // G705 replays a response this server wrote
		case mismatch:
			weberror.Error(w, "a different request already used this Idempotency-Key", http.StatusUnprocessableEntity)
		case inFlight:
			w.Header().Set("Retry-After", "1")
			weberror.Error(w, "the server is still serving a request with this Idempotency-Key", http.StatusConflict)
		case serve:
			rec := &recorder{ResponseWriter: w, status: 0, body: nil, overflow: false}

			defer func() { s.finish(scope, rec) }()

			next.ServeHTTP(rec, r)
		}
	})
}

// reserve looks the key up, holding it for this request when it is new, and
// returns what to do with the request and, to replay, the kept answer.
func (s *Store) reserve(scope string, fingerprint [sha256.Size]byte) (outcome, entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.expire(now)

	if e, ok := s.entries[scope]; ok {
		switch {
		case e.fingerprint != fingerprint:
			return mismatch, entry{}
		case !e.done:
			return inFlight, entry{}
		default:
			return replay, *e
		}
	}

	s.entries[scope] = &entry{
		fingerprint: fingerprint, created: now, done: false, status: 0, location: "", contentType: "", body: nil,
	}
	s.evict()

	return serve, entry{}
}

// finish keeps the request's answer when it is a 202, and frees its key
// otherwise, so the client can retry a refused or failed request.
func (s *Store) finish(scope string, rec *recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[scope]
	if !ok {
		return
	}

	if rec.status != http.StatusAccepted || rec.overflow {
		delete(s.entries, scope)

		return
	}

	e.done = true
	e.status = rec.status
	e.location = rec.Header().Get("Location")
	e.contentType = rec.Header().Get("Content-Type")
	e.body = rec.body
	s.bytes += len(e.body)

	s.evict()
}

// expire drops the keys older than the TTL.
func (s *Store) expire(now time.Time) {
	for scope, e := range s.entries {
		if now.Sub(e.created) >= s.ttl {
			s.drop(scope, e)
		}
	}
}

// evict drops the oldest answered keys while there are too many keys or
// bytes kept; keys still being served stay.
func (s *Store) evict() {
	for len(s.entries) > s.maxEntries || s.bytes > s.maxBytes {
		var (
			oldest string
			found  *entry
		)

		for scope, e := range s.entries {
			if e.done && (found == nil || e.created.Before(found.created)) {
				oldest, found = scope, e
			}
		}

		if found == nil {
			return
		}

		s.drop(oldest, found)
	}
}

// drop forgets one key.
func (s *Store) drop(scope string, e *entry) {
	s.bytes -= len(e.body)
	delete(s.entries, scope)
}

// recorder passes an answer on and keeps a copy of it to replay.
type recorder struct {
	http.ResponseWriter

	status   int
	body     []byte
	overflow bool
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}

	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	if len(r.body)+len(p) > maxBodyBytes {
		r.overflow = true
	} else if !r.overflow {
		r.body = append(r.body, p...)
	}

	return r.ResponseWriter.Write(p)
}
