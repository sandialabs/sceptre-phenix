package cache

import (
	"fmt"
	"time"
)

const lockTimeout = 5 * time.Minute

// LockedError is returned when locking an experiment or VM another action
// holds. It is temporary: the action ends and frees the lock.
type LockedError struct {
	// "experiment" or "vm"
	What   string
	Name   string
	Status Status
}

// Error names what is locked and by which action.
func (e *LockedError) Error() string {
	return fmt.Sprintf("%s %s is locked with status %s", e.What, e.Name, e.Status)
}

// Temporary reports that trying again later may succeed.
func (e *LockedError) Temporary() bool {
	return true
}

func IsExperimentLocked(name string) Status {
	key := "experiment|" + name

	return Locked(key)
}

// ExperimentStatus is the experiment's status as phenix reports it: what a
// handler is doing to it, or else started or stopped.
func ExperimentStatus(name string, running bool) Status {
	if status := IsExperimentLocked(name); status != "" {
		return status
	}

	if running {
		return StatusStarted
	}

	return StatusStopped
}

func UnlockExperiment(name string) {
	key := "experiment|" + name

	Unlock(key)
}

func UnlockVM(exp, name string) {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	Unlock(key)
}

func LockExperimentForCreation(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusCreating, lockTimeout); status != "" {
		return &LockedError{What: "experiment", Name: name, Status: status}
	}

	return nil
}

func LockExperimentForUpdate(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusUpdating, lockTimeout); status != "" {
		return &LockedError{What: "experiment", Name: name, Status: status}
	}

	return nil
}

func LockExperimentForDeletion(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusDeleting, 1*time.Minute); status != "" {
		return &LockedError{What: "experiment", Name: name, Status: status}
	}

	return nil
}

func LockExperimentForStarting(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusStarting, lockTimeout); status != "" {
		return &LockedError{What: "experiment", Name: name, Status: status}
	}

	return nil
}

func LockExperimentForStopping(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusStopping, 1*time.Minute); status != "" {
		return &LockedError{What: "experiment", Name: name, Status: status}
	}

	return nil
}

func LockVMForStarting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusStarting, 1*time.Minute); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForStopping(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusStopping, 1*time.Minute); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForRedeploying(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRedeploying, lockTimeout); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForSnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, lockTimeout); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForRestoring(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRestoring, lockTimeout); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForCommitting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusCommitting, lockTimeout); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}

func LockVMForMemorySnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, lockTimeout); status != "" {
		return &LockedError{What: "vm", Name: name, Status: status}
	}

	return nil
}
