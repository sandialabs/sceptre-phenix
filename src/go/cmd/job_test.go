package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/cobra"

	"phenix/api/job"
	"phenix/util/common"
)

// runJobCmd runs phenix job with args against the server at socket, and
// returns its output and error.
func runJobCmd(t *testing.T, socket string, args ...string) (string, error) {
	t.Helper()

	original := common.UnixSocket
	common.UnixSocket = socket //nolint:reassign // point the command at the fake server

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	cmd := newJobCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

func testJob(status job.Status) job.Job {
	return job.Job{
		ID: "j1", Kind: "vms.action", Owner: "alice", Experiment: "exp1", Title: "Restart 2 VMs", Status: status,
		Progress: job.Progress{Done: 2, Total: 2},
		Items: []job.Item{
			{Name: "web", Status: job.ItemSucceeded},
			{Name: "db", Status: job.ItemFailed, Error: "vm db is locked with status stopping", Retryable: true},
		},
		Created: time.Date(2026, 10, 14, 15, 4, 5, 0, time.UTC),
	}
}

func writeJSONBody(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// requestFields compares recorded requests by method, path and query alone.
var requestFields = cmp.Options{ //nolint:gochecknoglobals // cmp options shared by the tests
	cmp.AllowUnexported(recordedRequest{}),
	cmpopts.IgnoreFields(recordedRequest{}, "contentType", "body"),
	cmpopts.EquateEmpty(),
}

func TestJobList(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string][]job.Job{"jobs": {testJob(job.StatusPartial)}})
	})

	out, err := runJobCmd(t, fake.socket, "list", "--experiment", "exp1", "--kind", "vms.action", "--active")
	if err != nil {
		t.Fatalf("job list --experiment exp1 --kind vms.action --active error = %v", err)
	}

	for _, want := range []string{"j1", "vms.action", "partial", "2/2", "exp1", "alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("job list --experiment exp1 --kind vms.action --active = %q, want it to contain %q", out, want)
		}
	}

	want := []recordedRequest{{
		method: http.MethodGet,
		path:   "/api/v1/jobs",
		query:  url.Values{"experiment": {"exp1"}, "kind": {"vms.action"}, "active": {"true"}},
	}}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("job list --experiment exp1 --kind vms.action --active requests mismatch (-want +got):\n%s", diff)
	}

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err = runJobCmd(t, fake.socket, "list", "-o", format)
		if err != nil {
			t.Fatalf("job list -o %s error = %v", format, err)
		}

		var listed []job.Job
		if err := decodeCommandOutput(format, out, &listed); err != nil {
			t.Fatalf("decoding job list -o %s output %q: %v", format, out, err)
		}

		if diff := cmp.Diff([]job.Job{testJob(job.StatusPartial)}, listed); diff != "" {
			t.Errorf("job list -o %s mismatch (-want +got):\n%s", format, diff)
		}
	}
}

func TestJobGetAndCancel(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs/j1":
			writeJSONBody(w, testJob(job.StatusRunning))
		case "/api/v1/jobs/j1/cancel":
			j := testJob(job.StatusRunning)
			j.CancelRequested = true

			w.WriteHeader(http.StatusAccepted)
			writeJSONBody(w, j)
		default:
			http.Error(w, "job not found", http.StatusNotFound)
		}
	})

	out, err := runJobCmd(t, fake.socket, "get", "j1")
	if err != nil {
		t.Fatalf("job get j1 error = %v", err)
	}

	for _, want := range []string{"Restart 2 VMs", "running", "db", "yes", "vm db is locked with status stopping"} {
		if !strings.Contains(out, want) {
			t.Errorf("job get j1 = %q, want it to contain %q", out, want)
		}
	}

	if out, err := runJobCmd(t, fake.socket, "cancel", "j1"); err != nil || !strings.Contains(out, "Cancel requested for job j1") {
		t.Errorf("job cancel j1 = %q, %v; want %q, nil", out, err, "Cancel requested for job j1")
	}

	want := []recordedRequest{
		{method: http.MethodGet, path: "/api/v1/jobs/j1"},
		{method: http.MethodPost, path: "/api/v1/jobs/j1/cancel"},
	}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("job get j1, then job cancel j1 requests mismatch (-want +got):\n%s", diff)
	}

	_, err = runJobCmd(t, fake.socket, "get", "j2")

	var apiErr *workflowAPIError
	if !errors.As(err, &apiErr) {
		t.Errorf("job get j2 error = %v, want a *workflowAPIError", err)
	} else if apiErr.Status != http.StatusNotFound {
		t.Errorf("job get j2 error status = %d, want %d", apiErr.Status, http.StatusNotFound)
	}
}

