package cache

import "testing"

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
