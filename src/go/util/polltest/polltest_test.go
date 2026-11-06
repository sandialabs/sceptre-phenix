package polltest

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// recorder is a [testing.TB] that records a fatal message and then stops its
// goroutine, as a real test does.
type recorder struct {
	testing.TB

	fatal string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)

	runtime.Goexit()
}

// run calls fn with a new recorder on its own goroutine and returns the fatal
// message, or "" when fn ends without a failure.
func run(t *testing.T, fn func(r *recorder)) string {
	t.Helper()

	r := &recorder{TB: t, fatal: ""}
	done := make(chan struct{})

	go func() {
		defer close(done)

		fn(r)
	}()

	<-done

	return r.fatal
}

// Until returns when its condition holds, and fails with the condition's name
// when it does not hold in time.
func TestUntil(t *testing.T) {
	t.Parallel()

	var polls atomic.Int32

	holdsLater := func() bool { return polls.Add(1) >= 3 }

	tests := map[string]struct {
		cond func() bool
		want string
	}{
		"holds at once": {cond: func() bool { return true }, want: ""},
		"holds later":   {cond: holdsLater, want: ""},
		"never holds":   {cond: func() bool { return false }, want: "timed out waiting until the flag is set"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := run(t, func(r *recorder) { until(r, "the flag is set", tc.cond, 20*time.Millisecond) })

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("until() fatal message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Within returns when its work returns, and fails with the work's name and
// the timeout when the work takes too long.
func TestWithin(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	tests := map[string]struct {
		fn   func()
		want string
	}{
		"returns":     {fn: func() {}, want: ""},
		"never ready": {fn: func() { <-release }, want: "the work took longer than 20ms"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := run(t, func(r *recorder) { within(r, "the work", tc.fn, 20*time.Millisecond) })

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("within() fatal message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The exported Until and Within return without a failure when the condition
// holds or the work returns, after polling or running it.
func TestExportedWaits(t *testing.T) {
	t.Parallel()

	t.Run("Until", func(t *testing.T) {
		t.Parallel()

		var polls atomic.Int32

		got := run(t, func(r *recorder) { Until(r, "the third poll", func() bool { return polls.Add(1) >= 3 }) })

		if diff := cmp.Diff("", got); diff != "" {
			t.Errorf("Until() fatal message mismatch (-want +got):\n%s", diff)
		}

		if n := polls.Load(); n != 3 {
			t.Errorf("Until() polled %d times, want 3", n)
		}
	})

	t.Run("Within", func(t *testing.T) {
		t.Parallel()

		var ran atomic.Bool

		got := run(t, func(r *recorder) { Within(r, "the work", func() { ran.Store(true) }) })

		if diff := cmp.Diff("", got); diff != "" {
			t.Errorf("Within() fatal message mismatch (-want +got):\n%s", diff)
		}

		if !ran.Load() {
			t.Error("Within() returned without running its work")
		}
	})
}
