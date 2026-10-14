package freshness_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"phenix/util/freshness"
)

// Seq never repeats or goes back, even across many goroutines, never trails
// the clock, and fits a JavaScript number.
func TestSeqIsStrictlyIncreasing(t *testing.T) {
	const (
		goroutines = 64
		calls      = 10000
	)

	start := uint64(time.Now().UnixMicro()) //nolint:gosec // after 1970

	var (
		wg   sync.WaitGroup
		seen sync.Map
	)

	for range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			var last uint64

			for range calls {
				s := freshness.Seq()
				if s <= last {
					t.Errorf("seq went from %d to %d", last, s)

					return
				}

				if _, dup := seen.LoadOrStore(s, true); dup {
					t.Errorf("seq %d given twice", s)

					return
				}

				last = s
			}
		}()
	}

	wg.Wait()

	if s := freshness.Seq(); s < start || s >= 1<<53 {
		t.Errorf("seq %d is before the clock (%d) or past 2^53", s, start)
	}
}

func TestObservationOlder(t *testing.T) {
	now := time.Now()
	failed := errors.New("busy")

	a := freshness.Observed(now)
	b := freshness.Observation{At: now.Add(-time.Minute), Stale: true, Known: false, Err: failed}

	got := a.Older(b)
	if !got.At.Equal(b.At) || !got.Stale || got.Known || !errors.Is(got.Err, failed) {
		t.Errorf("a.Older(b) = %+v, want b's time, stale, unknown, with b's error", got)
	}

	if got := freshness.Unobserved().Older(a); got != a {
		t.Errorf("Unobserved().Older(a) = %+v, want a", got)
	}

	if got := a.Older(freshness.Unobserved()); got != a {
		t.Errorf("a.Older(Unobserved()) = %+v, want a", got)
	}

	// a part served stale with no time of its own still makes the whole stale
	busy := freshness.Observation{Stale: true, Known: false}
	if got := a.Older(busy); !got.At.Equal(now) || !got.Stale || got.Known {
		t.Errorf("a.Older(busy) = %+v, want a's time, stale and unknown", got)
	}
}
