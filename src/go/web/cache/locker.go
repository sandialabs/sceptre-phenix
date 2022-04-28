package cache

import (
	"fmt"
	"time"
)

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

	if status := Lock(key, StatusCreating, 5*time.Minute); status != "" {
		return fmt.Errorf("experiment %s is locked with status %s", name, status)
	}

	return nil
}

func LockExperimentForUpdate(name string) error {
	key := "experiment|" + name

	if status := Lock(key, StatusUpdating, 5*time.Minute); status != "" {
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

	if status := Lock(key, StatusStarting, 5*time.Minute); status != "" {
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
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForStopping(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusStopping, 1*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForRedeploying(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRedeploying, 5*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForSnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, 5*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForRestoring(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusRestoring, 5*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForCommitting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusCommitting, 5*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}

func LockVMForMemorySnapshotting(exp, name string) error {
	key := fmt.Sprintf("vm|%s/%s", exp, name)

	if status := Lock(key, StatusSnapshotting, 5*time.Minute); status != "" {
		return fmt.Errorf("VM %s is locked with status %s", name, status)
	}

	return nil
}
