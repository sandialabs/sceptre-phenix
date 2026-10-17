package freshness_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/freshness"
)

// Seq never repeats or goes back, even across many goroutines, never trails
// the clock, and fits a JavaScript number.
func TestSeqIsStrictlyIncreasing(t *testing.T) {
	const (
		goroutines = 64
		calls      = 1000
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
					t.Errorf("Seq() = %d after %d, want more", s, last)

					return
				}

				if _, dup := seen.LoadOrStore(s, true); dup {
					t.Errorf("Seq() = %d, which another call returned", s)

					return
				}

				last = s
			}
		}()
	}

	wg.Wait()

	if s := freshness.Seq(); s < start || s >= 1<<53 {
		t.Errorf("Seq() = %d, want at least the clock's %d and below 2^53", s, start)
	}
}

func TestObservationOlder(t *testing.T) {
	now := time.Now()
	failed := errors.New("busy")

	a := freshness.Observed(now)
	b := freshness.Observation{At: now.Add(-time.Minute), Stale: true, Known: false, Err: failed}

	if diff := cmp.Diff(b, a.Older(b), cmpopts.EquateErrors()); diff != "" {
		t.Errorf("a.Older(b) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(a, freshness.Unobserved().Older(a), cmpopts.EquateErrors()); diff != "" {
		t.Errorf("Unobserved().Older(a) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(a, a.Older(freshness.Unobserved()), cmpopts.EquateErrors()); diff != "" {
		t.Errorf("a.Older(Unobserved()) mismatch (-want +got):\n%s", diff)
	}

	// a part served stale with no time of its own still makes the whole stale
	busy := freshness.Observation{Stale: true, Known: false}
	want := freshness.Observation{At: now, Stale: true, Known: false, Err: nil}
	if diff := cmp.Diff(want, a.Older(busy), cmpopts.EquateErrors()); diff != "" {
		t.Errorf("a.Older(busy) mismatch (-want +got):\n%s", diff)
	}
}
