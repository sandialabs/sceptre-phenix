package mm

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

//nolint:paralleltest // counts the callers waiting in any flightGroup
func TestFlightGroupMergesCallsBeforeSeal(t *testing.T) {
	const callers = 5

	var (
		group   flightGroup[int]
		runs    atomic.Int32
		release = make(chan struct{})
		wg      sync.WaitGroup
		results = make([]int, callers)
	)

	fn := func(seal func()) int {
		runs.Add(1)
		<-release
		seal()

		return 42
	}

	// Let every caller finish even if the test fails first, so none is left
	// waiting on the run when a later test counts the callers waiting.
	unblock := sync.OnceFunc(func() { close(release) })

	t.Cleanup(func() {
		unblock()
		wg.Wait()
	})

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i] = value(group.do("k", fn))
		}()
	}

	// Whichever caller arrives first runs fn; the rest wait for its result.
	waitForJoined(t, callers-1)

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
	if got := value(group.do("k", func(func()) int { return 7 })); got != 7 {
		t.Fatalf("call after the run got %d, want its own result 7", got)
	}
}

func TestFlightGroupRunsCallAlone(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		seal bool   // whether the pending run has sent its first command
		key  string // the second call's key
	}{
		"while an identical run has started": {seal: true, key: "k"},
		"while a run for another key waits":  {seal: false, key: "other"},
	} {
		var (
			group   flightGroup[int]
			entered = make(chan struct{})
			release = make(chan struct{})
			first   = make(chan int, 1)
			second  = make(chan int, 1)
		)

		go func() {
			first <- value(group.do("k", func(seal func()) int {
				if tc.seal {
					seal()
				}

				close(entered)
				<-release

				return 1
			}))
		}()

		<-entered

		// A started run may have read minimega before this call was made, and a
		// run for another key answers another question: the call runs alone.
		go func() { second <- value(group.do(tc.key, func(func()) int { return 2 })) }()

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

//nolint:paralleltest // counts the callers waiting in any flightGroup
func TestFlightGroupReleasesWaitersOnPanic(t *testing.T) {
	type result struct {
		val int
		err error
	}

	var (
		group    flightGroup[int]
		entered  = make(chan struct{})
		release  = make(chan struct{})
		joined   = make(chan result, 1)
		panicked = make(chan any, 1)
	)

	// Let the run panic even if the test fails first, so its waiter is not
	// left waiting when a later test counts the callers waiting.
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)

	go func() {
		defer func() { panicked <- recover() }()

		_, _ = group.do("k", func(func()) int {
			close(entered)
			<-release
			panic("boom")
		})
	}()

	<-entered

	go func() {
		val, err := group.do("k", func(func()) int { return 7 })
		joined <- result{val: val, err: err}
	}()

	waitForJoined(t, 1)
	unblock()

	got := <-joined
	if !errors.Is(got.err, errFlightAborted) {
		t.Fatalf("waiter got (%d, %v), want errFlightAborted", got.val, got.err)
	}

	if got.val != 0 {
		t.Fatalf("waiter got %d alongside the error, want the zero value", got.val)
	}

	// The panic still reaches the caller whose run it was.
	if r := <-panicked; r != "boom" {
		t.Fatalf("leader recovered %v, want the original panic", r)
	}

	// The key is free again: the next call runs on its own and succeeds.
	val, err := group.do("k", func(func()) int { return 9 })
	if err != nil || val != 9 {
		t.Fatalf("call after the panic got (%d, %v), want (9, nil)", val, err)
	}
}

// value returns a flightGroup result, panicking (which fails the test, even
// from another goroutine) if the run aborted.
func value[T any](val T, err error) T { //nolint:ireturn // T is concrete at each use
	if err != nil {
		panic(fmt.Sprintf("unexpected flight error: %v", err))
	}

	return val
}
