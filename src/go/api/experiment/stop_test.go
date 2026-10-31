package experiment

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store/storetest"
)

const stopTestStart = "2024-01-01T00:00:00Z"

// storeRunningExperiment stores experiment exp, started at stopTestStart, with no nodes or apps of its own.
func storeRunningExperiment(t *testing.T) {
	t.Helper()

	storetest.Use(t)
	storeExperiment(t, "exp", map[string]any{"startTime": stopTestStart})
}

// countStopHooks counts the stop hooks run while the test runs.
func countStopHooks(t *testing.T) *int {
	t.Helper()

	original := hooks["stop"]
	t.Cleanup(func() { hooks["stop"] = original })

	count := new(int)
	hooks["stop"] = []Hook{func(string, string) { *count++ }}

	return count
}

func TestStopKeepsTheExperimentRunningWhenItsVMsSurvive(t *testing.T) { //nolint:paralleltest // replaces package globals
	unreachable := errors.New("minimega unreachable")

	tests := []struct {
		name          string
		clearErr      error
		force         bool
		wantErr       []string // empty for none
		wantTeardown  bool     // whether the error wraps ErrTeardownFailed
		wantStartTime string
		wantHooks     int
	}{
		{
			name:      "stops an experiment whose VMs were killed",
			wantHooks: 1,
		},
		{
			name:          "keeps an experiment whose VMs could not be killed running",
			clearErr:      unreachable,
			wantErr:       []string{"killing experiment VMs: minimega unreachable"},
			wantTeardown:  true,
			wantStartTime: stopTestStart,
		},
		{
			name:      "marks the experiment stopped anyway when forced",
			clearErr:  unreachable,
			force:     true,
			wantHooks: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storeRunningExperiment(t)
			fake := useStartMM(t, &startMM{clearErr: test.clearErr})
			hooksRun := countStopHooks(t)

			err := Stop("exp", StopWithKeepInjectionSnapshots(true), StopWithForce(test.force))

			var gotErr []string
			if err != nil {
				gotErr = errorTexts(err)
			}

			if diff := cmp.Diff(test.wantErr, gotErr, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Stop(force %t) errors mismatch (-want +got):\n%s", test.force, diff)
			}

			if got := errors.Is(err, ErrTeardownFailed); got != test.wantTeardown {
				t.Errorf("errors.Is(Stop(force %t), ErrTeardownFailed) = %t, want %t", test.force, got, test.wantTeardown)
			}

			if diff := cmp.Diff([]string{"exp"}, fake.cleared); diff != "" {
				t.Errorf("Stop(force %t) cleared namespaces mismatch (-want +got):\n%s", test.force, diff)
			}

			if got := experimentNamed(t, "exp").Status.StartTime(); got != test.wantStartTime {
				t.Errorf("stored start time after Stop(force %t) = %q, want %q", test.force, got, test.wantStartTime)
			}

			if *hooksRun != test.wantHooks {
				t.Errorf("Stop(force %t) ran %d stop hooks, want %d", test.force, *hooksRun, test.wantHooks)
			}
		})
	}
}

func TestStopCanBeRetriedAfterTheVMsSurvived(t *testing.T) { //nolint:paralleltest // replaces package globals
	storeRunningExperiment(t)
	fake := useStartMM(t, &startMM{clearErr: errors.New("minimega unreachable")})

	if err := Stop("exp", StopWithKeepInjectionSnapshots(true)); !errors.Is(err, ErrTeardownFailed) {
		t.Fatalf("first Stop() error = %v, want it to wrap ErrTeardownFailed", err)
	}

	fake.clearErr = nil

	if err := Stop("exp", StopWithKeepInjectionSnapshots(true)); err != nil {
		t.Fatalf("second Stop() once minimega answers error = %v, want nil", err)
	}

	if diff := cmp.Diff([]string{"exp", "exp"}, fake.cleared); diff != "" {
		t.Errorf("cleared namespaces mismatch (-want +got):\n%s", diff)
	}

	if experimentNamed(t, "exp").Running() {
		t.Error("experiment still running after the second Stop(), want it stopped")
	}
}
