package health

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/util/polltest"
)

// testClock is a clock tests move by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// withClock makes the registry's TTLs follow a test clock.
func withClock(r *Registry) *testClock {
	c := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	for _, e := range r.entries {
		e.now = c.Now
	}

	return c
}

// counted is a check of one key, "test:value", that counts its runs and
// returns status, after waiting for release when it is not nil.
func counted(runs *atomic.Int32, status Status, release <-chan struct{}) Check {
	return Check{
		Name:    "test",
		Keys:    []Key{{Name: "test:value", ComponentID: "test", ComponentType: TypeComponent}},
		Timeout: time.Second,
		TTL:     time.Minute,
		Run: func(context.Context) map[string][]Result {
			runs.Add(1)

			if release != nil {
				<-release
			}

			return map[string][]Result{"test:value": {{
				ObservedValue: 1,
				Status:        status,
			}}}
		},
	}
}

func TestWorst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, want Status
	}{
		{Pass, Pass, Pass},
		{Pass, Warn, Warn},
		{Warn, Pass, Warn},
		{Warn, Fail, Fail},
		{Fail, Pass, Fail},
		{Pass, Status("unknown"), Fail},
	}

	for _, test := range tests {
		if got := Worst(test.a, test.b); got != test.want {
			t.Errorf("Worst(%s, %s) = %s, want %s", test.a, test.b, got, test.want)
		}
	}
}

// A run is reused until its TTL passes.
func TestEvaluateReusesRunsWithinTTL(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32

	r := NewRegistry(counted(&runs, Pass, nil))
	clock := withClock(r)

	r.Evaluate(t.Context())
	clock.Add(59 * time.Second)

	if got := r.Evaluate(t.Context()).Status; got != Pass {
		t.Errorf("Evaluate() within the TTL: status = %s, want %s", got, Pass)
	}

	if got := runs.Load(); got != 1 {
		t.Errorf("Evaluate() within the TTL: %d runs, want 1", got)
	}

	clock.Add(time.Second)
	r.Evaluate(t.Context())

	if got := runs.Load(); got != 2 {
		t.Errorf("Evaluate() after the TTL: %d runs, want 2", got)
	}
}

// Concurrent requests share one run.
func TestEvaluateRunsOnceForConcurrentRequests(t *testing.T) {
	t.Parallel()

	var (
		runs    atomic.Int32
		release = make(chan struct{})
		r       = NewRegistry(counted(&runs, Warn, release))
		wg      sync.WaitGroup
	)

	statuses := make(chan Status, 50)

	for range cap(statuses) {
		wg.Add(1)

		go func() {
			defer wg.Done()

			statuses <- r.Evaluate(t.Context()).Status
		}()
	}

	polltest.Until(t, "the check runs", func() bool { return runs.Load() > 0 })
	close(release)
	wg.Wait()
	close(statuses)

	for status := range statuses {
		if status != Warn {
			t.Errorf("Evaluate() status = %s, want %s", status, Warn)
		}
	}

	if got := runs.Load(); got != 1 {
		t.Errorf("Evaluate() concurrently: %d runs, want 1", got)
	}
}

// A run that outlasts its timeout fails every key, and that failure is
// reused, without another run, until the run ends with its own results.
func TestEvaluateTimesOut(t *testing.T) {
	t.Parallel()

	var (
		runs    atomic.Int32
		release = make(chan struct{})
		check   = counted(&runs, Pass, release)
	)

	check.Timeout = 20 * time.Millisecond
	check.Keys = append(check.Keys, Key{Name: "test:other", ComponentID: "test", ComponentType: TypeComponent})

	r := NewRegistry(check)
	withClock(r)

	start := time.Now()
	got := r.Evaluate(t.Context())

	if took := time.Since(start); took > time.Second {
		t.Errorf("Evaluate() answered after %s, want about the timeout", took)
	}

	failure := []Result{{
		ComponentID:   "test",
		ComponentType: TypeComponent,
		Status:        Fail,
		Time:          "2026-01-01T00:00:00Z",
		Output:        "timed out after 20ms",
	}}
	want := Response{Status: Fail, Checks: map[string][]Result{"test:value": failure, "test:other": failure}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Evaluate() of a hung check mismatch (-want +got):\n%s", diff)
	}

	if got := r.Evaluate(t.Context()).Status; got != Fail {
		t.Errorf("Evaluate() while the run hangs: status = %s, want %s", got, Fail)
	}

	if got := runs.Load(); got != 1 {
		t.Errorf("Evaluate() while the run hangs: %d runs, want 1", got)
	}

	close(release)

	polltest.Until(t, "the late run's results are cached", func() bool {
		return r.Evaluate(t.Context()).Status == Pass
	})

	if got := runs.Load(); got != 1 {
		t.Errorf("Evaluate() after the late run: %d runs, want 1", got)
	}
}

// A request that ends while it waits does not make the check fail for the
// requests after it.
func TestEvaluateRequestEndDoesNotCacheFailure(t *testing.T) {
	t.Parallel()

	var (
		runs    atomic.Int32
		release = make(chan struct{})
		r       = NewRegistry(counted(&runs, Pass, release))
	)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if got := r.Evaluate(ctx).Status; got != Fail {
		t.Errorf("Evaluate(ended request) status = %s, want %s", got, Fail)
	}

	close(release)

	if got := r.Evaluate(t.Context()).Status; got != Pass {
		t.Errorf("Evaluate(next request) status = %s, want %s", got, Pass)
	}

	if got := runs.Load(); got != 1 {
		t.Errorf("Evaluate() of two requests: %d runs, want 1", got)
	}
}

func TestEvaluateReportsPanicAsFailure(t *testing.T) {
	t.Parallel()

	for name, timeout := range map[string]time.Duration{"cached": time.Second, "per request": 0} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := NewRegistry(Check{
				Name:    "test",
				Keys:    []Key{{Name: "test:value", ComponentID: "", ComponentType: ""}},
				Timeout: timeout,
				Run:     func(context.Context) map[string][]Result { panic("boom") },
			})
			withClock(r)

			want := Response{Status: Fail, Checks: map[string][]Result{"test:value": {{
				Status: Fail,
				Time:   "2026-01-01T00:00:00Z",
				Output: "check panicked: boom",
			}}}}

			if diff := cmp.Diff(want, r.Evaluate(t.Context())); diff != "" {
				t.Errorf("Evaluate() of a panicking check mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEvaluateSkipsExcludedAndDisabledChecks(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32

	failing := counted(&runs, Fail, nil)
	disabled := counted(&runs, Fail, nil)
	disabled.Name = "disabled"
	disabled.Enabled = func() bool { return false }

	r := NewRegistry(failing, disabled)

	want := Response{Status: Pass, Checks: map[string][]Result{}}

	if diff := cmp.Diff(want, r.Evaluate(t.Context(), "test")); diff != "" {
		t.Errorf("Evaluate(exclude test) mismatch (-want +got):\n%s", diff)
	}

	if got := runs.Load(); got != 0 {
		t.Errorf("Evaluate(exclude test): %d runs, want 0", got)
	}
}
