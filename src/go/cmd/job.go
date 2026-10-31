package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"phenix/api/job"
	"phenix/util/common"
	"phenix/util/printer"
	"phenix/util/sigterm"
)

const (
	defaultJobWaitInterval = time.Second
	outputJSON             = "json"
)

// exitPartial is the exit code of phenix job wait for a job that ended
// partial: some of its items succeeded and others did not.
const exitPartial = 2

// errJobNotSucceeded is the error of phenix job wait for a job that did not
// succeed.
var errJobNotSucceeded = errors.New("job did not succeed")

// exitCodeError is an error that sets the command's exit code.
type exitCodeError struct {
	err  error
	code int
}

// Error returns the wrapped error's message.
func (e *exitCodeError) Error() string {
	return e.err.Error()
}

// Unwrap returns the wrapped error.
func (e *exitCodeError) Unwrap() error {
	return e.err
}

// ExitCode returns the exit code the command ends with.
func (e *exitCodeError) ExitCode() int {
	return e.code
}

// jobClient reads and cancels the running phenix server's background jobs over
// its unix socket.
type jobClient struct {
	*workflowClient

	socket string
}

// newJobClient returns a client of the server listening on socket.
func newJobClient(socket string) *jobClient {
	return &jobClient{workflowClient: newWorkflowClient(socket), socket: socket}
}

// list returns the jobs filter selects, newest first.
func (c *jobClient) list(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	query := url.Values{}

	if filter.Experiment != "" {
		query.Set("experiment", filter.Experiment)
	}

	if filter.Kind != "" {
		query.Set("kind", filter.Kind)
	}

	if filter.Active {
		query.Set("active", boolStringTrue)
	}

	var body struct {
		Jobs []job.Job `json:"jobs"`
	}

	if err := c.call(ctx, http.MethodGet, "/api/v1/jobs", query, &body); err != nil {
		return nil, err
	}

	return body.Jobs, nil
}

// get returns the job with the given ID.
func (c *jobClient) get(ctx context.Context, id string) (job.Job, error) {
	var j job.Job

	err := c.call(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(id), nil, &j)

	return j, err
}

// cancel asks the job with the given ID to stop, and returns it.
func (c *jobClient) cancel(ctx context.Context, id string) (job.Job, error) {
	var j job.Job

	err := c.call(ctx, http.MethodPost, "/api/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, &j)

	return j, err
}

// call sends a request without a body and decodes the JSON answer into out.
// A server that does not answer is reported as not running.
func (c *jobClient) call(ctx context.Context, method, path string, query url.Values, out any) error {
	target := c.base + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	_, body, err := c.do(req)
	if errors.Is(err, errServerUnreachable) {
		return fmt.Errorf(
			"phenix ui is not running (no socket at %s), and jobs exist only in the running server: %w",
			c.socket, err,
		)
	}

	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding phenix server answer: %w", err)
	}

	return nil
}

