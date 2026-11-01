package experiment

import (
	"errors"
	"fmt"
)

// ErrTeardownFailed is wrapped by the error Stop returns when minimega could
// not kill the experiment's VMs, leaving the experiment running.
var ErrTeardownFailed = errors.New("killing experiment VMs")

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
