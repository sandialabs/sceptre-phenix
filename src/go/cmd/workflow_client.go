package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phenix/api/workflow"
	"phenix/util/plog"
)

// socketPermissionHint ends the error of a connection refused for
// permission. phenix ui usually runs as root, and only the users that its
// socket's mode allows may connect.
const socketPermissionHint = "run phenix as the user that runs phenix ui, or as a member of the group set by phenix ui --unix-socket-gid"

var (
	// errServerUnreachable marks a request that got no answer from the phenix
	// server: the connection could not be made, or it broke before the whole
	// response arrived. The server may have stopped or restarted.
	errServerUnreachable = errors.New("phenix server unreachable")

	// errNoConfigDryRun marks an answer to a config dry run that is not one.
	// An older phenix server ignores dryRun there and stores the config.
	errNoConfigDryRun = errors.New("the phenix server did not dry-run the config")

	// errNoWorkflowDryRun marks an answer to a workflow dry run that is not
	// one: it says dryRun is false, or its action is not one the command
	// knows. A server that ignores dryRun there applies the workflow config.
	errNoWorkflowDryRun = errors.New("the phenix server did not dry-run the workflow config")
)

// unknownActionError is an answer to a workflow dry run that says dryRun but
// holds an action the command does not know, as a phenix server of another
// version may send. It wraps errNoWorkflowDryRun.
type unknownActionError struct {
	action workflow.Action
}

// Error names the action and what to do.
func (e *unknownActionError) Error() string {
	return fmt.Sprintf(
		"the phenix server answered the dry run with the unknown action %q; use the same phenix version for the command and the server",
		e.action,
	)
}

// Unwrap returns errNoWorkflowDryRun.
func (e *unknownActionError) Unwrap() error {
	return errNoWorkflowDryRun
}

// preflightTimeout bounds each request of the preflight: the options and the
// dry runs of the configs and of the workflow config, which the phenix server
// answers at once. A server that accepts the connection and never answers
// then stops the run instead of holding it. The config upserts and the real
// apply may take long, so they are not bounded. The root command's own
// request for the options has the same bound.
const preflightTimeout = time.Minute

// preflightRequestTimeout is preflightTimeout. Tests lower it.
var preflightRequestTimeout = preflightTimeout //nolint:gochecknoglobals // lowered by tests

// noAnswerError describes a preflight request that got no answer within d.
// It wraps errServerUnreachable.
func noAnswerError(d time.Duration) error {
	return fmt.Errorf("%w: no answer within %s", errServerUnreachable, d)
}

// withPreflightTimeout returns a child of ctx for one preflight request. It
// ends after preflightRequestTimeout, with the cause from noAnswerError.
func withPreflightTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, preflightRequestTimeout, noAnswerError(preflightRequestTimeout))
}

// workflowClient calls the workflow API of the running phenix server over its
// unix socket.
type workflowClient struct {
	http *http.Client
	base string
}

// newWorkflowClient returns a client whose requests all dial socket.
func newWorkflowClient(socket string) *workflowClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer

			return dialer.DialContext(ctx, "unix", socket)
		},
	}

	return &workflowClient{
		http: &http.Client{Transport: transport},
		base: "http://unix",
	}
}

// options returns the decoded body of GET /api/v1/options. The request is
// bounded by preflightRequestTimeout.
func (c *workflowClient) options(ctx context.Context) (map[string]any, error) {
	ctx, cancel := withPreflightTimeout(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/options", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building options request: %w", err)
	}

	_, body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	opts := map[string]any{}

	if err := json.Unmarshal(body, &opts); err != nil {
		return nil, fmt.Errorf("decoding phenix server options: %w", err)
	}

	return opts, nil
}

// apply posts a workflow config to /api/v1/workflow/apply/{branch} and
// returns the server's result. Each tag is sent as its own tag query
// parameter, and each pending ref, as a config dry run returned it, as its
// own pending query parameter. dryRun asks for the plan without acting on it.
// pending names the configs that the caller upserts before its real apply;
// the server accepts them only on a dry run. A non-empty expect asks the
// server to change nothing unless its plan has that action. A dry run is
// bounded by preflightRequestTimeout; the real apply is not. The answer to a
// dry run must say dryRun and hold a known action; any other answer returns
// an error that wraps errNoWorkflowDryRun, a *unknownActionError when only
// the action is unknown.
func (c *workflowClient) apply(
	ctx context.Context,
	branch string,
	body []byte,
	contentType string,
	tags []string,
	dryRun bool,
	pending []string,
	expect workflow.Action,
) (workflow.Result, error) {
	query := url.Values{"tag": tags, "pending": pending}

	if dryRun {
		query.Set("dryRun", boolStringTrue)

		var cancel context.CancelFunc

		ctx, cancel = withPreflightTimeout(ctx)
		defer cancel()
	}

	if expect != "" {
		query.Set("expect", string(expect))
	}

	_, resp, err := c.post(ctx, "/api/v1/workflow/apply/"+url.PathEscape(branch), query, body, contentType)
	if err != nil {
		return workflow.Result{}, err
	}

	var result workflow.Result

	if err := json.Unmarshal(resp, &result); err != nil {
		return workflow.Result{}, fmt.Errorf("decoding apply result: %w", err)
	}

	if dryRun {
		if !result.DryRun {
			return workflow.Result{}, fmt.Errorf("%w; it may have applied it; upgrade the phenix server", errNoWorkflowDryRun)
		}

		if _, err := workflow.ParseAction(string(result.Action)); err != nil {
			return workflow.Result{}, &unknownActionError{action: result.Action}
		}
	}

	return result, nil
}