// waitForJob asks for the job until it finishes, and returns it as it
// finished; how phenix job wait exits for each end is TestJobOutcome's.
func TestJobWait(t *testing.T) {
	t.Parallel()

	for _, status := range []job.Status{job.StatusSucceeded, job.StatusPartial, job.StatusFailed, job.StatusCanceled} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			var polls atomic.Int32

			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				// running on the first two polls
				if polls.Add(1) <= 2 {
					writeJSONBody(w, testJob(job.StatusRunning))

					return
				}

				writeJSONBody(w, testJob(status))
			})

			j, err := waitForJob(t.Context(), newJobClient(fake.socket), "j1", time.Millisecond)
			if err != nil {
				t.Fatalf("waitForJob(j1) error = %v", err)
			}

			if diff := cmp.Diff(testJob(status), j); diff != "" {
				t.Errorf("waitForJob(j1) mismatch (-want +got):\n%s", diff)
			}

			if n := polls.Load(); n != 3 {
				t.Errorf("waitForJob(j1) polled %d times, want 3: until it finished", n)
			}
		})
	}
}

func TestJobWaitTimesOut(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, testJob(job.StatusRunning))
	})

	_, err := runJobCmd(t, fake.socket, "wait", "j1", "--timeout", "50ms")
	if want := "waiting for job j1 (running): timed out after 50ms"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("job wait j1 --timeout 50ms on a running job error = %v, want one containing %q", err, want)
	}

	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode(job wait j1 --timeout 50ms error) = %d, want 1", got)
	}
}

func TestJobCommandsWithoutServer(t *testing.T) {
	socket := shortSocketPath(t)

	_, err := runJobCmd(t, socket, "list")
	if want := "phenix ui is not running (no socket at " + socket + ")"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("job list without a server error = %v, want one containing %q", err, want)
	}
}

// A job that succeeded ends phenix job wait with no error, a partial job
// with exit code 2, and any other with exit code 1. Each error summarizes
// the items and wraps errJobNotSucceeded.
func TestJobOutcome(t *testing.T) {
	t.Parallel()

	tests := map[job.Status]struct {
		jobErr   string
		wantCode int // 0 for no error
		wantText string
	}{
		job.StatusSucceeded: {},
		job.StatusPartial: {
			wantCode: exitPartial,
			wantText: "job did not succeed: job j1 partial: 1 succeeded, 1 failed, 0 skipped, 0 canceled",
		},
		job.StatusFailed: {
			jobErr:   "disk full",
			wantCode: 1,
			wantText: "job did not succeed: job j1 failed: 1 succeeded, 1 failed, 0 skipped, 0 canceled: disk full",
		},
		job.StatusCanceled: {
			wantCode: 1,
			wantText: "job did not succeed: job j1 canceled: 1 succeeded, 1 failed, 0 skipped, 0 canceled",
		},
		job.StatusRunning: {
			wantCode: 1,
			wantText: "job did not succeed: job j1 running: 1 succeeded, 1 failed, 0 skipped, 0 canceled",
		},
		job.StatusQueued: {
			wantCode: 1,
			wantText: "job did not succeed: job j1 queued: 1 succeeded, 1 failed, 0 skipped, 0 canceled",
		},
	}

	for status, tc := range tests {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			j := testJob(status)
			j.Error = tc.jobErr

			err := jobOutcome(j)
			if tc.wantCode == 0 {
				if err != nil {
					t.Errorf("jobOutcome(%s job) = %v, want nil", status, err)
				}

				return
			}

			if err == nil || err.Error() != tc.wantText {
				t.Fatalf("jobOutcome(%s job) = %v, want %q", status, err, tc.wantText)
			}

			if !errors.Is(err, errJobNotSucceeded) {
				t.Errorf("jobOutcome(%s job) = %v, want it to wrap errJobNotSucceeded", status, err)
			}

			if got := exitCode(err); got != tc.wantCode {
				t.Errorf("exitCode(jobOutcome(%s job)) = %d, want %d", status, got, tc.wantCode)
			}
		})
	}
}

