package sigterm

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// A SIGINT to the process cancels the context of CancelContext.
func TestCancelContextSIGINT(t *testing.T) { //nolint:paralleltest // signals the test
	ctx := CancelContext(t.Context())

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("sending SIGINT to the test: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("CancelContext() context not canceled 5s after SIGINT, want canceled")
	}
}

// The first signal cancels the context, stops the trap once, and writes one
// notice. A done parent stops the trap and writes no notice.
func TestCancelOnSignal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		signal     bool
		wantNotice string
	}{
		{name: "signal", signal: true, wantNotice: cancelNotice + "\n"},
		{name: "parent done"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()

			var (
				sigs   = make(chan os.Signal, 2)
				stops  = make(chan struct{}, 2)
				notice bytes.Buffer
			)

			ctx := cancelOnSignal(parent, sigs, func() { stops <- struct{}{} }, &notice)

			if tc.signal {
				sigs <- syscall.SIGINT
				sigs <- syscall.SIGINT
			} else {
				cancelParent()
			}

			select {
			case <-stops:
			case <-time.After(5 * time.Second):
				t.Fatal("trap not stopped after 5s, want stopped")
			}

			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("context not canceled after 5s, want canceled")
			}

			if got := len(stops); got != 0 {
				t.Errorf("stop calls = %d, want 1", got+1)
			}

			if got := notice.String(); got != tc.wantNotice {
				t.Errorf("notice = %q, want %q", got, tc.wantNotice)
			}
		})
	}
}
