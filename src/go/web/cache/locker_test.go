package cache

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

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

// A lock held by another action is a temporary failure whose message names
// the locked VM and its status.
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

// lockerLock is a lock function of an experiment or VM, and the status it locks with.
type lockerLock struct {
	name   string
	lock   func(exp, vm string) error
	status Status
}

func lockerExperimentLocks() []lockerLock {
	exp := func(name string, lock func(string) error, status Status) lockerLock {
		return lockerLock{name: name, lock: func(exp, _ string) error { return lock(exp) }, status: status}
	}

	return []lockerLock{
		exp("creation", LockExperimentForCreation, StatusCreating),
		exp("update", LockExperimentForUpdate, StatusUpdating),
		exp("deletion", LockExperimentForDeletion, StatusDeleting),
		exp("starting", LockExperimentForStarting, StatusStarting),
		exp("stopping", LockExperimentForStopping, StatusStopping),
	}
}

func lockerVMLocks() []lockerLock {
	return []lockerLock{
		{name: "starting", lock: LockVMForStarting, status: StatusStarting},
		{name: "stopping", lock: LockVMForStopping, status: StatusStopping},
		{name: "redeploying", lock: LockVMForRedeploying, status: StatusRedeploying},
		{name: "snapshotting", lock: LockVMForSnapshotting, status: StatusSnapshotting},
		{name: "restoring", lock: LockVMForRestoring, status: StatusRestoring},
		{name: "committing", lock: LockVMForCommitting, status: StatusCommitting},
		{name: "memory snapshotting", lock: LockVMForMemorySnapshotting, status: StatusSnapshotting},
	}
}

// An experiment or VM is locked by one action until unlocked; a second lock fails
// naming its status, and experiment and VM locks never block one another.
//
//nolint:paralleltest // replaces the default cache
func TestLocksAreExclusive(t *testing.T) {
	original := DefaultWebCache
	t.Cleanup(func() { DefaultWebCache = original })

	names := rapid.SampledFrom([]string{"a", "b"})

	rapid.Check(t, func(t *rapid.T) {
		DefaultWebCache = NewGoWebCache()

		// the status each experiment ("exp a") and VM ("vm a/b") is locked with
		held := make(map[string]Status)

		lock := func(t *rapid.T, l lockerLock, key, what, exp, vm, name string) {
			err := l.lock(exp, vm)

			if status, ok := held[key]; ok {
				want := &LockedError{What: what, Name: name, Status: status}
				if diff := cmp.Diff(want, err); diff != "" {
					t.Fatalf("locking %s for %s while locked mismatch (-want +got):\n%s", key, l.name, diff)
				}

				return
			}

			if err != nil {
				t.Fatalf("locking unlocked %s for %s error = %v", key, l.name, err)
			}

			held[key] = l.status
		}

		t.Repeat(map[string]func(*rapid.T){
			"lock experiment": func(t *rapid.T) {
				l := rapid.SampledFrom(lockerExperimentLocks()).Draw(t, "lock")
				exp := names.Draw(t, "exp")

				lock(t, l, "exp "+exp, "experiment", exp, "", exp)
			},
			"lock VM": func(t *rapid.T) {
				l := rapid.SampledFrom(lockerVMLocks()).Draw(t, "lock")
				exp, vm := names.Draw(t, "exp"), names.Draw(t, "vm")

				lock(t, l, "vm "+exp+"/"+vm, "vm", exp, vm, vm)
			},
			"unlock experiment": func(t *rapid.T) {
				exp := names.Draw(t, "exp")

				UnlockExperiment(exp)

				delete(held, "exp "+exp)
			},
			"unlock VM": func(t *rapid.T) {
				exp, vm := names.Draw(t, "exp"), names.Draw(t, "vm")

				UnlockVM(exp, vm)
				delete(held, "vm "+exp+"/"+vm)
			},
			"": func(t *rapid.T) {
				for _, exp := range []string{"a", "b"} {
					want := held["exp "+exp]
					if want == "" {
						want = StatusStopped
					}

					if got := ExperimentStatus(exp, false); got != want {
						t.Fatalf("ExperimentStatus(%s, false) = %q, want %q", exp, got, want)
					}
				}
			},
		})
	})
}
