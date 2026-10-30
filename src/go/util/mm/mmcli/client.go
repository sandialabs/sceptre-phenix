// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/hashicorp/go-multierror"

	"phenix/util/common"
)

var (
	// ErrTimeout is reported when a command does not return within the requested
	// time period.
	ErrTimeout = errors.New("timeout running command")

	// ErrNoResponse is reported when minimega closes a command's response stream
	// without having sent anything. That means the connection was lost, not that
	// the command legitimately produced no output.
	ErrNoResponse = errors.New("no response from minimega (connection lost)")

	// ErrNotSent is wrapped by the error of a command that gave up waiting for
	// the shared connection, so minimega never received it.
	ErrNotSent = errors.New("waiting for the minimega connection")

	// errDeadline is a wait ended by its context's deadline: it is both
	// ErrTimeout and [context.DeadlineExceeded].
	errDeadline error = deadlineError{}

	// shared is held by the command using the shared connection, so commands
	// take it one at a time, background ones last.
	shared connLock //nolint:gochecknoglobals // global lock

	mu     sync.Mutex       //nolint:gochecknoglobals // guards mm and mmDead
	mm     *miniclient.Conn //nolint:gochecknoglobals // global connection
	mmDead bool             //nolint:gochecknoglobals // flags mm for replacement
)

// Deadlines for commands on the shared connection. Tests shorten them.
//
//nolint:gochecknoglobals // see above
var (
	// DefaultWaitTimeout is how long a command waits behind others for the
	// shared connection before giving up with ErrTimeout, however long its
	// context (see RunContext) would let it wait.
	DefaultWaitTimeout = 10 * time.Minute

	// DefaultCommandTimeout is how long a command run without a context
	// deadline may hold the shared connection. Past it the caller gets
	// ErrTimeout and the connection is abandoned, so the commands behind it
	// can go on; minimega still finishes the command. Long-running commands
	// use RunDedicated instead.
	DefaultCommandTimeout = 30 * time.Minute
)

type deadlineError struct{}

func (deadlineError) Error() string { return ErrTimeout.Error() + " (context deadline exceeded)" }

func (deadlineError) Is(target error) bool {
	return target == ErrTimeout || target == context.DeadlineExceeded
}

// errResponse builds the single response value used to report an error back
// through a response channel.
func errResponse(err error) *miniclient.Response {
	return &miniclient.Response{ //nolint:exhaustruct // partial initialization
		Resp: minicli.Responses{
			&minicli.Response{ //nolint:exhaustruct // partial initialization
				Error: err.Error(),
			},
		},
		More: false,
	}
}

// reconstructErr turns a response's error string back into an error, restoring
// the sentinel identity where the string is one of ours.
func reconstructErr(msg string) error {
	if rest, ok := strings.CutPrefix(msg, ErrNotSent.Error()+": "); ok {
		return fmt.Errorf("%w: %w", ErrNotSent, reconstructErr(rest))
	}

	for _, sentinel := range []error{errDeadline, ErrTimeout, ErrNoResponse, context.Canceled} {
		if msg == sentinel.Error() {
			return sentinel
		}

		// streamEnded wraps its sentinel with context before it is flattened to a string
		if suffix := ": " + sentinel.Error(); strings.HasSuffix(msg, suffix) {
			return fmt.Errorf("%s: %w", strings.TrimSuffix(msg, suffix), sentinel)
		}
	}

	return errors.New(msg)
}

func wrapErr(err error) chan *miniclient.Response {
	out := make(chan *miniclient.Response, 1)

	out <- errResponse(err)

	close(out)

	return out
}

// ErrorResponse is used when only concerned with errors returned from a call to
// minimega. A *multierror.Error will be returned containing a full list of all
// the errors encountered.
func ErrorResponse(responses chan *miniclient.Response) error {
	var errs error

	for response := range responses {
		for _, resp := range response.Resp {
			if resp.Error != "" {
				errs = multierror.Append(errs, reconstructErr(resp.Error))
			}
		}
	}

	return errs
}

// vmNotFoundPrefix starts minimega's error for a VM a node doesn't have.
const vmNotFoundPrefix = "vm not found: "

// VMTargetErrorResponse is ErrorResponse for a command naming one VM that minimega runs on every node in the namespace
// (such as `vm flush <vm>`). As minimega does for its VM-target commands, the other nodes' "vm not found" errors are
// dropped once any node answered without one.
func VMTargetErrorResponse(responses chan *miniclient.Response) error {
	var (
		errs, notFound error
		ok             bool
	)

	for response := range responses {
		for _, resp := range response.Resp {
			switch {
			case resp.Error == "":
				ok = true
			case strings.HasPrefix(resp.Error, vmNotFoundPrefix):
				notFound = multierror.Append(notFound, reconstructErr(resp.Error))
			default:
				errs = multierror.Append(errs, reconstructErr(resp.Error))
			}
		}
	}

	if errs == nil && !ok {
		return notFound
	}

	return errs
}

