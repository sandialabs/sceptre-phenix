package cache

import (
	"errors"
	"testing"

	"phenix/api/job"
)

func TestExperimentStatus(t *testing.T) {
	if got := ExperimentStatus("status-test", false); got != StatusStopped {
		t.Errorf("ExperimentStatus(status-test, false) = %q, want %q", got, StatusStopped)
	}

	if got := ExperimentStatus("status-test", true); got != StatusStarted {
		t.Errorf("ExperimentStatus(status-test, true) = %q, want %q", got, StatusStarted)
	}

	if err := LockExperimentForStarting("status-test"); err != nil {
		t.Fatalf("LockExperimentForStarting(status-test) error = %v", err)
	}

	t.Cleanup(func() { UnlockExperiment("status-test") })

	// what a handler is doing to the experiment wins
	if got := ExperimentStatus("status-test", false); got != StatusStarting {
		t.Errorf("ExperimentStatus(status-test, false) while starting = %q, want %q", got, StatusStarting)
	}
}

// A lock held by another action is a temporary failure, with the message it
// always had.
func TestLockedErrorIsRetryable(t *testing.T) {
	if err := LockVMForStarting("locked-test", "vm1"); err != nil {
		t.Fatalf("LockVMForStarting(locked-test, vm1) error = %v", err)
	}

	t.Cleanup(func() { UnlockVM("locked-test", "vm1") })

	err := LockVMForStopping("locked-test", "vm1")

	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("LockVMForStopping(locked-test, vm1) error = %v, want a *LockedError", err)
	}

	if want := "vm vm1 is locked with status starting"; err.Error() != want {
		t.Errorf("LockVMForStopping(locked-test, vm1) error = %q, want %q", err, want)
	}

	if !job.Retryable(err) {
		t.Errorf("job.Retryable(%v) = false, want true", err)
	}
}
