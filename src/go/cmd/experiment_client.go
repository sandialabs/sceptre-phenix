package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// scorchWaitInterval is how often phenix experiment scorch asks the server
	// for the status of the run it waits for.
	scorchWaitInterval = time.Second

	// scorchCancelTimeout bounds the cancel request that an interrupt sends.
	scorchCancelTimeout = 10 * time.Second

	scorchStatusSuccess = "success"
	scorchStatusUnknown = "unknown"
	scorchStageDone     = "done"
	scorchStageCleanup  = "cleanup"
)

// errScorchRunFailed is the error of a SCORCH run that the server executed
// and that did not succeed.
var errScorchRunFailed = errors.New("scorch run did not succeed")

// experimentClient calls the experiment routes of the running phenix server
// over its unix socket: the cancel of triggered apps and the SCORCH runs. The
// server holds the cancelers of the runs that it executes, so only it can
// report or cancel them.
type experimentClient struct {
	*workflowClient

	socket string
}

// newExperimentClient returns a client of the server listening on socket.
func newExperimentClient(socket string) *experimentClient {
	return &experimentClient{workflowClient: newWorkflowClient(socket), socket: socket}
}

// scorchNode is a stage or component node of a SCORCH pipeline. A stage node
// has no stage.
type scorchNode struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Stage  string `json:"stage"`
}

// label is the node's name, after its stage for a component node.
func (n scorchNode) label() string {
	if n.Stage == "" {
		return n.Name
	}

	return n.Stage + "/" + n.Name
}

// scorchPipeline is the top-level loop of a SCORCH run.
type scorchPipeline struct {
	Name       string       `json:"name"`
	HasCleanup bool         `json:"hasCleanup"`
	Pipeline   []scorchNode `json:"pipeline"`
}

// status returns the status of the stage node named stage, or "" when the
// pipeline has no such node.
func (p scorchPipeline) status(stage string) string {
	for _, n := range p.Pipeline {
		if n.Stage == "" && n.Name == stage {
			return n.Status
		}
	}

	return ""
}

// scorchPipelines is the answer of GET /experiments/{name}/scorch/pipelines.
// Executing is nil when the server does not report the runs that it executes.
type scorchPipelines struct {
	Pipelines []scorchPipeline `json:"pipelines"`
	Executing *[]int           `json:"executing"`
}

// run returns the pipeline of run, or an error when the experiment has no
// such run.
func (p scorchPipelines) run(exp string, run int) (scorchPipeline, error) {
	if run < 0 || run >= len(p.Pipelines) {
		return scorchPipeline{}, fmt.Errorf("experiment %s has no Scorch run %d", exp, run)
	}

	return p.Pipelines[run], nil
}

// executing reports whether the server executes run. It is an error when the
// server does not report the runs that it executes.
func (p scorchPipelines) executing(run int) (bool, error) {
	if p.Executing == nil {
		return false, errors.New(
			"the phenix server does not report the Scorch runs that it executes; use the same phenix version for the command and the server",
		)
	}

	return slices.Contains(*p.Executing, run), nil
}

// scorchPath is the path of the SCORCH pipelines of experiment exp, with the
// parts given after it.
func scorchPath(exp string, parts ...string) string {
	var path strings.Builder

	path.WriteString("/api/v1/experiments/" + url.PathEscape(exp) + "/scorch/pipelines")

	for _, part := range parts {
		path.WriteString("/" + url.PathEscape(part))
	}

	return path.String()
}

// scorchPipelines returns the pipelines of every SCORCH run of experiment exp.
func (c *experimentClient) scorchPipelines(ctx context.Context, exp string) (scorchPipelines, error) {
	var p scorchPipelines

	err := c.send(ctx, http.MethodGet, scorchPath(exp), nil, &p)

	return p, err
}

// startScorch starts run of experiment exp on the server, or only its cleanup
// stage when cleanupOnly is set.
func (c *experimentClient) startScorch(ctx context.Context, exp string, run int, cleanupOnly bool) error {
	parts := []string{strconv.Itoa(run)}
	if cleanupOnly {
		parts = append(parts, scorchStageCleanup)
	}

	return c.send(ctx, http.MethodPost, scorchPath(exp, parts...), nil, nil)
}

