// Package lifecycle tells the UI server's long-lived handlers (websockets,
// terminals, streams) that it is shutting down, and counts those still open.
package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"phenix/web/weberror"
)

// CloseReason goes with close code 1001 (going away) on a websocket the
// server closes because it is shutting down.
const CloseReason = "server shutting down"

// CloseWait bounds how long a connection closed at shutdown has to take its
// close frame.
const CloseWait = time.Second

// CloseGoingAway is the close code of a websocket closed at shutdown.
const CloseGoingAway = 1001

// Unavailable answers a request the server refuses while shutting down, with
// RetryAfter, in seconds.
const (
	Unavailable = "the phēnix server is shutting down. Try again when it is available"
	RetryAfter  = "10"
)

// ErrStopping is the cause of a context WithStop canceled at shutdown.
var ErrStopping = errors.New(CloseReason)

type registry struct {
	mu       sync.Mutex
	stopping chan struct{}
	stopped  bool
	open     map[*handle]struct{}
	// closed once stopped with nothing open
	idle chan struct{}
}

type handle struct {
	kind string
	stop func()
}

var (
	currentMu sync.Mutex      //nolint:gochecknoglobals // the server's lifecycle
	current   = newRegistry() //nolint:gochecknoglobals // the server's lifecycle
)

func newRegistry() *registry {
	return &registry{
		mu:       sync.Mutex{},
		stopping: make(chan struct{}),
		stopped:  false,
		open:     make(map[*handle]struct{}),
		idle:     make(chan struct{}),
	}
}

func get() *registry {
	currentMu.Lock()
	defer currentMu.Unlock()

	return current
}

// Start begins a new serving period, as the server does when it starts;
// handlers of an earlier one are no longer counted.
func Start() {
	currentMu.Lock()
	defer currentMu.Unlock()

	current = newRegistry()
}

// Stopping returns a channel closed once the server starts shutting down.
func Stopping() <-chan struct{} {
	return get().stopping
}

// IsStopping reports whether the server has started shutting down.
func IsStopping() bool {
	select {
	case <-Stopping():
		return true
	default:
		return false
	}
}

// Refuse answers 503 with Retry-After and Unavailable as the JSON Error body,
// for a request the server will not take on while shutting down.
func Refuse(w http.ResponseWriter) {
	w.Header().Set("Retry-After", RetryAfter)
	weberror.Error(w, Unavailable, http.StatusServiceUnavailable)
}

// RefuseWhileStopping refuses the request, and reports true, once the server
// has started shutting down.
func RefuseWhileStopping(w http.ResponseWriter) bool {
	if !IsStopping() {
		return false
	}

	Refuse(w)

	return true
}

// Stop calls, each on its own goroutine, the stop function of every handler
// registered now or later. Only the first call does anything.
func Stop() {
	r := get()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return
	}

	r.stopped = true
	close(r.stopping)

	for h := range r.open {
		if h.stop != nil {
			go h.stop()
		}
	}

	r.idleLocked()
}

// Track registers a long-lived handler of kind, such as "VNC", which stop (if
// not nil) ends at shutdown. Call the returned release when the handler ends.
func Track(kind string, stop func()) func() {
	r := get()
	h := &handle{kind: kind, stop: stop}

	r.mu.Lock()
	r.open[h] = struct{}{}

	if r.stopped && stop != nil {
		go stop()
	}
	r.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()

			delete(r.open, h)
			r.idleLocked()
		})
	}
}

// Conn is a connection a handler ends by closing it, such as a
// golang.org/x/net/websocket connection.
type Conn interface {
	SetDeadline(t time.Time) error
	Close() error
}

// closeWriter sends a close frame of a status but no reason, as a
// golang.org/x/net/websocket connection does.
type closeWriter interface {
	WriteClose(status int) error
}

// TrackConn is Track for a handler that ends when conn closes. At shutdown
// conn's deadline is cut to CloseWait, it is sent close code 1001 if it can
// take one, and it is closed.
func TrackConn(kind string, conn Conn) func() {
	return Track(kind, func() {
		_ = conn.SetDeadline(time.Now().Add(CloseWait))

		if c, ok := conn.(closeWriter); ok {
			_ = c.WriteClose(CloseGoingAway)
		}

		_ = conn.Close()
	})
}

// WithStop registers a handler of kind with a copy of ctx that shutdown
// cancels with cause ErrStopping. Call the returned release when it ends.
func WithStop(ctx context.Context, kind string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	release := Track(kind, func() { cancel(ErrStopping) })

	return ctx, func() {
		release()
		cancel(nil)
	}
}

// Stopped reports whether ctx, from WithStop, ended because the server is
// shutting down.
func Stopped(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrStopping)
}

// Wait waits, after Stop, until every handler has ended or ctx is done. It
// returns how many of each kind were still open, or nil for none.
func Wait(ctx context.Context) map[string]int {
	r := get()

	select {
	case <-r.idle:
		return nil
	case <-ctx.Done():
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.open) == 0 {
		return nil
	}

	open := make(map[string]int)
	for h := range r.open {
		open[h.kind]++
	}

	return open
}

// idleLocked closes idle once stopped with nothing open. r.mu is held.
func (r *registry) idleLocked() {
	if !r.stopped || len(r.open) > 0 {
		return
	}

	select {
	case <-r.idle:
	default:
		close(r.idle)
	}
}
