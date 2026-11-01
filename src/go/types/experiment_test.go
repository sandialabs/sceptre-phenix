package types

import (
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
)

func TestExperimentRunningAndDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		startTime   string
		wantRunning bool
		wantDryRun  bool
	}{
		{name: "stopped", startTime: ""},
		{name: "started", startTime: "2024-01-01T00:00:00Z", wantRunning: true},
		{name: "started as a dry run", startTime: "2024-01-01T00:00:00Z-DRYRUN", wantRunning: true, wantDryRun: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Status.SetStartTime(test.startTime)

			if got := exp.Running(); got != test.wantRunning {
				t.Errorf("Running() with start time %q = %t, want %t", test.startTime, got, test.wantRunning)
			}

			if got := exp.DryRun(); got != test.wantDryRun {
				t.Errorf("DryRun() with start time %q = %t, want %t", test.startTime, got, test.wantDryRun)
			}
		})
	}
}

// Start records an RFC 3339 time, with -DRYRUN appended for a dry run.
func TestExperimentDryRunMatchesTheStartTimeSuffix(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			at     = time.Unix(rapid.Int64Range(0, 1<<33).Draw(t, "unix"), 0).UTC()
			dryrun = rapid.Bool().Draw(t, "dryrun")
			start  = at.Format(time.RFC3339)
		)

		if dryrun {
			start += "-DRYRUN"
		}

		exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
		exp.Status.SetStartTime(start)

		if got := exp.DryRun(); got != dryrun {
			t.Fatalf("DryRun() with start time %q = %t, want %t", start, got, dryrun)
		}
	})
}

// The pending starts survive the trip through a stored config, VM names as given.
func TestExperimentPendingStartsRoundTrip(t *testing.T) {
	t.Parallel()

	vmName := rapid.StringMatching(`[A-Za-z][A-Za-z0-9_.-]{0,15}`)

	rapid.Check(t, func(t *rapid.T) {
		var (
			timers = rapid.MapOf(vmName, rapid.StringMatching(`20[0-9]{2}-0[1-9]-1[0-9]T0[0-9]:00:00Z`)).Draw(t, "timers")
			c2     = rapid.MapOf(vmName, rapid.MapOfN(vmName, rapid.Bool(), 1, 3)).Draw(t, "c2")
			post   = rapid.Bool().Draw(t, "postStart")
			reason = rapid.String().Draw(t, "interrupted")
		)

		exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
		exp.Status.SetStartTime("2024-01-01T00:00:00Z")
		exp.Status.SetDelayedStarts(timers, c2, post)
		exp.Status.SetInterrupted(reason)

		c := store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: exp.Metadata,
			Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
			Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
		}

		got, err := DecodeExperimentFromConfig(c)
		if err != nil {
			t.Fatalf("DecodeExperimentFromConfig() error = %v", err)
		}

		if diff := cmp.Diff(timers, got.Status.DelayedTimers(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("DelayedTimers() after a round trip mismatch (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff(c2, got.Status.DelayedC2(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("DelayedC2() after a round trip mismatch (-want +got):\n%s", diff)
		}

		if got.Status.PostStartPending() != post || got.Status.Interrupted() != reason {
			t.Errorf("PostStartPending(), Interrupted() after a round trip = %t, %q; want %t, %q",
				got.Status.PostStartPending(), got.Status.Interrupted(), post, reason)
		}
	})
}
