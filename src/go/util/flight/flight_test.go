package flight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phenix/util/polltest"
)

func TestGroupMergesCallsBeforeSeal(t *testing.T) {
	t.Parallel()

	const callers = 5

	var (
		group   Group[int]
		runs    atomic.Int32
		release = make(chan struct{})
		wg      sync.WaitGroup
		results = make([]int, callers)
	)

	fn := func(ctx context.Context, seal func()) (int, error) {
		runs.Add(1)
		<-release
		seal()

		return 42, ctx.Err() // nil: every caller still waits
	}

	// Let every caller finish even if the test fails first.
	unblock := sync.OnceFunc(func() { close(release) })

	t.Cleanup(func() {
		unblock()
		wg.Wait()
	})

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i] = value(group.Do(context.Background(), "k", fn))
		}()
	}

	// Whichever caller arrives first runs fn; the rest wait for its result.
	polltest.Until(t, "every caller waits for the run", func() bool { return group.Waiting("k") == callers })

	unblock()
	wg.Wait()

	if got := runs.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}

	for i, got := range results {
		if got != 42 {
			t.Fatalf("caller %d got %d, want 42", i, got)
		}
	}

	// Nothing is kept once the run is done: the next call runs on its own.
	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 7, nil })); got != 7 {
		t.Fatalf("call after the run got %d, want its own result 7", got)
	}
}

func TestGroupRunsCallAlone(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		seal bool   // whether the pending run has sent its first command
		key  string // the second call's key
	}{
		"while an identical run has started": {seal: true, key: "k"},
		"while a run for another key waits":  {seal: false, key: "other"},
	} {
		var (
			group   Group[int]
			entered = make(chan struct{})
			release = make(chan struct{})
			first   = make(chan int, 1)
			second  = make(chan int, 1)
		)

		go func() {
			first <- value(group.Do(context.Background(), "k", func(_ context.Context, seal func()) (int, error) {
				if tc.seal {
					seal()
				}

				close(entered)
				<-release

				return 1, nil
			}))
		}()

		<-entered

		// A started run may have read minimega before this call was made, and a
		// run for another key answers another question: the call runs alone.
		go func() {
			second <- value(group.Do(context.Background(), tc.key, func(context.Context, func()) (int, error) { return 2, nil }))
		}()

		select {
		case got := <-second:
			if got != 2 {
				t.Errorf("%s: call got %d, want its own result 2", name, got)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: call waited for the pending run", name)
		}

		close(release)

		if got := <-first; got != 1 {
			t.Errorf("%s: pending run got %d, want 1", name, got)
		}
	}
}

func TestGroupReleasesWaitersOnPanic(t *testing.T) {
	t.Parallel()

	type result struct {
		val int
		err error
	}

	var (
		group    Group[int]
		entered  = make(chan struct{})
		release  = make(chan struct{})
		joined   = make(chan result, 1)
		panicked = make(chan any, 1)
	)

	// Let the run panic even if the test fails first.
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)

	go func() {
		defer func() { panicked <- recover() }()

		_, _ = group.Do(context.Background(), "k", func(context.Context, func()) (int, error) {
			close(entered)
			<-release
			panic("boom")
		})
	}()

	<-entered

	go func() {
		val, err := group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 7, nil })
		joined <- result{val: val, err: err}
	}()

	polltest.Until(t, "the second caller joins the run", func() bool { return group.Waiting("k") == 2 })
	unblock()

	got := <-joined
	if !errors.Is(got.err, ErrAborted) {
		t.Fatalf("waiter got (%d, %v), want ErrAborted", got.val, got.err)
	}

	if got.val != 0 {
		t.Fatalf("waiter got %d alongside the error, want the zero value", got.val)
	}

	// The panic still reaches the caller whose run it was.
	if r := <-panicked; r != "boom" {
		t.Fatalf("leader recovered %v, want the original panic", r)
	}

	// The key is free again: the next call runs on its own and succeeds.
	val, err := group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 9, nil })
	if err != nil || val != 9 {
		t.Fatalf("call after the panic got (%d, %v), want (9, nil)", val, err)
	}
}

// A caller whose context ends while it waits for another's run returns at
// once, and the run goes on for the callers still waiting. Once none is left,
// the run's context is canceled, and a later call runs on its own.
func TestGroupCancelsARunNoOneWaitsFor(t *testing.T) {
	t.Parallel()

	var (
		group      Group[int]
		running    = make(chan context.Context, 1)
		leaderDone = make(chan int, 1)
		joinedDone = make(chan error, 1)

		leaderCtx, cancelLeader = context.WithCancel(context.Background())
		joinedCtx, cancelJoined = context.WithCancel(context.Background())
	)

	t.Cleanup(cancelLeader)
	t.Cleanup(cancelJoined)

	go func() {
		leaderDone <- value(group.Do(leaderCtx, "k", func(ctx context.Context, _ func()) (int, error) {
			running <- ctx
			<-ctx.Done()

			return 1, nil
		}))
	}()

	runCtx := <-running

	go func() {
		_, err := group.Do(joinedCtx, "k", func(context.Context, func()) (int, error) { return 2, nil })
		joinedDone <- err
	}()

	polltest.Until(t, "the second caller joins the run", func() bool { return group.Waiting("k") == 2 })
	cancelJoined()

	if err := <-joinedDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("the caller that left got %v, want %v", err, context.Canceled)
	}

	if err := runCtx.Err(); err != nil {
		t.Fatalf("the run's context ended (%v) while its leader still waited", err)
	}

	cancelLeader()

	if got := <-leaderDone; got != 1 {
		t.Fatalf("leader got %d, want its run's 1", got)
	}

	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 3, nil })); got != 3 {
		t.Fatalf("call after the canceled run got %d, want its own result 3", got)
	}
}

// A forgotten run still answers the callers waiting for it, while later
// callers start a new one.
func TestGroupForget(t *testing.T) {
	t.Parallel()

	var (
		group   Group[int]
		entered = make(chan struct{})
		release = make(chan struct{})
		first   = make(chan int, 1)
	)

	go func() {
		first <- value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) {
			close(entered)
			<-release

			return 1, nil
		}))
	}()

	<-entered
	group.Forget("k")

	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 2, nil })); got != 2 {
		t.Errorf("call after Forget got %d, want its own result 2", got)
	}

	close(release)

	if got := <-first; got != 1 {
		t.Errorf("forgotten run got %d, want 1", got)
	}
}

// value returns a Group result, panicking (which fails the test, even
// from another goroutine) if the run aborted.
func value[T any](val T, err error) T { //nolint:ireturn // T is concrete at each use
	if err != nil {
		panic(fmt.Sprintf("unexpected flight error: %v", err))
	}

	return val
}
