package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"

	"phenix/util/plog"
)

// serverClient calls the API routes that the running phenix server serves on its unix socket, for state that only
// that server holds. Unlike workflowClient.do, it can stream a response body.
type serverClient struct {
	*workflowClient

	socket string
	// held says what only the running server holds, for the error when it does not answer
	held string
}

// newServerClient returns a client of the server listening on socket. held completes "phenix ui is not running, and"
// in the error when that server does not answer.
func newServerClient(socket, held string) *serverClient {
	return &serverClient{workflowClient: newWorkflowClient(socket), socket: socket, held: held}
}

// open sends a request without a body and returns the response of a 2xx answer, whose body the caller closes. Any
// other status becomes a *workflowAPIError. A server that does not answer is reported as not running.
func (c *serverClient) open(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	target := c.base + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	plog.Debug(plog.TypeSystem, "phenix server request", "method", req.Method, "url", req.URL.String())

	resp, err := c.http.Do(req) //nolint:gosec // G704 the phenix server the user configured
	if err != nil {
		if ctx.Err() != nil {
			return nil, endedRequestError(req, err)
		}

		if errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("%w: %w; %s", errServerUnreachable, err, socketPermissionHint)
		}

		return nil, fmt.Errorf(
			"phenix ui is not running (no socket at %s), and %s: %w: %w", c.socket, c.held, errServerUnreachable, err,
		)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		return nil, newWorkflowAPIError(resp.StatusCode, body)
	}

	return resp, nil
}

// send sends a request without a body, and ignores the body of a 2xx answer.
func (c *serverClient) send(ctx context.Context, method, path string) error {
	resp, err := c.open(ctx, method, path, nil)
	if err != nil {
		return err
	}

	_ = resp.Body.Close()

	return nil
}

// getJSON decodes the JSON answer to a GET of path into out.
func (c *serverClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.open(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding phenix server answer: %w", err)
	}

	return nil
}

// download copies the answer to a GET of path to w.
func (c *serverClient) download(ctx context.Context, path string, query url.Values, w io.Writer) error {
	resp, err := c.open(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if _, err := io.Copy(w, resp.Body); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("contacting phenix server: %w", ctx.Err())
		}

		return fmt.Errorf("%w: reading the response: %w", errServerUnreachable, err)
	}

	return nil
}

// apiStatus returns the HTTP status of a *workflowAPIError in err's chain, or 0.
func apiStatus(err error) int {
	var apiErr *workflowAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}

	return 0
}

// call sends a request to the API path with the JSON of in as its body, when
// in is not nil, and decodes a JSON answer into out, when out is not nil. A
// server that does not answer is reported as not running.
func (c *serverClient) call(ctx context.Context, method, path string, query url.Values, in, out any) error {
	target := c.base + "/api/v1" + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	var body io.Reader = http.NoBody

	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}

		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	_, resp, err := c.do(req)
	if errors.Is(err, errServerUnreachable) {
		return fmt.Errorf("phenix ui is not running (no socket at %s), and %s: %w", c.socket, c.held, err)
	}

	if err != nil {
		return err
	}

	if out == nil || len(resp) == 0 {
		return nil
	}

	if err := json.Unmarshal(resp, out); err != nil {
		return fmt.Errorf("decoding phenix server answer: %w", err)
	}

	return nil
}
