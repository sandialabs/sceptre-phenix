package flight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
		t.Errorf("runs of fn for %d Do(k) calls = %d, want 1", callers, got)
	}

	if diff := cmp.Diff([]int{42, 42, 42, 42, 42}, results); diff != "" {
		t.Errorf("Do(k) results of the merged callers mismatch (-want +got):\n%s", diff)
	}

	// Nothing is kept once the run is done: the next call runs on its own.
	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 7, nil })); got != 7 {
		t.Errorf("Do(k) after the run = %d, want its own result 7", got)
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
		t.Run(name, func(t *testing.T) {
			t.Parallel()

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
					t.Errorf("Do(%s) = %d, want its own result 2", tc.key, got)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("Do(%s) waited for the pending run of k", tc.key)
			}

			close(release)

			if got := <-first; got != 1 {
				t.Errorf("Do(k) of the pending run = %d, want 1", got)
			}
		})
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
		t.Errorf("Do(k) of the waiter when the run panics error = %v, want %v", got.err, ErrAborted)
	}

	if got.val != 0 {
		t.Errorf("Do(k) of the waiter when the run panics = %d, want the zero value", got.val)
	}

	// The panic still reaches the caller whose run it was.
	if r := <-panicked; r != "boom" {
		t.Errorf("Do(k) of the leader recovered %v, want the original panic %q", r, "boom")
	}

	// The key is free again: the next call runs on its own and succeeds.
	val, err := group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 9, nil })
	if err != nil || val != 9 {
		t.Errorf("Do(k) after the panic = %d, %v; want 9, nil", val, err)
	}
}

// A caller whose context ends returns at once while the run goes on for those still waiting; once
// none is left, the run's context is canceled, and a later call runs on its own.
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
		t.Errorf("Do(k) of the caller that left error = %v, want %v", err, context.Canceled)
	}

	if err := runCtx.Err(); err != nil {
		t.Errorf("run context error while its leader waits = %v, want nil", err)
	}

	cancelLeader()

	if got := <-leaderDone; got != 1 {
		t.Errorf("Do(k) of the leader = %d, want its run's 1", got)
	}

	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 3, nil })); got != 3 {
		t.Errorf("Do(k) after the canceled run = %d, want its own result 3", got)
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
		t.Errorf("Do(k) after Forget(k) = %d, want its own result 2", got)
	}

	close(release)

	if got := <-first; got != 1 {
		t.Errorf("Do(k) of the forgotten run = %d, want 1", got)
	}
}

// Every caller that joins a run gets the run's error, and the next call runs on its own.
func TestGroupSharesTheRunError(t *testing.T) {
	t.Parallel()

	var (
		group   Group[int]
		boom    = errors.New("boom")
		entered = make(chan struct{})
		release = make(chan struct{})
		errs    = make(chan error, 2)
	)

	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)

	go func() {
		_, err := group.Do(context.Background(), "k", func(context.Context, func()) (int, error) {
			close(entered)
			<-release

			return 5, boom
		})
		errs <- err
	}()

	<-entered

	go func() {
		_, err := group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 7, nil })
		errs <- err
	}()

	polltest.Until(t, "the second caller joins the run", func() bool { return group.Waiting("k") == 2 })
	unblock()

	for range 2 {
		if err := <-errs; !errors.Is(err, boom) {
			t.Errorf("Do(k) error of a caller of the failed run = %v, want %v", err, boom)
		}
	}

	if got := value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 3, nil })); got != 3 {
		t.Errorf("Do(k) after the failed run = %d, want its own result 3", got)
	}
}

// When the caller that started a run leaves, the run goes on for a caller still waiting, which gets its result.
func TestGroupKeepsARunSomeoneWaitsFor(t *testing.T) {
	t.Parallel()

	var (
		group      Group[int]
		running    = make(chan context.Context, 1)
		release    = make(chan struct{})
		leaderDone = make(chan int, 1)
		joinedDone = make(chan int, 1)

		leaderCtx, cancelLeader = context.WithCancel(context.Background())
	)

	t.Cleanup(cancelLeader)

	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)

	go func() {
		leaderDone <- value(group.Do(leaderCtx, "k", func(ctx context.Context, _ func()) (int, error) {
			running <- ctx
			<-release

			return 1, ctx.Err()
		}))
	}()

	runCtx := <-running

	go func() {
		joinedDone <- value(group.Do(context.Background(), "k", func(context.Context, func()) (int, error) { return 2, nil }))
	}()

	polltest.Until(t, "the second caller joins the run", func() bool { return group.Waiting("k") == 2 })
	cancelLeader()
	polltest.Until(t, "the leader leaves", func() bool { return group.Waiting("k") == 1 })

	if err := runCtx.Err(); err != nil {
		t.Errorf("run context error while a caller waits = %v, want nil", err)
	}

	unblock()

	if got := <-joinedDone; got != 1 {
		t.Errorf("Do(k) of the caller still waiting = %d, want the run's 1", got)
	}

	if got := <-leaderDone; got != 1 {
		t.Errorf("Do(k) of the leader = %d, want its run's 1", got)
	}
}

// Waiting is 0 and Forget does nothing for a key with no run, also on a zero Group, and after a run is over.
func TestGroupWithoutARun(t *testing.T) {
	t.Parallel()

	var group Group[string]

	group.Forget("k")

	if got := group.Waiting("k"); got != 0 {
		t.Errorf("Waiting(k) of a zero Group = %d, want 0", got)
	}

	got := value(group.Do(context.Background(), "k", func(context.Context, func()) (string, error) { return "a", nil }))
	if got != "a" {
		t.Errorf("Do(k) = %q, want %q", got, "a")
	}

	if got := group.Waiting("k"); got != 0 {
		t.Errorf("Waiting(k) after the run = %d, want 0", got)
	}

	if got := group.Waiting("other"); got != 0 {
		t.Errorf("Waiting(other) = %d, want 0", got)
	}
}

// Concurrent calls for many keys each get the result of a run for their own key.
func TestGroupKeepsKeysApart(t *testing.T) {
	t.Parallel()

	const keys, callersPerKey = 8, 4

	var (
		group Group[string]
		wg    sync.WaitGroup
		got   = make([][]string, keys)
	)

	for k := range keys {
		got[k] = make([]string, callersPerKey)

		for c := range callersPerKey {
			wg.Add(1)

			go func() {
				defer wg.Done()

				key := fmt.Sprintf("k%d", k)
				got[k][c] = value(group.Do(context.Background(), key, func(context.Context, func()) (string, error) {
					return key, nil
				}))
			}()
		}
	}

	wg.Wait()

	for k := range keys {
		want := fmt.Sprintf("k%d", k)
		if diff := cmp.Diff([]string{want, want, want, want}, got[k]); diff != "" {
			t.Errorf("Do(%s) results mismatch (-want +got):\n%s", want, diff)
		}
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