// SingleResponse is used when only a single response (or error) is expected to
// be returned from a call to minimega. It returns the first non-error response
// and the last error encountered (if no non-error responses were encountered).
func SingleResponse(responses chan *miniclient.Response) (string, error) {
	var (
		resp *string
		err  error
	)

	for response := range responses {
		// If we've encountered a non-error response (even if it's empty), then
		// continue on to drain the responses channel.
		if resp != nil {
			continue
		}

		for _, r := range response.Resp {
			if r.Error != "" {
				err = reconstructErr(r.Error)

				continue
			}

			resp = &r.Response

			// Clear any error previously encountered and break out of this inner
			// for-loop since we've encountered a non-error response (even if it's
			// empty).
			err = nil

			break
		}
	}

	if resp == nil {
		return "", err
	}

	return *resp, err
}

// SingleDataResponse is used when only a single response (or error) is expected
// to be returned from a call to minimega, and the response just includes user
// data. It returns the first non-error data response and the last error
// encountered (if no non-error responses were encountered).
func SingleDataResponse(responses chan *miniclient.Response) (any, error) {
	var (
		data any
		err  error
	)

	for response := range responses {
		// If we've encountered a non-error response (even if it's empty), then
		// continue on to drain the responses channel.
		if data != nil {
			continue
		}

		for _, r := range response.Resp {
			if r.Error != "" {
				err = reconstructErr(r.Error)

				continue
			}

			data = r.Data

			// Clear any error previously encountered and break out of this inner
			// for-loop since we've encountered a non-error response (even if it's
			// empty).
			err = nil

			break
		}
	}

	return data, err
}

// conn returns a usable connection to minimega, dialing or redialing as needed.
// The caller must hold shared.
func conn() (*miniclient.Conn, error) {
	mu.Lock()
	defer mu.Unlock()

	// miniclient records only the FIRST error it encounters and never clears it,
	// so any non-nil error means this connection is permanently unusable
	if mm != nil && mm.Error() != nil {
		mmDead = true
	}

	if mm == nil || mmDead {
		c, err := miniclient.Dial(common.MinimegaBase)
		if err != nil {
			return nil, fmt.Errorf("unable to dial: %w", err)
		}

		mm, mmDead = c, false
	}

	return mm, nil
}

// markDead flags the given connection for replacement on the next call, but only
// if it is still the current connection. The identity check stops a stale
// timeout from clobbering a connection that a later call already redialed.
func markDead(c *miniclient.Conn) {
	mu.Lock()
	defer mu.Unlock()

	if mm == c {
		mmDead = true
	}
}

// streamEnded reports the error, if any, that should be appended once a response
// stream has closed. miniclient signals a lost connection only through its own
// private error field and closes the response channel having sent nothing.
func streamEnded(c *miniclient.Conn, count int) error {
	err := c.Error()

	if err == nil {
		if count > 0 {
			return nil
		}

		err = ErrNoResponse
	}

	return fmt.Errorf("running minimega command: %w", err)
}

// guard forwards responses from the shared connection, reporting a truncated or empty stream as an error. release is
// called once the terminal error has been read (miniclient closes the channel before releasing its lock), or at once
// when a command outlasts timeout (none if zero or less) and the connection is closed. Callers MUST drain the returned
// channel, or this goroutine and miniclient's reader block for the life of the process.
func guard(
	c *miniclient.Conn,
	in chan *miniclient.Response,
	release func(),
	timeout time.Duration,
) chan *miniclient.Response {
	out := make(chan *miniclient.Response)

	go func() {
		defer close(out)

		var (
			count int
			after <-chan time.Time // nil (never fires) without a timeout
		)

		if timeout > 0 {
			timer := time.NewTimer(timeout)
			defer timer.Stop()

			after = timer.C
		}

		for {
			select {
			case resp, ok := <-in:
				if !ok {
					err := streamEnded(c, count)

					release()

					if err != nil {
						markDead(c)

						out <- errResponse(err)
					}

					return
				}

				count++

				out <- resp
			case <-after:
				markDead(c)

				// unblocks miniclient's reader, then lets it finish
				_ = c.Close()

				go func() {
					for range in {
						_ = struct{}{}
					}
				}()

				release()

				out <- errResponse(ErrTimeout)

				return
			}
		}
	}()

	return out
}

