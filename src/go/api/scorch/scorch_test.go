package scorch

import (
	"testing"
)

// Only the stop and cleanup stages end a background component, and only the
// one that the start or configure stage of the same name started.
//
//nolint:paralleltest // replaces the background component state
func TestHandleBackgrounded(t *testing.T) {
	opts := NewOptions(Experiment(*builtinExperiment()), Name("bg"))

	for stage := range backgrounded {
		backgrounded[stage]["exp/"+string(stage)+"/bg"] = func() {}
	}

	t.Cleanup(func() {
		for stage := range backgrounded {
			delete(backgrounded[stage], "exp/"+string(stage)+"/bg")
		}
	})

	for _, tc := range []struct {
		stage Action
		want  bool
	}{
		{ActionConfigure, false}, {ActionStart, false}, {ActionDone, false}, {ActionLoop, false}, {"other", false},
		{ActionStop, true}, {ActionCleanup, true}, {ActionStop, false}, {ActionCleanup, false},
	} {
		if got := handleBackgrounded(tc.stage, opts); got != tc.want {
			t.Errorf("handleBackgrounded(%s) = %t, want %t", tc.stage, got, tc.want)
		}
	}
}