// exitCode is the code an error carries, also when wrapped, and 1 for any
// other error.
func TestJobExitCode(t *testing.T) {
	t.Parallel()

	coded := &exitCodeError{err: errors.New("partial"), code: exitPartial}

	tests := map[string]struct {
		err  error
		want int
	}{
		"plain error":   {err: errors.New("boom"), want: 1},
		"coded":         {err: coded, want: exitPartial},
		"wrapped coded": {err: fmt.Errorf("job wait: %w", coded), want: exitPartial},
		"code 0":        {err: &exitCodeError{err: errors.New("x"), code: 0}, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}

	if coded.Error() != "partial" || !errors.Is(coded, coded.err) {
		t.Errorf("exitCodeError = %q, unwraps to %v; want the message and the error it wraps", coded, errors.Unwrap(coded))
	}
}

// The client escapes job IDs in the path, sends only the filters set, and
// reports an answer that is not JSON and a request it cannot build.
func TestJobClientRequests(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("kind") == "bad" {
			_, _ = w.Write([]byte("not json"))

			return
		}

		if r.URL.Path == "/api/v1/jobs" {
			writeJSONBody(w, map[string][]job.Job{"jobs": nil})

			return
		}

		writeJSONBody(w, testJob(job.StatusRunning))
	})
	client := newJobClient(fake.socket)

	if jobs, err := client.list(t.Context(), job.Filter{}); err != nil || len(jobs) != 0 {
		t.Errorf("list(no filter) = %v, %v; want no jobs", jobs, err)
	}

	if _, err := client.get(t.Context(), "a/b c"); err != nil {
		t.Errorf("get(a/b c) error = %v", err)
	}

	if _, err := client.cancel(t.Context(), "a/b"); err != nil {
		t.Errorf("cancel(a/b) error = %v", err)
	}

	_, err := client.list(t.Context(), job.Filter{Kind: "bad"})
	if err == nil || !strings.Contains(err.Error(), "decoding phenix server answer") {
		t.Errorf("list(kind=bad) error = %v, want a decoding error", err)
	}

	want := []recordedRequest{
		{method: http.MethodGet, path: "/api/v1/jobs"},
		{method: http.MethodGet, path: "/api/v1/jobs/a%2Fb%20c"},
		{method: http.MethodPost, path: "/api/v1/jobs/a%2Fb/cancel"},
		{method: http.MethodGet, path: "/api/v1/jobs", query: url.Values{"kind": {"bad"}}},
	}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}

	var out job.Job
	if err := client.call(t.Context(), "BAD METHOD", "/api/v1/jobs", nil, &out); err == nil ||
		!strings.Contains(err.Error(), "building request") {
		t.Errorf("call(BAD METHOD) error = %v, want a building request error", err)
	}

	if n := len(fake.recorded()); n != len(want) {
		t.Errorf("call(BAD METHOD) sent a request: %d requests, want %d", n, len(want))
	}
}

// waitForJob stops at the first error from the server.
func TestJobWaitServerError(t *testing.T) {
	t.Parallel()

	var polls atomic.Int32

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		polls.Add(1)
		http.Error(w, "job not found", http.StatusNotFound)
	})

	j, err := waitForJob(t.Context(), newJobClient(fake.socket), "j9", time.Millisecond)

	var apiErr *workflowAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("waitForJob(j9) error = %v, want a 404 *workflowAPIError", err)
	}

	if diff := cmp.Diff(job.Job{}, j); diff != "" {
		t.Errorf("waitForJob(j9) job mismatch (-want +got):\n%s", diff)
	}

	if n := polls.Load(); n != 1 {
		t.Errorf("waitForJob(j9) polled %d times, want 1", n)
	}
}

// jobFailWriter is a writer that always fails.
type jobFailWriter struct{}

