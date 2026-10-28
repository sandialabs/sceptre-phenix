package health

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Check is one health check, reporting one or more check keys.
type Check struct {
	// Name identifies the check, for ?exclude=.
	Name string
	// Keys are the check keys Run reports, each given a failure when the run
	// times out or panics.
	Keys []Key
	// Timeout bounds a run. A check with none runs on the request's goroutine
	// every time, and must be quick (it reads memory only).
	Timeout time.Duration
	// TTL is how long a finished run is reused.
	TTL time.Duration
	// Enabled reports whether the check runs; nil means always.
	Enabled func() bool
	// Run observes the check's values. Its context ends at Timeout.
	Run func(ctx context.Context) map[string][]Result
}

// Key is a check key and the component it observes.
type Key struct {
	Name          string
	ComponentID   string
	ComponentType string
}

// Registry evaluates a set of checks.
type Registry struct {
	entries []*entry
}

// entry is a check with its last result and its run in flight, if any. At
// most one run of a check is outstanding at a time, however many requests
// wait on it, so a probe flood cannot multiply the work, and a check that
// hangs (a minimega that never answers) holds one goroutine, not one
// per request.
type entry struct {
	check Check
	now   func() time.Time // the clock of the TTL, replaced by tests

	mu       sync.Mutex
	last     map[string][]Result
	at       time.Time     // when last was stored
	inflight chan struct{} // closed when the run in flight ends
	started  time.Time     // when the run in flight started
}

// NewRegistry returns a registry of the checks.
func NewRegistry(checks ...Check) *Registry {
	r := &Registry{entries: make([]*entry, 0, len(checks))}

	for _, c := range checks {
		r.entries = append(r.entries, &entry{check: c, now: time.Now}) //nolint:exhaustruct // zero state
	}

	return r
}

// Evaluate runs the checks not excluded by name, concurrently, and returns
// their merged results. The status is the worst of every result's, or pass
// when there are none.
func (r *Registry) Evaluate(ctx context.Context, exclude ...string) Response {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		checks  = make(map[string][]Result)
		overall = Pass
	)

	for _, e := range r.entries {
		if slices.Contains(exclude, e.check.Name) {
			continue
		}

		if e.check.Enabled != nil && !e.check.Enabled() {
			continue
		}

		wg.Add(1)

		go func() {
			defer wg.Done()

			results := e.get(ctx)

			mu.Lock()
			defer mu.Unlock()

			for key, rs := range results {
				checks[key] = append(checks[key], rs...)

				for _, result := range rs {
					overall = Worst(overall, result.Status)
				}
			}
		}()
	}

	wg.Wait()

	return Response{ //nolint:exhaustruct // the handler fills in the service fields
		Status: overall,
		Checks: checks,
	}
}

// get returns the check's results: the last ones while they are younger than
// the TTL, otherwise those of a run, started unless one is in flight. A run
// that outlasts the timeout is reported, and cached, as a failure of every
// key; when it does end, its own results replace that failure.
func (e *entry) get(ctx context.Context) map[string][]Result {
	if e.check.Timeout <= 0 {
		return e.safeRun(ctx)
	}

	e.mu.Lock()

	if e.last != nil && e.now().Sub(e.at) < e.check.TTL {
		last := e.last

		e.mu.Unlock()

		return last
	}

	done := e.inflight
	if done == nil {
		done = make(chan struct{})

		e.inflight, e.started = done, time.Now()

		go e.run(done)
	}

	started := e.started

	e.mu.Unlock()

	timedOut := time.After(e.check.Timeout - time.Since(started))

	select {
	case <-done:
		e.mu.Lock()
		defer e.mu.Unlock()

		return e.last
	case <-timedOut:
		e.mu.Lock()
		defer e.mu.Unlock()

		// the run may have ended as the timer fired, with results of its own
		if e.inflight != done {
			return e.last
		}

		failed := e.failure(fmt.Sprintf("timed out after %s", e.check.Timeout))
		e.last, e.at = failed, e.now()

		return failed
	case <-ctx.Done():
		// the run goes on, and caches what it finds, for the next request
		return e.failure("request ended before the check: " + ctx.Err().Error())
	}
}

// run runs the check on a context of its own, so that a request that ends
// cannot make it fail, and caches its results.
func (e *entry) run(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), e.check.Timeout)
	defer cancel()

	results := e.safeRun(ctx)

	e.mu.Lock()
	e.last, e.at, e.inflight = results, e.now(), nil
	e.mu.Unlock()

	close(done)
}

// safeRun runs the check, reporting a panic as a failure of every key, and
// stamps results that lack a time with the time it ended.
func (e *entry) safeRun(ctx context.Context) map[string][]Result {
	results, panicked := e.recoverRun(ctx)
	if panicked != nil {
		return e.failure(fmt.Sprintf("check panicked: %v", panicked))
	}

	now := e.now().UTC().Format(time.RFC3339)

	for key, rs := range results {
		for i := range rs {
			if rs[i].Time == "" {
				rs[i].Time = now
			}
		}

		results[key] = rs
	}

	return results
}

// recoverRun runs the check, returning what it panicked with, if it did.
func (e *entry) recoverRun(ctx context.Context) (map[string][]Result, any) {
	var panicked any

	results := func() map[string][]Result {
		defer func() { panicked = recover() }()

		return e.check.Run(ctx)
	}()

	return results, panicked
}

// failure is a failure of every key of the check, for the reason given.
func (e *entry) failure(output string) map[string][]Result {
	var (
		now     = e.now().UTC().Format(time.RFC3339)
		results = make(map[string][]Result, len(e.check.Keys))
	)

	for _, key := range e.check.Keys {
		results[key.Name] = []Result{{ //nolint:exhaustruct // no value was observed
			ComponentID:   key.ComponentID,
			ComponentType: key.ComponentType,
			Status:        Fail,
			Time:          now,
			Output:        output,
		}}
	}

	return results
}
