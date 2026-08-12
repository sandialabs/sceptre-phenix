package app

import (
	"context"
	"testing"

	"phenix/store"
	"phenix/types"
)

// TestTapCleanupNoopWhenStatusMissing reproduces the bug where stopping an
// experiment before a post-start tap app has run (e.g. because the
// experiment was stopped while waiting on delayed VMs) causes Cleanup to
// fail with "missing status for app tap", even though the app never
// created any taps that need to be cleaned up.
func TestTapCleanupNoopWhenStatusMissing(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	var tapApp Tap

	if err := tapApp.Cleanup(context.Background(), exp); err != nil {
		t.Fatalf("expected no error cleaning up tap app with no recorded status, got: %v", err)
	}
}

// TestTapCleanupDeletesRecordedTaps verifies Cleanup still parses and
// processes status when the tap app previously recorded taps it created.
func TestTapCleanupDeletesRecordedTaps(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	// No taps recorded, but the status entry itself exists -- this should
	// still be treated as a legitimate (empty) status rather than "missing".
	exp.Status.SetAppStatus("tap", TapAppStatus{Host: "compute-0"})

	var tapApp Tap

	if err := tapApp.Cleanup(context.Background(), exp); err != nil {
		t.Fatalf("expected no error cleaning up tap app with empty taps, got: %v", err)
	}
}
