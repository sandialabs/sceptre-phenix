package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
)

// A read goes on after its caller stops waiting and after the caller's context
// ends, but it has a deadline liveReadTimeout away.
func TestLiveReadsBoundsTheDetachedRead(t *testing.T) {
	t.Parallel()

	reads := newLiveReads[int]()
	reads.setWait(time.Millisecond)

	var (
		got     = make(chan context.Context, 1)
		release = make(chan struct{})
	)

	ctx, cancel := context.WithCancel(t.Context())
	start := time.Now()

	if _, ok, err := reads.get(ctx, "exp", func(ctx context.Context) int {
		got <- ctx
		<-release

		return 1
	}); ok || err != nil {
		t.Errorf("get() of a read that has not ended = %t, %v; want false, nil", ok, err)
	}

	cancel()

	readCtx := <-got

	if err := readCtx.Err(); err != nil {
		t.Errorf("the read's context after the caller's ended has error %v, want none", err)
	}

	deadline, ok := readCtx.Deadline()
	if !ok || deadline.Before(start) || deadline.After(time.Now().Add(liveReadTimeout)) {
		t.Errorf("the read's context deadline = %v, %t; want one about %v away", deadline, ok, liveReadTimeout)
	}

	close(release)
	polltest.Until(t, "the read ends", func() bool { return reads.running() == 0 })
}

// A read that panics gives its callers errLiveReadPanic, is logged, and frees
// its key for the next read.
//
//nolint:paralleltest // captures the process-wide log
func TestLiveReadsRecoversAPanic(t *testing.T) {
	logs := plogtest.Capture(t)
	reads := newLiveReads[int]()

	_, ok, err := reads.get(t.Context(), "exp", func(context.Context) int { panic("boom") })
	if !ok || !errors.Is(err, errLiveReadPanic) {
		t.Errorf("get() of a read that panics = %t, %v; want true, %v", ok, err, errLiveReadPanic)
	}

	if n := reads.running(); n != 0 {
		t.Errorf("reads running after a panic = %d, want 0", n)
	}

	got, ok, err := reads.get(t.Context(), "exp", func(context.Context) int { return 2 })
	if got != 2 || !ok || err != nil {
		t.Errorf("get() after a read panicked = %d, %t, %v; want 2, true, nil", got, ok, err)
	}

	panics := logs.Records(t, func(record map[string]any) bool {
		return record["level"] == "ERROR" && record["panic"] == "boom" && record["key"] == "exp"
	})
	if len(panics) != 1 {
		t.Errorf("logged %d errors for the panic, want 1", len(panics))
	}
}
