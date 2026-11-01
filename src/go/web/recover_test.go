package web

import (
	"strings"
	"testing"

	"phenix/api/experiment"
	"phenix/util/mm"
)

// A start a restarted phenix ui cannot resume is recorded as interrupted in
// the experiment's status, and leaves nothing to cancel.
func TestRecoveredStartThatCannotResumeIsInterrupted(t *testing.T) {
	exp := namedExperiment(t, "waiting", "2024-01-01T00:00:00Z")
	exp.Status["delayedTimers"] = map[string]any{"gone": "2024-01-01T00:05:00Z"}
	exp.Status["postStartPending"] = true

	useTestStore(t, exp)
	useTestMM(t, &testMM{vms: mm.VMs{}, states: map[string]string{}})

	// recoverExperiments runs this in the background for each experiment with a pending start
	resumeStart("waiting")

	commonMu.Lock()
	_, cancelable := cancelers["waiting"]
	commonMu.Unlock()

	if cancelable {
		t.Error("a start that could not resume left a canceler, want none")
	}

	got, err := experiment.Get("waiting")
	if err != nil {
		t.Fatalf("experiment.Get(waiting) error = %v", err)
	}

	if !strings.Contains(got.Status.Interrupted(), "no longer in minimega: gone") {
		t.Errorf("experiment.Get(waiting) interrupted = %q, want it to name gone", got.Status.Interrupted())
	}
}