func (jobFailWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// printJobTo runs printJob for a command whose -o is format, with w as its
// output.
func printJobTo(t *testing.T, w io.Writer, j job.Job, format string) error {
	t.Helper()

	cmd := &cobra.Command{Use: "get"}
	out := addOutputFlag(cmd, formatTable, tableJSONYAML())

	if err := cmd.Flags().Set(outputFlagName, format); err != nil {
		t.Fatalf("-o %s error = %v", format, err)
	}

	cmd.SetOut(w)

	return printJob(cmd, out, j)
}

// printJob writes the job's table, or the job as indented JSON or as YAML
// with the JSON keys. A writer that fails gives an error.
func TestJobPrint(t *testing.T) {
	t.Parallel()

	j := testJob(job.StatusFailed)

	var text bytes.Buffer

	if err := printJobTo(t, &text, j, formatTable); err != nil || !strings.Contains(text.String(), "Restart 2 VMs") ||
		strings.HasPrefix(text.String(), "{") {
		t.Errorf("printJob(table) = %q, %v; want the job's table", text.String(), err)
	}

	for _, tt := range []struct {
		format string
		prefix string // the start of the output
	}{
		{format: FormatJSON, prefix: "{\n  \"id\": "},
		{format: FormatYAML, prefix: "id: j1\n"},
	} {
		var buf bytes.Buffer
		if err := printJobTo(t, &buf, j, tt.format); err != nil || !strings.HasPrefix(buf.String(), tt.prefix) {
			t.Errorf("printJob(%s) = %q, %v; want output that starts with %q", tt.format, buf.String(), err, tt.prefix)
		}

		var decoded job.Job
		if err := decodeCommandOutput(tt.format, buf.String(), &decoded); err != nil {
			t.Fatalf("decoding printJob(%s) output: %v", tt.format, err)
		}

		if diff := cmp.Diff(j, decoded); diff != "" {
			t.Errorf("printJob(%s) mismatch (-want +got):\n%s", tt.format, diff)
		}

		if err := printJobTo(t, jobFailWriter{}, j, tt.format); err == nil || !strings.Contains(err.Error(), "writing") {
			t.Errorf("printJob(%s) to a failing writer error = %v, want a writing error", tt.format, err)
		}
	}
}

// commandContext is the context the command ran with, or the background
// context when it ran without one.
func TestJobCommandContext(t *testing.T) {
	t.Parallel()

	if got := commandContext(&cobra.Command{}); got != context.Background() {
		t.Errorf("commandContext(no context) = %v, want context.Background()", got)
	}

	type key struct{}

	ctx := context.WithValue(t.Context(), key{}, "v")
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	if got := commandContext(cmd); got != ctx {
		t.Errorf("commandContext() = %v, want the command's context", got)
	}
}

// The subcommands print what the server answers, name the job in their
// errors, and phenix job wait exits by how the job ended.
func TestJobCommands(t *testing.T) { //nolint:paralleltest // runJobCmd sets common.UnixSocket
	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs":
			if r.URL.Query().Get("experiment") == "broken" {
				http.Error(w, "store failed", http.StatusInternalServerError)

				return
			}

			writeJSONBody(w, map[string][]job.Job{"jobs": {}})
		case "/api/v1/jobs/ok":
			writeJSONBody(w, testJob(job.StatusSucceeded))
		case "/api/v1/jobs/part":
			writeJSONBody(w, testJob(job.StatusPartial))
		default:
			http.Error(w, "job not found", http.StatusNotFound)
		}
	})

	tests := map[string]struct {
		args     []string
		want     []string // in the output
		wantErr  string   // "" for no error
		wantCode int
	}{
		"help":          {args: nil, want: []string{"Background job management", "list", "cancel", "wait"}},
		"no jobs":       {args: []string{"list"}, want: []string{"No jobs\n"}},
		"no jobs, json": {args: []string{"list", "-o", "json"}, want: []string{"[]\n"}},
		"list fails":    {args: []string{"list", "--experiment", "broken"}, wantErr: "listing jobs: ", wantCode: 1},
		"get, json":     {args: []string{"get", "ok", "-o", "json"}, want: []string{`"status": "succeeded"`}},
		"get, no ID":    {args: []string{"get"}, wantErr: "accepts 1 arg", wantCode: 1},
		"cancel fails":  {args: []string{"cancel", "gone"}, wantErr: "canceling job gone: ", wantCode: 1},
		"wait fails":    {args: []string{"wait", "gone"}, wantErr: "job not found", wantCode: 1},
		"wait, succeeded": {
			args: []string{"wait", "ok", "--timeout", "1m"}, want: []string{"Restart 2 VMs", "succeeded"},
		},
		"wait, partial, json": {
			args: []string{"wait", "part", "-o", "json"}, want: []string{`"status": "partial"`},
			wantErr: "job j1 partial", wantCode: exitPartial,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := runJobCmd(t, fake.socket, tc.args...)

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("job %v = %q, want it to contain %q", tc.args, out, want)
				}
			}

			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("job %v error = %v, want nil", tc.args, err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("job %v error = %v, want one containing %q", tc.args, err, tc.wantErr)
			}

			if got := exitCode(err); got != tc.wantCode {
				t.Errorf("exitCode(job %v error) = %d, want %d", tc.args, got, tc.wantCode)
			}
		})
	}

	t.Run("wait, output fails", func(t *testing.T) {
		original := common.UnixSocket
		common.UnixSocket = fake.socket //nolint:reassign // point the command at the fake server

		t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

		cmd := newJobCmd()
		cmd.SetOut(jobFailWriter{})
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"wait", "ok", "-o", "json"})

		if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "writing JSON") {
			t.Errorf("job wait ok -o json to a failing writer error = %v, want a writing JSON error", err)
		}
	})
}

// The job commands refuse an output format other than table, json and yaml
// before they ask the server.
func TestJobCmdOutputFormat(t *testing.T) { //nolint:paralleltest // replaces common.UnixSocket
	socket := filepath.Join(t.TempDir(), "none.sock")

	for _, args := range [][]string{{"list", "-o", "xml"}, {"get", "j1", "-o", "xml"}, {"wait", "j1", "-o", "csv"}} {
		// pflag does not wrap the error of a flag value, so check its text.
		if _, err := runJobCmd(t, socket, args...); err == nil || !strings.Contains(err.Error(), errOutputFormat.Error()) {
			t.Errorf("job %q error = %v, want %v", args, err, errOutputFormat)
		}
	}
}
