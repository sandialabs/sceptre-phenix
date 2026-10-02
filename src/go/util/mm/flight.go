package mm

import (
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
}

// do runs fn, or waits for and returns the result of an identical run that
// has not started yet. fn must call seal just before it sends its first
// minimega command (see mmcli.RunStarted); seal may be called more than once.
//
// Every caller receives the same value, so callers must not modify it and
// should hand their own callers a copy.
//
// If fn panics, the panic carries on up the caller that ran it, and every
// caller that joined the run gets errFlightAborted (and a zero value). The
// error is nil whenever fn returned.
func (g *flightGroup[T]) do(key string, fn func(seal func()) T) (T, error) { //nolint:ireturn // T is concrete at each use
	g.mu.Lock()

	if g.flights == nil {
		g.flights = make(map[string]*flight[T])
	}

	if f, ok := g.flights[key]; ok {
		g.mu.Unlock()

		<-f.done

		return f.val, f.err
	}

	f := &flight[T]{done: make(chan struct{})} //nolint:exhaustruct // val is set by fn below

	g.flights[key] = f

	g.mu.Unlock()

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
		close(f.done)
	}()

	f.val = fn(seal)
	completed = true

	return f.val, nil
}
