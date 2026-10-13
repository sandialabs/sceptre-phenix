package health

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	if resp := r.Evaluate(t.Context()); resp.Status != Pass || runs.Load() != 1 {
		t.Fatalf("within the TTL: status %s after %d runs, want pass after 1", resp.Status, runs.Load())
	}

	clock.Add(time.Second)
	r.Evaluate(t.Context())

	if runs.Load() != 2 {
		t.Errorf("after the TTL: %d runs, want 2", runs.Load())
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
			t.Errorf("status = %s, want warn", status)
		}
	}

	if runs.Load() != 1 {
		t.Errorf("%d runs, want 1", runs.Load())
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
	resp := r.Evaluate(t.Context())

	if took := time.Since(start); took > time.Second {
		t.Errorf("answered after %s, want about the timeout", took)
	}

	if resp.Status != Fail {
		t.Errorf("status = %s, want fail", resp.Status)
	}

	for _, key := range []string{"test:value", "test:other"} {
		results := resp.Checks[key]
		if len(results) != 1 || results[0].Status != Fail || !strings.Contains(results[0].Output, "timed out") {
			t.Errorf("%s = %+v, want one timed out failure", key, results)
		}

		if results[0].ComponentID != "test" || results[0].ObservedValue != nil {
			t.Errorf("%s = %+v, want the component and no value", key, results)
		}
	}

	if resp := r.Evaluate(t.Context()); resp.Status != Fail || runs.Load() != 1 {
		t.Errorf("while the run hangs: status %s after %d runs, want fail after 1", resp.Status, runs.Load())
	}

	close(release)

	polltest.Until(t, "the late run's results are cached", func() bool {
		return r.Evaluate(t.Context()).Status == Pass
	})

	if runs.Load() != 1 {
		t.Errorf("%d runs, want 1", runs.Load())
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

	if resp := r.Evaluate(ctx); resp.Status != Fail {
		t.Errorf("ended request: status %s, want fail", resp.Status)
	}

	close(release)

	if resp := r.Evaluate(t.Context()); resp.Status != Pass {
		t.Errorf("next request: status %s, want pass", resp.Status)
	}

	if runs.Load() != 1 {
		t.Errorf("%d runs, want 1", runs.Load())
	}
}

func TestEvaluateReportsPanicAsFailure(t *testing.T) {
	t.Parallel()

	for name, timeout := range map[string]time.Duration{"cached": time.Second, "per request": 0} {
		r := NewRegistry(Check{
			Name:    "test",
			Keys:    []Key{{Name: "test:value", ComponentID: "", ComponentType: ""}},
			Timeout: timeout,
			Run:     func(context.Context) map[string][]Result { panic("boom") },
		})

		resp := r.Evaluate(t.Context())

		if results := resp.Checks["test:value"]; resp.Status != Fail || len(results) != 1 ||
			!strings.Contains(results[0].Output, "boom") {
			t.Errorf("%s: %+v, want a failure naming the panic", name, resp)
		}
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

	if resp := r.Evaluate(t.Context(), "test"); resp.Status != Pass || len(resp.Checks) != 0 || runs.Load() != 0 {
		t.Errorf("Evaluate(exclude test) = %+v after %d runs, want an empty pass", resp, runs.Load())
	}
}
