package sigterm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// cancelNotice is the line that CancelContext writes to stderr when a signal
// cancels the context.
const cancelNotice = "canceling; press Ctrl+C again to stop at once"

// CancelContext returns a context that wraps the given context with a cancel
// function that's called when a SIGTERM or SIGINT is trapped. It traps the
// signals before it returns. The first signal cancels the context, writes one
// line to stderr, and stops the trap, so a second signal ends the process.
func CancelContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)

	return cancelOnSignal(ctx, term, func() { signal.Stop(term) }, os.Stderr)
}

// cancelOnSignal returns a copy of ctx that the first value from sigs cancels.
// It calls stop when a signal arrives or ctx is done, and writes cancelNotice
// to notice only for a signal.
func cancelOnSignal(ctx context.Context, sigs <-chan os.Signal, stop func(), notice io.Writer) context.Context {
	ctxWithCancel, cancel := context.WithCancel(ctx)

	go func() {
		defer cancel()

		select {
		case <-sigs:
			stop()
			fmt.Fprintln(notice, cancelNotice)
		case <-ctx.Done():
			stop()
		}
	}()

	return ctxWithCancel
}
