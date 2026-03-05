package cache

import (
	"fmt"
	"time"
)

const lockTimeout = 5 * time.Minute

func IsExperimentLocked(name string) Status {
	key := "experiment|" + name

	return Locked(key)
}

func UnlockExperiment(name string) {
	key := "experiment|" + name

	Unlock(key)
}

func IsVMLocked(exp, name string) Status {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	return Locked(key)
}

func UnlockVM(exp, name string) {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	Unlock(key)
}

func LockExperimentForCreation(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusCreating, lockTimeout); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockExperimentForUpdate(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusUpdating, lockTimeout); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockExperimentForDeletion(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusDeleting, 1*time.Minute); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockExperimentForStarting(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusStarting, lockTimeout); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockExperimentForStopping(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusStopping, 1*time.Minute); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForStarting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusStarting, 1*time.Minute); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForStopping(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusStopping, 1*time.Minute); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForRedeploying(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRedeploying, lockTimeout); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForSnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, lockTimeout); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForRestoring(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRestoring, lockTimeout); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForCommitting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusCommitting, lockTimeout); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForMemorySnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, lockTimeout); status != "" {
		return fmt.Errorf("vm %s is locked with status %s", name, status)
	}

	return nil
}
