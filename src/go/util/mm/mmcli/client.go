// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
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

var ErrTimeout = errors.New("timeout running command")

var (
	mu     sync.Mutex       //nolint:gochecknoglobals // global lock
	mm     *miniclient.Conn //nolint:gochecknoglobals // global connection
	mmDead bool             //nolint:gochecknoglobals // flags mm for replacement
)

func wrapErr(err error) chan *miniclient.Response {
	out := make(chan *miniclient.Response, 1)

	out <- &miniclient.Response{ //nolint:exhaustruct // partial initialization
		Resp: minicli.Responses{
			&minicli.Response{ //nolint:exhaustruct // partial initialization
				Error: err.Error(),
			},
		},
		More: false,
	}

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
				errs = multierror.Append(errs, errors.New(resp.Error))
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
				err = errors.New(r.Error)

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
				err = errors.New(r.Error)

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
	if mm == nil || mmDead {
		c, err := miniclient.Dial(common.MinimegaBase)
		if err != nil {
			return nil, fmt.Errorf("unable to dial: %w", err)
		}

		mm, mmDead = c, false
	}

	// If the connection is already in a broken state, redial.
	if err := mm.Error(); err != nil {
		s := err.Error()

		if strings.Contains(s, "broken pipe") || strings.Contains(s, "no such file or directory") {
			c, err := miniclient.Dial(common.MinimegaBase)
			if err != nil {
				return nil, fmt.Errorf("unable to redial: %w", err)
			}

			mm, mmDead = c, false
		} else {
			return nil, fmt.Errorf("minimega error: %w", err)
		}
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

// Run dials the minimega Unix socket and runs the given command, automatically
// redialing if disconnected. Any errors encountered will be returned as part of
// the response channel.
func Run(c *Command) chan *miniclient.Response {
	mu.Lock()

	active, err := conn()
	if err != nil {
		mu.Unlock()

		return wrapErr(err)
	}

	// Build the command string and release mu before waiting on the response.
	// The connection's own internal lock serializes the actual exchange, so we
	// don't need to (and must not) hold mu across a potentially slow command --
	// doing so would serialize all minimega traffic behind one slow command.
	cmdStr := c.String()

	mu.Unlock()

	if c.Timeout == 0 {
		return active.Run(cmdStr)
	}

	var (
		resp = make(chan chan *miniclient.Response, 1)
		done = make(chan struct{})
	)

	go func() {
		resp <- active.Run(cmdStr)

		close(done)
	}()

	select {
	case <-done:
		return <-resp
	case <-time.After(c.Timeout):
		// Dispatch is stuck, close it so the goroutine fails
		active.Close()
		// Flag it so the next call redials
		markDead(active)

		return wrapErr(ErrTimeout)
	}
}
