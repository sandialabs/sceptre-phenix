package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

	out, err = runJobCmd(t, fake.socket, "list", "-o", "json")
	if err != nil {
		t.Fatalf("job list -o json error = %v", err)
	}

	var listed []job.Job
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("json.Unmarshal(job list -o json output %q) error = %v", out, err)
	}

	if diff := cmp.Diff([]job.Job{testJob(job.StatusPartial)}, listed); diff != "" {
		t.Errorf("job list -o json mismatch (-want +got):\n%s", diff)
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

// phenix job wait polls until the job finishes, and exits 0, 2 or 1 by how
// it ended.
func TestJobWait(t *testing.T) {
	//nolint:exhaustive // only the statuses a job ends in
	for status, want := range map[job.Status]int{
		job.StatusSucceeded: 0,
		job.StatusPartial:   exitPartial,
		job.StatusFailed:    1,
		job.StatusCanceled:  1,
	} {
		t.Run(string(status), func(t *testing.T) {
			var polls atomic.Int32

			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				// running on the first poll
				if polls.Add(1) == 1 {
					writeJSONBody(w, testJob(job.StatusRunning))

					return
				}

				writeJSONBody(w, testJob(status))
			})

			original := common.UnixSocket
			common.UnixSocket = fake.socket //nolint:reassign // point the command at the fake server

			t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

			j, err := waitForJob(t.Context(), newJobClient(fake.socket), "j1", time.Millisecond)
			if err != nil {
				t.Fatalf("waitForJob(j1) error = %v", err)
			}

			err = jobOutcome(j)

			got := 0
			if err != nil {
				got = exitCode(err)
			}

			if got != want {
				t.Errorf("exitCode(jobOutcome(%s job)) = %d (error %v), want %d", status, got, err, want)
			}

			if n := polls.Load(); n < 2 {
				t.Errorf("waitForJob(j1) polled %d times, want at least 2: until it finished", n)
			}
		})
	}
}

func TestJobWaitTimesOut(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, testJob(job.StatusRunning))
	})

	_, err := runJobCmd(t, fake.socket, "wait", "j1", "--timeout", "50ms")
	if err == nil || !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Errorf("job wait j1 --timeout 50ms on a running job error = %v, want one containing %q", err, "timed out after 50ms")
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
