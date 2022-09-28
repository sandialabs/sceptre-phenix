// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"phenix/util/common"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/hashicorp/go-multierror"
)

var ErrTimeout = fmt.Errorf("timeout running command")

var (
	mu sync.Mutex
	mm *miniclient.Conn
)

// noop returns a closed channel
func noop() chan *miniclient.Response {
	out := make(chan *miniclient.Response)
	close(out)

	return out
}

func wrapErr(err error) chan *miniclient.Response {
	out := make(chan *miniclient.Response, 1)

	out <- &miniclient.Response{
		Resp: minicli.Responses{
			&minicli.Response{
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

// SingleReponse is used when only a single response (or error) is expected to
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

// SingleDataReponse is used when only a single response (or error) is expected
// to be returned from a call to minimega, and the response just includes user
// data. It returns the first non-error data response and the last error
// encountered (if no non-error responses were encountered).
func SingleDataResponse(responses chan *miniclient.Response) (interface{}, error) {
	var (
		data interface{}
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

// Run dials the minimega Unix socket and runs the given command, automatically
// redialing if disconnected. Any errors encountered will be returned as part of
// the response channel.
func Run(c *Command) chan *miniclient.Response {
	mu.Lock()
	defer mu.Unlock()

	var err error

	if mm == nil {
		if mm, err = miniclient.Dial(common.MinimegaBase); err != nil {
			return wrapErr(fmt.Errorf("unable to dial: %w", err))
		}
	}

	// check if there's already an error and try to redial
	if err := mm.Error(); err != nil {
		s := err.Error()

		if strings.Contains(s, "broken pipe") || strings.Contains(s, "no such file or directory") {
			if mm, err = miniclient.Dial(common.MinimegaBase); err != nil {
				return wrapErr(fmt.Errorf("unable to redial: %w", err))

			}
		} else {
			return wrapErr(fmt.Errorf("minimega error: %w", err))
		}
	}

	if c.Timeout == 0 {
		return mm.Run(c.String())
	}

	var (
		resp chan *miniclient.Response
		done = make(chan struct{})
	)

	go func() {
		resp = mm.Run(c.String())
		close(done)
	}()

	select {
	case <-done:
		return resp
	case <-time.After(c.Timeout):
		// Reset mm since the miniclient has a lock that is likely still activated.
		mm = nil
		return wrapErr(ErrTimeout)
	}
}
