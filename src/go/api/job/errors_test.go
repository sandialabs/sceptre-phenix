package job

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

// temporaryError says whether it is temporary, as some library errors do.
type temporaryError struct{ temporary bool }

func (e temporaryError) Error() string   { return "busy" }
func (e temporaryError) Temporary() bool { return e.temporary }

func TestRetryable(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":                     {nil, false},
		"plain":                   {errors.New("no such VM"), false},
		"marked":                  {fmt.Errorf("vm is locked: %w", ErrRetryable), true},
		"deadline":                {fmt.Errorf("waiting: %w", context.DeadlineExceeded), true},
		"canceled":                {context.Canceled, false},
		"network timeout":         {&net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}}, true},
		"temporary":               {fmt.Errorf("wrapped: %w", temporaryError{temporary: true}), true},
		"says it is not":          {temporaryError{temporary: false}, false},
		"forbidden":               {ErrForbidden, false},
		"forbidden and temporary": {errors.Join(ErrForbidden, ErrRetryable), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := Retryable(tc.err); got != tc.want {
				t.Errorf("Retryable(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