// cancelScorch cancels run of experiment exp, if the server executes it.
func (c *experimentClient) cancelScorch(ctx context.Context, exp string, run int) error {
	return c.send(ctx, http.MethodDelete, scorchPath(exp, strconv.Itoa(run)), nil, nil)
}

// clearScorch clears the status of run of experiment exp.
func (c *experimentClient) clearScorch(ctx context.Context, exp string, run int) error {
	return c.send(ctx, http.MethodPost, scorchPath(exp, strconv.Itoa(run), "clear"), nil, nil)
}

// cancelTriggers cancels the apps that the server runs a trigger of for
// experiment exp: those named, or all of them when apps is empty.
func (c *experimentClient) cancelTriggers(ctx context.Context, exp string, apps []string) error {
	query := url.Values{}
	if len(apps) > 0 {
		query.Set("apps", strings.Join(apps, ","))
	}

	return c.send(ctx, http.MethodDelete, "/api/v1/experiments/"+url.PathEscape(exp)+"/trigger", query, nil)
}

// send sends a request without a body, and decodes the JSON answer into out
// unless out is nil.
func (c *experimentClient) send(ctx context.Context, method, path string, query url.Values, out any) error {
	target := c.base + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	_, body, err := c.do(req)
	if err != nil {
		return err
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding phenix server answer: %w", err)
	}

	return nil
}

// notRunning returns err, which a request to the server returned, with what
// exists only in the running server when the server did not answer.
func (c *experimentClient) notRunning(err error, only string) error {
	if !serverAbsent(err) {
		return err
	}

	return fmt.Errorf("phenix ui is not running (no socket at %s), and %s: %w", c.socket, only, err)
}

// serverAbsent reports whether err, which a request to the server returned,
// says that no server listens on the socket. A socket that refuses the
// connection for permission has a server.
func serverAbsent(err error) bool {
	return errors.Is(err, errServerUnreachable) && !errors.Is(err, fs.ErrPermission)
}

// scorchProgress writes a line for each node of a SCORCH run's pipeline whose
// status changes.
type scorchProgress struct {
	w    io.Writer
	run  int
	seen map[string]string
}

// newScorchProgress returns the progress of run, which shows only the
// changes after the statuses in before.
func newScorchProgress(w io.Writer, run int, before scorchPipeline) *scorchProgress {
	p := &scorchProgress{w: w, run: run, seen: map[string]string{}}

	for _, n := range before.Pipeline {
		p.seen[n.label()] = n.Status
	}

	return p
}

// update writes a line for each node of pl whose status changed. A status
// that a new run resets to unknown gets no line.
func (p *scorchProgress) update(pl scorchPipeline) {
	for _, n := range pl.Pipeline {
		label := n.label()

		if p.seen[label] == n.Status {
			continue
		}

		p.seen[label] = n.Status

		if n.Status != "" && n.Status != scorchStatusUnknown {
			fmt.Fprintf(p.w, "Scorch run %d: %s %s\n", p.run, label, n.Status)
		}
	}
}

// scorchRunStatus is what phenix experiment scorch --status shows: the
// status of each node of the pipeline of a run, and whether the server
// executes the run.
type scorchRunStatus struct {
	Experiment string       `json:"experiment"`
	Run        int          `json:"run"`
	Name       string       `json:"name"`
	Executing  bool         `json:"executing"`
	Pipeline   []scorchNode `json:"pipeline"`
}

// newScorchRunStatus returns the status of run of experiment exp in
// pipelines, or an error when the experiment has no such run or the server
// does not report the runs that it executes.
func newScorchRunStatus(exp string, run int, pipelines scorchPipelines) (scorchRunStatus, error) {
	pl, err := pipelines.run(exp, run)
	if err != nil {
		return scorchRunStatus{}, err
	}

	executing, err := pipelines.executing(run)
	if err != nil {
		return scorchRunStatus{}, err
	}

	return scorchRunStatus{Experiment: exp, Run: run, Name: pl.Name, Executing: executing, Pipeline: nonNil(pl.Pipeline)}, nil
}

