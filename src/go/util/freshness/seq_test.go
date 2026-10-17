package freshness_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

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

// genSeqObservation draws observations with few distinct times and errors, so ties,
// zero times and repeated errors are common.
func genSeqObservation(base time.Time, errs []error) *rapid.Generator[freshness.Observation] {
	return rapid.Custom(func(t *rapid.T) freshness.Observation {
		at := time.Time{}
		if offset := rapid.IntRange(-1, 3).Draw(t, "offset"); offset >= 0 {
			at = base.Add(time.Duration(offset) * time.Second)
		}

		return freshness.Observation{
			At:    at,
			Stale: rapid.Bool().Draw(t, "stale"),
			Known: rapid.Bool().Draw(t, "known"),
			Err:   rapid.SampledFrom(errs).Draw(t, "err"),
		}
	})
}

// Combining observations in any grouping gives the oldest non-zero time, stale if any part
// is, known only if every part is, and the first error; Unobserved changes nothing.
func TestObservationOlderProperties(t *testing.T) {
	t.Parallel()

	var (
		base = time.Now()
		errs = []error{nil, errors.New("busy"), errors.New("down")}
	)

	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(genSeqObservation(base, errs), 1, 6).Draw(t, "parts")

		got := freshness.Unobserved()
		want := freshness.Observation{Known: true}

		for _, part := range parts {
			got = got.Older(part)

			if !part.At.IsZero() && (want.At.IsZero() || part.At.Before(want.At)) {
				want.At = part.At
			}

			want.Stale = want.Stale || part.Stale
			want.Known = want.Known && part.Known

			if want.Err == nil {
				want.Err = part.Err
			}
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("Older over %+v mismatch (-want +got):\n%s", parts, diff)
		}

		a, b, c := parts[0], parts[len(parts)/2], parts[len(parts)-1]
		if diff := cmp.Diff(a.Older(b).Older(c), a.Older(b.Older(c)), cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("(a.Older(b)).Older(c) != a.Older(b.Older(c)) for %+v, %+v, %+v (-left +right):\n%s", a, b, c, diff)
		}

		if diff := cmp.Diff(a, a.Older(freshness.Unobserved()), cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("a.Older(Unobserved()) mismatch (-want +got):\n%s", diff)
		}
	})
}
