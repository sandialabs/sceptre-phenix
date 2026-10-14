package cache

import (
	"errors"
	"testing"

	"phenix/api/job"
)

func TestExperimentStatus(t *testing.T) {
	if got := ExperimentStatus("status-test", false); got != StatusStopped {
		t.Errorf("stopped experiment: status = %q, want %q", got, StatusStopped)
	}

	if got := ExperimentStatus("status-test", true); got != StatusStarted {
		t.Errorf("running experiment: status = %q, want %q", got, StatusStarted)
	}

	if err := LockExperimentForStarting("status-test"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { UnlockExperiment("status-test") })

	// what a handler is doing to the experiment wins
	if got := ExperimentStatus("status-test", false); got != StatusStarting {
		t.Errorf("locked experiment: status = %q, want %q", got, StatusStarting)
	}
}

// A lock held by another action is a temporary failure, with the message it
// always had.
func TestLockedErrorIsRetryable(t *testing.T) {
	if err := LockVMForStarting("locked-test", "vm1"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { UnlockVM("locked-test", "vm1") })

	err := LockVMForStopping("locked-test", "vm1")

	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("LockVMForStopping = %v, want a *LockedError", err)
	}

	if want := "vm vm1 is locked with status starting"; err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}

	if !job.Retryable(err) {
		t.Error("a held lock is not retryable")
	}
}
