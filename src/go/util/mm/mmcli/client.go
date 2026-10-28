// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

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
	mu            sync.Mutex       //nolint:gochecknoglobals // global lock
	mm            *miniclient.Conn //nolint:gochecknoglobals // global connection
	mmDead        bool             //nolint:gochecknoglobals // flags mm for replacement
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
	for _, sentinel := range []error{ErrTimeout, ErrNoResponse} {
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
// The caller must hold mu.
func conn() (*miniclient.Conn, error) {
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
// release is called once the terminal error has been read, before markDead,
// which takes mu itself. It must not be called any earlier: miniclient closes
// the response channel before releasing its own connection lock, so another
// command could otherwise take that lock and overwrite Conn.err first.
//
// Callers MUST drain the returned channel. Abandoning it blocks this goroutine,
// and miniclient's reader behind it, for the life of the process.
func guard(
	c *miniclient.Conn,
	in chan *miniclient.Response,
	release func(),
) chan *miniclient.Response {
	out := make(chan *miniclient.Response)

	go func() {
		defer close(out)

		var count int

		for resp := range in {
			count++

			out <- resp
		}

		err := streamEnded(c, count)

		release()

		if err == nil {
			return
		}

		markDead(c)

		out <- errResponse(err)
	}()

	return out
}

// Run runs the given command against minimega, automatically redialing the
// shared connection if it was disconnected. Any errors encountered will be
// returned as part of the response channel, which the caller must drain.
func Run(c *Command) chan *miniclient.Response {
	cmdStr := c.String()

	if c.Timeout > 0 {
		return runWithTimeout(cmdStr, c.Timeout)
	}

	mu.Lock()

	active, err := conn()
	if err != nil {
		mu.Unlock()

		return wrapErr(err)
	}

	// mu is held until guard has read the terminal error, so no other command
	// can reach Conn.err in between
	return guard(active, active.Run(cmdStr), mu.Unlock)
}

// runWithTimeout runs a command on a connection of its own, so that abandoning
// it on timeout cannot disturb commands in flight on the shared connection.
//
// Callers MUST drain the returned channel, as with guard.
func runWithTimeout(cmdStr string, timeout time.Duration) chan *miniclient.Response {
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
			after = time.After(timeout)
			count int
		)

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
