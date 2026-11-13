package cache

import (
	"errors"
	"testing"
	"time"

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

// A LockedError names what is locked, its name and the status it is locked with, and is always temporary.
func TestLockedError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *LockedError
		want string
	}{
		"an experiment": {
			err:  &LockedError{What: "experiment", Name: "exp1", Status: StatusDeleting},
			want: "experiment exp1 is locked with status deleting",
		},
		"a VM": {
			err:  &LockedError{What: "vm", Name: "host", Status: StatusCommitting},
			want: "vm host is locked with status committing",
		},
		"no name": {err: &LockedError{What: "vm"}, want: "vm  is locked with status "},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}

			if !tt.err.Temporary() {
				t.Errorf("Temporary() = false, want true")
			}
		})
	}
}

// IsExperimentLocked gives the status an experiment is locked with, or "" once it is unlocked, and no VM lock of the
// same name counts.
//
//nolint:paralleltest // replaces the default cache
func TestIsExperimentLocked(t *testing.T) {
	original := DefaultWebCache
	DefaultWebCache = NewGoWebCache()

	t.Cleanup(func() { DefaultWebCache = original })

	if err := LockVMForStarting("exp1", "exp1"); err != nil {
		t.Fatalf("LockVMForStarting(exp1, exp1) error = %v", err)
	}

	if got := IsExperimentLocked("exp1"); got != "" {
		t.Errorf("IsExperimentLocked(exp1) with only a VM locked = %q, want none", got)
	}

	if err := LockExperimentForUpdate("exp1"); err != nil {
		t.Fatalf("LockExperimentForUpdate(exp1) error = %v", err)
	}

	if got := IsExperimentLocked("exp1"); got != StatusUpdating {
		t.Errorf("IsExperimentLocked(exp1) = %q, want %q", got, StatusUpdating)
	}

	UnlockExperiment("exp1")

	if got := IsExperimentLocked("exp1"); got != "" {
		t.Errorf("IsExperimentLocked(exp1) after UnlockExperiment = %q, want none", got)
	}

	if got := DefaultWebCache.Locked("vm|exp1/exp1"); got != StatusStarting {
		t.Errorf("VM lock after UnlockExperiment(exp1) = %q, want %q kept", got, StatusStarting)
	}
}

// A kept lock outlives its timeout while it is kept, and expires one timeout after the keeping stops. Renew changes
// only a lock that holds the status it is given.
//
//nolint:paralleltest // replaces the default cache
func TestKeepLocked(t *testing.T) {
	original := DefaultWebCache
	DefaultWebCache = NewGoWebCache()

	t.Cleanup(func() { DefaultWebCache = original })

	const timeout = 150 * time.Millisecond

	if status := Lock("experiment|kept", StatusStopping, timeout); status != "" {
		t.Fatalf("Lock(kept) = %q, want it locked", status)
	}

	stop := keepLocked("experiment|kept", StatusStopping, timeout)

	time.Sleep(4 * timeout)

	if got := IsExperimentLocked("kept"); got != StatusStopping {
		t.Errorf("IsExperimentLocked(kept) after 4 timeouts kept = %q, want %q", got, StatusStopping)
	}

	stop()
	stop()

	time.Sleep(2 * timeout)

	if got := IsExperimentLocked("kept"); got != "" {
		t.Errorf("IsExperimentLocked(kept) 2 timeouts after the keeping stopped = %q, want none", got)
	}

	if status := Lock("experiment|other", StatusStarting, time.Hour); status != "" {
		t.Fatalf("Lock(other) = %q, want it locked", status)
	}

	for key, status := range map[string]Status{"experiment|other": StatusStopping, "experiment|none": StatusStopping} {
		if Renew(key, status, time.Hour) {
			t.Errorf("Renew(%s, %s) = true, want false", key, status)
		}
	}

	if !Renew("experiment|other", StatusStarting, time.Hour) {
		t.Error("Renew(other, starting) = false, want true")
	}

	KeepExperimentStarting("other")()
	KeepExperimentStopping("other")()
}
