package experiment

import "fmt"

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