// Run runs c on the shared connection, redialing it if needed; errors come in the response channel,
// which the caller must drain. Past DefaultWaitTimeout or DefaultCommandTimeout it returns
// ErrTimeout. Code serving a web request or a background job uses RunContext instead.
func Run(c *Command) chan *miniclient.Response {
	return runShared(context.Background(), c, nil)
}

// RunContext is Run for a request or job that may end first: once ctx is done, a command still
// waiting for the shared connection gives up unsent (ErrNotSent wrapping ctx's error). A sent
// command is not interrupted; ctx's deadline, if any, bounds it in place of DefaultCommandTimeout.
func RunContext(ctx context.Context, c *Command) chan *miniclient.Response {
	return runShared(ctx, c, nil)
}

// RunStartedContext is RunContext, calling started (if not nil) just before c is sent; started may
// run under the shared connection's lock, so it must be quick and run no minimega commands.
func RunStartedContext(ctx context.Context, c *Command, started func()) chan *miniclient.Response {
	return runShared(ctx, c, started)
}

// runShared runs c on the shared connection once it is free, unless c has a
// timeout of its own, which gives it a private connection. Nothing is sent
// once ctx is done.
func runShared(ctx context.Context, c *Command, started func()) chan *miniclient.Response {
	cmdStr := c.String()

	// checked first, as a free connection is taken even once ctx is done
	if err := ctx.Err(); err != nil {
		return wrapErr(gaveUp(err))
	}

	if c.Timeout > 0 {
		notify(started)

		return runPrivate(cmdStr, c.Timeout)
	}

	wait, cancel := context.WithTimeout(ctx, DefaultWaitTimeout)
	defer cancel()

	if !shared.acquire(wait, c.Background) {
		return wrapErr(gaveUp(ctx.Err()))
	}

	release := shared.release

	notify(started)

	active, err := conn()
	if err != nil {
		release()

		return wrapErr(err)
	}

	timeout := DefaultCommandTimeout

	if deadline, ok := ctx.Deadline(); ok {
		timeout = max(time.Until(deadline), time.Nanosecond)
	}

	// shared is held until guard has read the terminal error, so no other
	// command can reach Conn.err in between
	return guard(active, active.Run(cmdStr), release, timeout)
}

// gaveUp is the error of a command that stopped waiting for the shared
// connection: err, its context's error, or ErrTimeout after DefaultWaitTimeout.
func gaveUp(err error) error {
	switch {
	case err == nil:
		err = ErrTimeout
	case errors.Is(err, context.DeadlineExceeded):
		err = errDeadline
	}

	return fmt.Errorf("%w: %w", ErrNotSent, err)
}

// RunDedicated runs the given command on a connection of its own, with no
// timeout unless c.Timeout is set. It is for long-running commands (such as
// reading an experiment's script or launching its VMs) that would otherwise
// hold the shared connection, and so every other command, for their whole
// duration. Callers MUST drain the returned channel, as with Run.
func RunDedicated(c *Command) chan *miniclient.Response {
	return runPrivate(c.String(), c.Timeout)
}

func notify(started func()) {
	if started != nil {
		started()
	}
}

// runPrivate runs a command on a connection of its own, so that abandoning it
// on timeout cannot disturb commands in flight on the shared connection. A
// timeout of zero or less waits for the command however long it takes.
//
// Callers MUST drain the returned channel, as with guard.
func runPrivate(cmdStr string, timeout time.Duration) chan *miniclient.Response {
	private, err := miniclient.Dial(common.MinimegaBase)
	if err != nil {
		return wrapErr(fmt.Errorf("unable to dial: %w", err))
	}

	out := make(chan *miniclient.Response)

	go func() {
		defer close(out)
		defer func() { _ = private.Close() }()

		var (
			in    = private.Run(cmdStr)
			after <-chan time.Time // nil (never fires) without a timeout
			count int
		)

		if timeout > 0 {
			after = time.After(timeout)
		}

		for {
			select {
			case resp, ok := <-in:
				if !ok {
					if err := streamEnded(private, count); err != nil {
						out <- errResponse(err)
					}

					return
				}

				count++

				out <- resp
			case <-after:
				// Drain in the background so miniclient's reader can exit. It
				// may be parked on its unbuffered send, where closing the
				// connection alone would not release it.
				go func() {
					for range in {
						_ = struct{}{}
					}
				}()

				out <- errResponse(ErrTimeout)

				return
			}
		}
	}()

	return out
}