// printScorchStatus writes the status of each node of the run's pipeline,
// and whether the server executes the run.
func printScorchStatus(w io.Writer, s scorchRunStatus) {
	name := ""
	if s.Name != "" {
		name = " (" + s.Name + ")"
	}

	state := "not executing"
	if s.Executing {
		state = "executing"
	}

	fmt.Fprintf(w, "Scorch run %d%s of experiment %s: %s\n", s.Run, name, s.Experiment, state)

	width := 0
	for _, n := range s.Pipeline {
		width = max(width, len(n.label()))
	}

	for _, n := range s.Pipeline {
		fmt.Fprintf(w, "  %-*s  %s\n", width, n.label(), n.Status)
	}
}

// scorchWait waits for a SCORCH run that the server executes.
type scorchWait struct {
	client      *experimentClient
	exp         string
	run         int
	cleanupOnly bool
	interval    time.Duration
	out         io.Writer
}

// startAndWait starts the run on the server and waits until it ends, and
// writes its progress. It returns an error that wraps errScorchRunFailed when
// the run did not succeed. When ctx ends first, it cancels the run on the
// server and returns [context.Canceled].
func (s scorchWait) startAndWait(ctx context.Context) error {
	before, err := s.client.scorchPipelines(ctx, s.exp)
	if err != nil {
		return err
	}

	pl, err := before.run(s.exp, s.run)
	if err != nil {
		return err
	}

	if _, err := before.executing(s.run); err != nil {
		return err
	}

	if err := s.client.startScorch(ctx, s.exp, s.run, s.cleanupOnly); err != nil {
		if ctx.Err() != nil {
			return s.cancel(ctx)
		}

		return err
	}

	what, phrase := "Scorch run", "Scorch run"
	if s.cleanupOnly {
		what, phrase = "Cleanup stage of Scorch run", "the cleanup stage of Scorch run"
	}

	fmt.Fprintf(s.out, "%s %d of experiment %s started on the phenix server\n", what, s.run, s.exp)

	final, err := s.wait(ctx, newScorchProgress(s.out, s.run, pl))
	if err != nil {
		if ctx.Err() != nil {
			return s.cancel(ctx)
		}

		return err
	}

	stage := scorchStageDone
	if s.cleanupOnly {
		stage = scorchStageCleanup
	}

	status := final.status(stage)
	if status != scorchStatusSuccess {
		return fmt.Errorf(
			"%w: %s %d of experiment %s ended with %s status %q",
			errScorchRunFailed, phrase, s.run, s.exp, stage, status,
		)
	}

	fmt.Fprintf(s.out, "%s %d of experiment %s succeeded\n", what, s.run, s.exp)

	return nil
}

// wait asks for the run's pipeline every interval until the server no longer
// executes the run, and returns the run's last pipeline.
func (s scorchWait) wait(ctx context.Context, progress *scorchProgress) (scorchPipeline, error) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		pipelines, err := s.client.scorchPipelines(ctx, s.exp)
		if err != nil {
			return scorchPipeline{}, err
		}

		pl, err := pipelines.run(s.exp, s.run)
		if err != nil {
			return scorchPipeline{}, err
		}

		progress.update(pl)

		executing, err := pipelines.executing(s.run)
		if err != nil {
			return scorchPipeline{}, err
		}

		if !executing {
			return pl, nil
		}

		select {
		case <-ctx.Done():
			return pl, fmt.Errorf("waiting for Scorch run %d: %w", s.run, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// cancel cancels the run on the server after an interrupt ended ctx, and
// returns [context.Canceled].
func (s scorchWait) cancel(ctx context.Context) error {
	cancelCtx, done := context.WithTimeout(context.WithoutCancel(ctx), scorchCancelTimeout)
	defer done()

	if err := s.client.cancelScorch(cancelCtx, s.exp, s.run); err != nil {
		return fmt.Errorf("canceling Scorch run %d of experiment %s on the phenix server: %w", s.run, s.exp, err)
	}

	fmt.Fprintf(s.out, "Canceled Scorch run %d of experiment %s on the phenix server\n", s.run, s.exp)

	return context.Canceled
}
