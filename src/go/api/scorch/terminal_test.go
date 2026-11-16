package scorch

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"golang.org/x/term"

	"phenix/util/testenv"
)

// terminal fails when its command does not start, and when STDIN is not a
// terminal, as in a test.
func TestTerminalErrors(t *testing.T) {
	t.Parallel()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		testenv.Skipf(t, "STDIN is a terminal, which the test would put into raw mode")
	}

	t.Run("no command", func(t *testing.T) {
		t.Parallel()

		err := terminal(t.Context(), t.TempDir(), "/nonexistent/sh", nil)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("terminal(/nonexistent/sh) error = %v, want one that wraps %v", err, os.ErrNotExist)
		}
	})

	t.Run("STDIN not a terminal", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var errno syscall.Errno

		err := terminal(ctx, t.TempDir(), "/bin/sh", []string{"-c", "exit 0"})
		if !errors.As(err, &errno) {
			t.Errorf("terminal(/bin/sh) error = %v, want a system error", err)
		}
	})
}
