package experiment

import "fmt"

type DelayedVMError struct {
	VM  string
	src error
	msg string
}

func NewDelayedVMError(vm string, err error, format string, a ...interface{}) DelayedVMError {
	return DelayedVMError{VM: vm, src: err, msg: fmt.Sprintf(format, a...)}
}

func (this DelayedVMError) Error() string {
	return fmt.Sprintf("%s: %v", this.msg, this.src)
}

func (this DelayedVMError) Unwrap() error {
	return this.src
}