// waitForJob asks for the job every interval until it finishes or ctx ends,
// and returns it as it finished.
func waitForJob(ctx context.Context, c *jobClient, id string, interval time.Duration) (job.Job, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		j, err := c.get(ctx, id)
		if err != nil {
			return job.Job{}, err
		}

		if j.Status.Terminal() {
			return j, nil
		}

		select {
		case <-ctx.Done():
			return j, fmt.Errorf("waiting for job %s (%s): %w", id, j.Status, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// jobOutcome is the error phenix job wait ends with for a finished job: none
// when it succeeded, exit code 2 when it ended partial, and 1 otherwise.
func jobOutcome(j job.Job) error {
	succeeded, failed, skipped, canceled := j.Counts()

	summary := fmt.Sprintf(
		"job %s %s: %d succeeded, %d failed, %d skipped, %d canceled",
		j.ID, j.Status, succeeded, failed, skipped, canceled,
	)

	if j.Error != "" {
		summary += ": " + j.Error
	}

	switch j.Status {
	case job.StatusSucceeded:
		return nil
	case job.StatusPartial:
		return &exitCodeError{err: fmt.Errorf("%w: %s", errJobNotSucceeded, summary), code: exitPartial}
	case job.StatusQueued, job.StatusRunning, job.StatusFailed, job.StatusCanceled:
	}

	return fmt.Errorf("%w: %s", errJobNotSucceeded, summary)
}

// printJob writes the job, in the output format given.
func printJob(w io.Writer, j job.Job, output string) error {
	if output == outputJSON {
		return writeJobJSON(w, j)
	}

	printer.PrintJob(w, outputPalette(w), j)

	return nil
}

// writeJobJSON writes v as indented JSON.
func writeJobJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}

func newJobCmd() *cobra.Command {
	desc := `Background job management

  Lists, follows and cancels the background jobs of the running phenix ui
  server (bulk VM actions, experiment file archives, disk clones), over its
  unix socket (--unix-socket), which acts as Global Admin and sees every
  user's jobs. Jobs live in that server's memory only, and are started from
  the web UI or the REST API: the CLI's own VM and disk commands run in the
  CLI process and are not jobs.`

	cmd := &cobra.Command{
		Use:   "job",
		Short: "Background job management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newJobListCmd(), newJobGetCmd(), newJobCancelCmd(), newJobWaitCmd())

	return cmd
}

func newJobListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List background jobs, newest first",
		Example: `
  # Every job the server holds
  phenix job list

  # Running and queued jobs of one experiment, as JSON
  phenix job list --experiment exp1 --active -o json`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter := job.Filter{
				Owner:      "",
				Experiment: mustGet(cmd.Flags().GetString, "experiment"),
				Kind:       mustGet(cmd.Flags().GetString, "kind"),
				Active:     mustGet(cmd.Flags().GetBool, "active"),
			}

			jobs, err := newJobClient(common.UnixSocket).list(commandContext(cmd), filter)
			if err != nil {
				return fmt.Errorf("listing jobs: %w", err)
			}

			if mustGet(cmd.Flags().GetString, "output") == outputJSON {
				return writeJobJSON(cmd.OutOrStdout(), jobs)
			}

			if len(jobs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No jobs")

				return nil
			}

			printer.PrintTableOfJobs(cmd.OutOrStdout(), outputPalette(cmd.OutOrStdout()), jobs...)

			return nil
		},
	}

	cmd.Flags().String("experiment", "", "only the jobs of this experiment")
	cmd.Flags().String("kind", "", "only jobs of this kind (vms.action, files.archive, disk.clone)")
	cmd.Flags().Bool("active", false, "only jobs queued or running")
	cmd.Flags().StringP("output", "o", "table", "output format ('table' or 'json')")

	return cmd
}

func newJobGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <ID>",
		Short: "Show a background job and the outcome of each item",
		Args:  argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := newJobClient(common.UnixSocket).get(commandContext(cmd), args[0])
			if err != nil {
				return fmt.Errorf("getting job %s: %w", args[0], err)
			}

			return printJob(cmd.OutOrStdout(), j, mustGet(cmd.Flags().GetString, "output"))
		},
	}

	cmd.Flags().StringP("output", "o", "table", "output format ('table' or 'json')")

	return cmd
}

func newJobCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <ID>",
		Short: "Cancel a background job",
		Long: `Cancel a background job

  A queued job is canceled at once. A running VM action job stops after the
  VMs in progress; archive and clone jobs stop at once and leave no file. Use
  phenix job wait to see it end.`,
		Args: argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := newJobClient(common.UnixSocket).cancel(commandContext(cmd), args[0])
			if err != nil {
				return fmt.Errorf("canceling job %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cancel requested for job %s (%s)\n", j.ID, j.Status)

			return nil
		},
	}

	return cmd
}

func newJobWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait <ID>",
		Short: "Wait for a background job to finish",
		Long: `Wait for a background job to finish

  Asks the server for the job every second until it finishes, then prints it.
  Exits 0 when it succeeded, 2 when it ended partial (some items succeeded and
  others did not), and 1 when it failed or was canceled, or when waiting
  failed or timed out.`,
		Example: `
  # Wait at most 10 minutes
  phenix job wait j0f3a9c2e81b4d7a6 --timeout 10m`,
		Args: argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout := mustGet(cmd.Flags().GetDuration, "timeout")

			ctx := sigterm.CancelContext(commandContext(cmd))

			if timeout > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timed out after %s", timeout))
				defer cancel()
			}

			j, err := waitForJob(ctx, newJobClient(common.UnixSocket), args[0], defaultJobWaitInterval)
			if err != nil {
				return err
			}

			if err := printJob(cmd.OutOrStdout(), j, mustGet(cmd.Flags().GetString, "output")); err != nil {
				return err
			}

			return jobOutcome(j)
		},
	}

	cmd.Flags().Duration("timeout", 0, "give up after this long (0 waits until the job finishes)")
	cmd.Flags().StringP("output", "o", "table", "output format ('table' or 'json')")

	return cmd
}

// commandContext returns the command's context, which is nil unless it was
// run with ExecuteContext.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}

	return context.Background()
}

// exitCode is the exit code for the error a command returned.
func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}

	return 1
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newJobCmd(), true)
}
