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

	// shared is held by the command using the shared connection, so commands
	// take it one at a time; a channel, so a waiter can give up.
	shared = make(chan struct{}, 1) //nolint:gochecknoglobals // global lock

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
	for _, sentinel := range []error{ErrTimeout, ErrNoResponse, context.Canceled} {
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

// VMTargetErrorResponse is ErrorResponse for a command naming one VM that
// minimega runs on every node in the namespace (such as `vm flush <vm>` or `cc
// tunnel <vm> ...`). The nodes not running the VM answer "vm not found", so, as
// minimega itself does for its VM-target commands, those errors are dropped
// once any node has answered without an error. They are returned only when no
// node succeeded and nothing else went wrong.
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

// guard forwards responses from the shared connection, reporting a truncated or
// empty stream as an error rather than as a successful empty result.
//
// release is called once the terminal error has been read. It must not be
// called any earlier: miniclient closes the response channel before releasing
// its own connection lock, so another command could otherwise take that lock
// and overwrite Conn.err first.
//
// A command still running after timeout (none if zero or less) is abandoned:
// the connection is marked dead and closed, and release is called at once, so
// the commands waiting behind it can go on over a new connection.
//
// Callers MUST drain the returned channel. Abandoning it blocks this goroutine,
// and miniclient's reader behind it, for the life of the process.
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

// Run runs the given command against minimega, automatically redialing the
// shared connection if it was disconnected. Any errors encountered will be
// returned as part of the response channel, which the caller must drain. A
// command waiting longer than DefaultWaitTimeout for the shared connection, or
// holding it longer than DefaultCommandTimeout, returns ErrTimeout.
//
// Code serving a web request or a background job uses RunContext instead.
func Run(c *Command) chan *miniclient.Response {
	return runShared(context.Background(), c, nil)
}

// RunStarted is Run, calling started (if not nil) just before the command is
// sent, once it is no longer waiting behind other commands for the shared
// connection. started may run with the shared connection's lock held, so it
// must be quick and must not run minimega commands itself.
func RunStarted(c *Command, started func()) chan *miniclient.Response {
	return runShared(context.Background(), c, started)
}

// RunContext is Run for a command run on behalf of something that may end
// first, such as a web request whose client may leave or a job that may be
// canceled: once ctx is done, a command still waiting for the shared
// connection gives up without having been sent, returning an error that wraps
// ctx's error (ErrTimeout for its deadline). A command already sent is not
// interrupted, since that would disturb the shared connection; it is bounded
// by ctx's deadline, if any, or else by DefaultCommandTimeout. Whatever ctx
// says, a command waits no longer than DefaultWaitTimeout, as with Run.
func RunContext(ctx context.Context, c *Command) chan *miniclient.Response {
	return runShared(ctx, c, nil)
}

// RunStartedContext is RunContext, calling started as RunStarted does.
func RunStartedContext(ctx context.Context, c *Command, started func()) chan *miniclient.Response {
	return runShared(ctx, c, started)
}

// runShared runs c on the shared connection once it is free, unless c has a
// timeout of its own, which gives it a private connection. Nothing is sent
// once ctx is done.
func runShared(ctx context.Context, c *Command, started func()) chan *miniclient.Response {
	cmdStr := c.String()

	// checked first, as select picks at random when the connection is free too
	if err := ctx.Err(); err != nil {
		return wrapErr(gaveUp(err))
	}

	if c.Timeout > 0 {
		notify(started)

		return runPrivate(cmdStr, c.Timeout)
	}

	wait, cancel := context.WithTimeout(ctx, DefaultWaitTimeout)
	defer cancel()

	select {
	case shared <- struct{}{}:
	case <-wait.Done():
		return wrapErr(gaveUp(wait.Err()))
	}

	release := func() { <-shared }

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
// connection because err ended its wait: ErrTimeout for a deadline, else err.
func gaveUp(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		err = ErrTimeout
	}

	return fmt.Errorf("waiting for the minimega connection: %w", err)
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
