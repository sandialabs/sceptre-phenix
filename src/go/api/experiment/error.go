package experiment

import (
	"errors"
	"fmt"

	"phenix/store"
)

// ErrTeardownFailed is wrapped by the error Stop returns when minimega could
// not kill the experiment's VMs, leaving the experiment running.
var ErrTeardownFailed = errors.New("killing experiment VMs")

// ErrStopNotRunning is the error Stop returns for an experiment that does not run.
var ErrStopNotRunning = errors.New("experiment isn't running")

// ErrDeleteRunning is the error Delete returns for an experiment that runs.
var ErrDeleteRunning = errors.New("cannot delete a running experiment")

// ErrDeleteCleanup is wrapped by the error Delete returns when it deleted the
// experiment's config, but could not delete all of its files.
var ErrDeleteCleanup = errors.New("cleaning up the deleted experiment")

type DelayedVMError struct {
	VM  string
	src error
	msg string
}

func NewDelayedVMError(vm string, err error, format string, a ...any) DelayedVMError {
	return DelayedVMError{VM: vm, src: err, msg: fmt.Sprintf(format, a...)}
}

func (e DelayedVMError) Error() string {
	return fmt.Sprintf("%s: %v", e.msg, e.src)
}

func (e DelayedVMError) Unwrap() error {
	return e.src
}

// notFound wraps a store error in ErrExperimentNotFound when the experiment's
// config does not exist.
func notFound(err error) error {
	if errors.Is(err, store.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrExperimentNotFound, err)
	}

	return err
}
