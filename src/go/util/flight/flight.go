// Package flight merges concurrent identical calls into one run, so several
// callers asking minimega the same question at once cost one set of commands.
package flight

import (
	"context"
	"errors"
	"sync"

	"phenix/util/metrics"
)

// ErrAborted is what the callers that joined a run get when the run panicked
// (or otherwise never returned), instead of a zero value that would read as an
// empty but successful answer.
var ErrAborted = errors.New("shared run aborted before returning a result")

// Group merges concurrent calls with the same key into one run. A call joins
// a run only until the run is sealed (or forgotten); nothing is kept once a
// run finishes, so callers that reuse results layer a cache on top. The zero
// value is ready to use.
type Group[T any] struct {
	// Name labels the group's calls in the metrics; it must be fixed in code.
	Name string

	mu    sync.Mutex
	calls map[string]*call[T]
}

type call[T any] struct {
	done chan struct{}
	val  T
	err  error // ErrAborted if fn did not return; set before done is closed

	// guarded by the group's mu: cancel cancels the run's context; waiting
	// counts the callers that have not left; finished is set once fn is over,
	// after which callers leaving change nothing
	cancel   context.CancelFunc
	waiting  int
	finished bool
}

// Do runs fn, or joins key's unsealed run and returns its result, which callers must not modify; fn
// may call seal to stop later joins. fn's context ends once every caller's ctx is done, and a
// joined caller returns ctx's error at once, or ErrAborted if fn panicked.
func (g *Group[T]) Do( //nolint:ireturn // T is concrete at each use
	ctx context.Context,
	key string,
	fn func(ctx context.Context, seal func()) (T, error),
) (T, error) {
	g.mu.Lock()

	if g.calls == nil {
		g.calls = make(map[string]*call[T])
	}

	if c, ok := g.calls[key]; ok {
		leave := g.join(ctx, key, c)

		g.mu.Unlock()

		metrics.Flight(g.Name, metrics.FlightJoined)

		defer leave()

		select {
		case <-c.done:
			return c.val, c.err
		case <-ctx.Done():
			var zero T

			return zero, ctx.Err() //nolint:wrapcheck // the caller's own context
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())

	c := &call[T]{done: make(chan struct{}), cancel: cancel} //nolint:exhaustruct // val and err are set by fn below

	g.calls[key] = c
	leave := g.join(ctx, key, c)

	g.mu.Unlock()

	metrics.Flight(g.Name, metrics.FlightRan)

	defer leave()

	seal := func() { g.forget(key, c) }

	completed := false

	// Release anyone waiting even if fn panics, telling them it did.
	defer func() {
		if !completed {
			c.err = ErrAborted
		}

		g.mu.Lock()
		c.finished = true

		if g.calls[key] == c {
			delete(g.calls, key)
		}
		g.mu.Unlock()

		cancel()
		close(c.done)
	}()

	c.val, c.err = fn(runCtx, seal)
	completed = true

	return c.val, c.err
}

// Forget stops later callers joining the run for key, if any: they start a new
// run, while the callers already waiting get this one's result.
func (g *Group[T]) Forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.calls, key)
}

// Waiting returns how many callers wait for the run for key that can still be
// joined, or 0 when there is none.
func (g *Group[T]) Waiting(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if c, ok := g.calls[key]; ok {
		return c.waiting
	}

	return 0
}

func (g *Group[T]) forget(key string, c *call[T]) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.calls[key] == c {
		delete(g.calls, key)
	}
}

// join counts a caller waiting for c until its ctx is done or it leaves. Once
// none is left before c finishes, c is forgotten and its context canceled. The
// returned function is the caller leaving. g.mu must be held.
func (g *Group[T]) join(ctx context.Context, key string, c *call[T]) func() {
	c.waiting++

	stop := context.AfterFunc(ctx, func() { g.leave(key, c) })

	// stop can win against ctx's cancel and keep leave from running, so run it here.
	return func() {
		if stop() {
			g.leave(key, c)
		}
	}
}

func (g *Group[T]) leave(key string, c *call[T]) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if c.finished {
		return
	}

	if c.waiting--; c.waiting > 0 {
		return
	}

	if g.calls[key] == c {
		delete(g.calls, key)
	}

	c.cancel()
}
