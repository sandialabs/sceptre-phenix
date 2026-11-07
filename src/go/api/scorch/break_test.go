package scorch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/term"

	"phenix/app"
	"phenix/util/polltest"
	"phenix/util/testenv"
	"phenix/web/scorch"
)

// breakTerminal returns the web terminal of the break component name in
// experiment exp, once the break starts it.
func breakTerminal(t *testing.T, name string) scorch.WebTerm {
	t.Helper()

	var found scorch.WebTerm

	polltest.Until(t, "the break starts its web terminal", func() bool {
		terms, _ := scorch.GetExperimentTerminals("exp", -1)
		for _, term := range terms {
			if term.Name == name {
				found = term

				return true
			}
		}

		return false
	})

	return found
}

// A break started from the CLI gives control to a terminal, and a break
// started from the UI waits until its web terminal exits or its context ends.
func TestBreakTerminals(t *testing.T) {
	t.Parallel()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		testenv.Skipf(t, "STDIN is a terminal, which the test would put into raw mode")
	}

	start := func(t *testing.T, ctx context.Context, name string) <-chan error {
		t.Helper()

		var b Break
		_ = b.Init(Experiment(*builtinExperiment()), Name(name))

		done := make(chan error, 1)

		go func() { done <- b.Start(ctx) }()

		return done
	}

	t.Run("CLI", func(t *testing.T) {
		t.Parallel()

		var errno syscall.Errno

		// the test's STDIN is not a terminal
		if err := <-start(t, app.SetContextTriggerCLI(t.Context()), "cli"); !errors.As(err, &errno) {
			t.Errorf("Break.Start(from the CLI) error = %v, want a system error", err)
		}
	})

	t.Run("UI terminal does not start", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(app.SetContextTriggerUI(t.Context()))
		cancel()

		if err := <-start(t, ctx, "ui-fail"); !errors.Is(err, context.Canceled) {
			t.Errorf("Break.Start(from the UI, context done) error = %v, want %v", err, context.Canceled)
		}
	})

	t.Run("UI terminal exits", func(t *testing.T) {
		t.Parallel()

		done := start(t, app.SetContextTriggerUI(t.Context()), "ui-exit")

		if err := scorch.KillTerminal(breakTerminal(t, "ui-exit")); err != nil {
			t.Fatalf("KillTerminal() error = %v", err)
		}

		if err := <-done; err != nil {
			t.Errorf("Break.Start(from the UI, terminal exits) error = %v, want nil", err)
		}
	})

	t.Run("UI context ends", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(app.SetContextTriggerUI(t.Context()))
		defer cancel()

		done := start(t, ctx, "ui-cancel")
		term := breakTerminal(t, "ui-cancel")

		cancel()

		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Break.Start(from the UI, context ends) error = %v, want %v", err, context.Canceled)
		}

		polltest.Until(t, "the break's web terminal ends", func() bool {
			_, err := scorch.GetTerminalByPID(term.Pid)

			return errors.Is(err, scorch.ErrTerminalNotFound)
		})
	})
}

// A break fails when it cannot make its temporary directory.
func TestBreakTemporaryDirectoryError(t *testing.T) { //nolint:paralleltest // sets TMPDIR
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	var b Break
	_ = b.Init(Experiment(*builtinExperiment()), Name("brk"))

	var pathErr *os.PathError
	if err := b.Start(context.Background()); !errors.As(err, &pathErr) {
		t.Errorf("Break.Start(no temporary directory) error = %v, want a path error", err)
	}
}