// configDryRun asks the server what an upsert of one config file would do,
// without storing it, through POST /api/v1/workflow/configs/{branch} with
// dryRun=true. The result's kind, name and pending ref are the config's after
// the server filled in its placeholders. Only a 200 whose body says dryRun is
// a config dry run. Any other answer, such as the empty 201 of an older
// server that ignores dryRun and stores the config, returns an error that
// wraps errNoConfigDryRun and names the status. The request is bounded by
// preflightRequestTimeout.
func (c *workflowClient) configDryRun(
	ctx context.Context,
	branch string,
	body []byte,
	contentType string,
) (workflow.ConfigResult, error) {
	ctx, cancel := withPreflightTimeout(ctx)
	defer cancel()

	query := url.Values{"dryRun": {boolStringTrue}}

	status, resp, err := c.post(ctx, "/api/v1/workflow/configs/"+url.PathEscape(branch), query, body, contentType)
	if err != nil {
		return workflow.ConfigResult{}, err
	}

	var result workflow.ConfigResult

	if status != http.StatusOK || json.Unmarshal(resp, &result) != nil || !result.DryRun {
		return workflow.ConfigResult{}, fmt.Errorf(
			"%w: it answered %d %s; it may have stored it; upgrade the phenix server",
			errNoConfigDryRun, status, http.StatusText(status),
		)
	}

	return result, nil
}

// upsertConfig posts one config file to /api/v1/workflow/configs/{branch},
// which stores it.
func (c *workflowClient) upsertConfig(ctx context.Context, branch string, body []byte, contentType string) error {
	_, _, err := c.post(ctx, "/api/v1/workflow/configs/"+url.PathEscape(branch), nil, body, contentType)

	return err
}

// post sends body to path with the given query and Content-Type header, and
// returns the status and body of a 2xx response.
func (c *workflowClient) post(ctx context.Context, path string, query url.Values, body []byte, contentType string) (int, []byte, error) {
	target := c.base + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)

	return c.do(req)
}

// do sends req and returns the status and body of a 2xx response. Any other
// status becomes a *workflowAPIError, and a request that got no answer wraps
// errServerUnreachable unless its context ended first. A preflight request
// whose time bound ended while the caller's context was alive got no answer
// too. A connection refused for permission also gets socketPermissionHint.
// Requests and responses are logged at debug level.
func (c *workflowClient) do(req *http.Request) (int, []byte, error) {
	plog.Debug(plog.TypeSystem, "phenix server request", "method", req.Method, "url", req.URL.String(), "bytes", req.ContentLength)

	resp, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return 0, nil, endedRequestError(req, err)
		}

		if errors.Is(err, fs.ErrPermission) {
			return 0, nil, fmt.Errorf("%w: %w; %s", errServerUnreachable, err, socketPermissionHint)
		}

		return 0, nil, fmt.Errorf("%w: %w", errServerUnreachable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if req.Context().Err() != nil {
			return 0, nil, endedRequestError(req, err)
		}

		return 0, nil, fmt.Errorf("%w: reading the response: %w", errServerUnreachable, err)
	}

	plog.Debug(
		plog.TypeSystem, "phenix server response",
		"method", req.Method, "url", req.URL.String(), "status", resp.StatusCode, "body", string(body),
	)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, nil, newWorkflowAPIError(resp.StatusCode, body)
	}

	return resp.StatusCode, body, nil
}

// endedRequestError describes a request whose context ended before its answer
// arrived. When the time bound of a preflight request ended it, while the
// caller's context was alive, the cause from noAnswerError is returned, which
// wraps errServerUnreachable. Otherwise the caller's context ended: an
// interrupt.
func endedRequestError(req *http.Request, err error) error {
	if cause := context.Cause(req.Context()); errors.Is(cause, errServerUnreachable) {
		return cause
	}

	return fmt.Errorf("contacting phenix server: %w", err)
}

// workflowAPIError is a non-2xx response from the phenix server, decoded from
// its JSON error envelope. Validation holds the explained validation lines
// (metadata.validation) of a rejected config, one per line.
type workflowAPIError struct {
	Status     int
	Message    string
	Cause      string
	Validation string
}

// newWorkflowAPIError decodes the error envelope in body. A body that is not
// JSON, such as the empty body of a bare 500, becomes the message as is.
func newWorkflowAPIError(status int, body []byte) *workflowAPIError {
	var envelope apiErrorBody

	if err := json.Unmarshal(body, &envelope); err != nil {
		return &workflowAPIError{Status: status, Message: strings.TrimSpace(string(body)), Cause: "", Validation: ""}
	}

	return &workflowAPIError{
		Status:     status,
		Message:    envelope.Message,
		Cause:      envelope.Cause,
		Validation: envelope.Metadata["validation"],
	}
}

// Error returns the status and the server's message. The cause is added
// unless the server sent validation lines: callers log those one by one, and
// the cause only repeats them as raw validator text.
func (e *workflowAPIError) Error() string {
	msg := fmt.Sprintf("phenix server returned %d %s", e.Status, http.StatusText(e.Status))

	if e.Message != "" {
		msg += ": " + e.Message
	}

	if e.Cause != "" && e.Validation == "" {
		msg += ": " + e.Cause
	}

	return msg
}

// apiErrorBody is the JSON error envelope written by the phenix web server.
type apiErrorBody struct {
	Cause    string            `json:"cause"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}
