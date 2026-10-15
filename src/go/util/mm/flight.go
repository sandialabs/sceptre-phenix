package mm

import (
	"context"
	"errors"
	"sync"
)

// errFlightAborted is what the callers that joined a run get when the run
// panicked (or otherwise never returned), instead of a zero value that would
// read as an empty but successful answer.
var errFlightAborted = errors.New("shared minimega query aborted before returning a result")

// flightGroup merges concurrent identical reads into one run, so several
// callers asking minimega the same question at once (the VMs page for a few
// users, the websocket VM list, state of health) cost one set of commands.
//
// Unlike golang.org/x/sync/singleflight, a call only joins a run that has not
// yet sent its first minimega command. A run that has already started may have
// read minimega before the joining caller's own previous action (starting a VM,
// say) took effect, so joining it could hand back an answer older than the
// call. Nothing is cached once a run finishes.
type flightGroup[T any] struct {
	mu      sync.Mutex
	flights map[string]*flight[T]
}

type flight[T any] struct {
	done chan struct{}
	val  T
	err  error // errFlightAborted if fn did not return; set before done is closed

	// cancel cancels the run's context; waiting counts the callers still
	// waiting for the run, whose context is not done. Both are guarded by
	// the group's mu.
	cancel  context.CancelFunc
	waiting int
}

// do runs fn, or waits for and returns the result of an identical run that
// has not started yet. fn must call seal just before it sends its first
// minimega command (see mmcli.RunStarted); seal may be called more than once.
//
// fn runs with a context of its own, canceled once every caller of the run
// has stopped waiting for it because the caller's ctx is done, so a run no
// one waits for gives up its queued commands (see mmcli.RunContext). A caller
// joining a run returns ctx's error as soon as ctx is done; the caller
// running fn returns once fn does.
//
// Every caller receives the same value, so callers must not modify it and
// should hand their own callers a copy.
//
// If fn panics, the panic carries on up the caller that ran it, and every
// caller that joined the run gets errFlightAborted (and a zero value). The
// error is nil whenever fn returned.
func (g *flightGroup[T]) do( //nolint:ireturn // T is concrete at each use
	ctx context.Context,
	key string,
	fn func(ctx context.Context, seal func()) T,
) (T, error) {
	g.mu.Lock()

	if g.flights == nil {
		g.flights = make(map[string]*flight[T])
	}

	if f, ok := g.flights[key]; ok {
		stop := g.join(ctx, key, f)

		g.mu.Unlock()

		defer stop()

		select {
		case <-f.done:
			return f.val, f.err
		case <-ctx.Done():
			var zero T

			return zero, ctx.Err() //nolint:wrapcheck // the caller's own context
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())

	f := &flight[T]{done: make(chan struct{}), cancel: cancel} //nolint:exhaustruct // val is set by fn below

	g.flights[key] = f
	stop := g.join(ctx, key, f)

	g.mu.Unlock()

	defer stop()

	var once sync.Once

	seal := func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()

			if g.flights[key] == f {
				delete(g.flights, key)
			}
		})
	}

	completed := false

	// Release anyone waiting even if fn panics, telling them it did.
	defer func() {
		if !completed {
			f.err = errFlightAborted
		}

		seal()
		cancel()
		close(f.done)
	}()

	f.val = fn(runCtx, seal)
	completed = true

	return f.val, nil
}

// join counts a caller waiting for f until the caller's ctx is done. Once no
// caller is left waiting, f is sealed, so no later caller joins a run no one
// waits for, and its context is canceled. The returned function stops the
// count's watch on ctx, once the caller is done waiting. g.mu must be held.
func (g *flightGroup[T]) join(ctx context.Context, key string, f *flight[T]) func() bool {
	f.waiting++

	return context.AfterFunc(ctx, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if f.waiting--; f.waiting > 0 {
			return
		}

		if g.flights[key] == f {
			delete(g.flights, key)
		}

		f.cancel()
	})
}
