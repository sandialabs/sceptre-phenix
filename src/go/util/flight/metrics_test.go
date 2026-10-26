package flight

import (
	"context"
	"sync"
	"testing"

	"phenix/util/metrics/metricstest"
	"phenix/util/polltest"
)

// A call that runs the group's function and one that joins it count apart,
// by the group's name.
func TestGroupRecordsCallMetrics(t *testing.T) {
	t.Parallel()

	var (
		group   = Group[int]{Name: "test-metrics"}
		release = make(chan struct{})
		wg      sync.WaitGroup
		ran     = map[string]string{"group": "test-metrics", "outcome": "ran"}
		joined  = map[string]string{"group": "test-metrics", "outcome": "joined"}
	)

	fn := func(ctx context.Context, _ func()) (int, error) {
		<-release

		return 1, ctx.Err()
	}

	for range 2 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = group.Do(context.Background(), "k", fn)
		}()
	}

	polltest.Until(t, "both callers wait for the run", func() bool { return group.Waiting("k") == 2 })
	close(release)
	wg.Wait()

	if got := metricstest.Value(t, "phenix_flight_calls_total", ran); got != 1 {
		t.Errorf("phenix_flight_calls_total %v = %v, want 1", ran, got)
	}

	if got := metricstest.Value(t, "phenix_flight_calls_total", joined); got != 1 {
		t.Errorf("phenix_flight_calls_total %v = %v, want 1", joined, got)
	}
}
