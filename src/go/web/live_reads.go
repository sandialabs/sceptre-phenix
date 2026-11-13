package web

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"phenix/util/plog"
)

const (
	// liveReadWait is how long a list waits for minimega's answer about one
	// running experiment before it answers from what it knew last. The read
	// goes on after the wait (see liveReads).
	liveReadWait = 2 * time.Second

	// liveReadTimeout is how long a read of a liveReads may run in all, after
	// every caller stopped waiting.
	liveReadTimeout = 60 * time.Second
)

// errLiveReadPanic is the error of a read of a liveReads that panicked.
var errLiveReadPanic = errors.New("the read of minimega panicked")

// errLiveReadWaitPassed is the cause of a waitContext ending at its deadline.
var errLiveReadWaitPassed = errors.New("the wait for minimega passed")

// liveReads runs one read of minimega at a time for each key, such as an
// experiment's name. A caller waits for the read at most wait. The read goes
// on to its end after every caller stopped waiting, so minimega's answer is
// not abandoned, and a caller that comes meanwhile waits for the same read
// rather than send another command.
type liveReads[T any] struct {
	mu    sync.Mutex
	reads map[string]*liveRead[T]
	wait  time.Duration
}

// liveRead is one read of a liveReads, and its result once done is closed.
// err is set only when the read panicked.
type liveRead[T any] struct {
	done chan struct{}
	val  T
	err  error
}

func newLiveReads[T any]() *liveReads[T] {
	return &liveReads[T]{mu: sync.Mutex{}, reads: map[string]*liveRead[T]{}, wait: liveReadWait}
}

// get starts read for key, unless a read for key runs, and waits for it until
// ctx is done or the wait passes. It reports whether it got the read's result,
// and gives errLiveReadPanic when the read panicked. read gets ctx without its
// cancel and deadline, so the wait does not end it, but with a deadline
// liveReadTimeout away.
func (l *liveReads[T]) get(ctx context.Context, key string, read func(context.Context) T) (T, bool, error) {
	l.mu.Lock()

	r, running := l.reads[key]
	if !running {
		r = &liveRead[T]{done: make(chan struct{})} //nolint:exhaustruct // val and err are set by the read

		l.reads[key] = r

		go l.run(ctx, key, r, read)
	}

	wait := l.wait

	l.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-r.done:
		return r.val, true, r.err
	case <-timer.C:
	case <-ctx.Done():
	}

	var zero T

	return zero, false, nil
}

// run runs read for key into r, then ends r. A panic in read is r's error.
func (l *liveReads[T]) run(ctx context.Context, key string, r *liveRead[T], read func(context.Context) T) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveReadTimeout)

	defer func() {
		cancel()

		if p := recover(); p != nil {
			r.err = fmt.Errorf("%w: %v", errLiveReadPanic, p)

			plog.Error(plog.TypeSystem, "read of minimega panicked", "key", key, "panic", p, "stack", string(debug.Stack()))
		}

		l.mu.Lock()
		delete(l.reads, key)
		l.mu.Unlock()

		close(r.done)
	}()

	r.val = read(readCtx)
}

// setWait changes how long get waits.
func (l *liveReads[T]) setWait(wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.wait = wait
}

// waitContext gives ctx with a deadline one wait away, for all the reads of
// one list, so the list waits at most one wait in all (see waitPassed).
func (l *liveReads[T]) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	l.mu.Lock()
	wait := l.wait
	l.mu.Unlock()

	return context.WithTimeoutCause(ctx, wait, errLiveReadWaitPassed)
}

// waitPassed reports whether ctx, from waitContext, ended because its wait
// passed, not because the context it came from ended.
func waitPassed(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errLiveReadWaitPassed)
}

// running reports how many reads run now.
func (l *liveReads[T]) running() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.reads)
}
