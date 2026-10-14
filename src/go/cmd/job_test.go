package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestJobList(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string][]job.Job{"jobs": {testJob(job.StatusPartial)}})
	})

	out, err := runJobCmd(t, fake.socket, "list", "--experiment", "exp1", "--kind", "vms.action", "--active")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"j1", "vms.action", "partial", "2/2", "exp1", "alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}

	reqs := fake.recorded()
	if len(reqs) != 1 || reqs[0].path != "/api/v1/jobs" ||
		reqs[0].query.Get("experiment") != "exp1" || reqs[0].query.Get("kind") != "vms.action" ||
		reqs[0].query.Get("active") != "true" {
		t.Errorf("requests %+v, want one filtered GET /api/v1/jobs", reqs)
	}

	out, err = runJobCmd(t, fake.socket, "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}

	var listed []job.Job
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 1 || listed[0].ID != "j1" {
		t.Errorf("JSON output %q (%v), want the one job", out, err)
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
		t.Fatal(err)
	}

	for _, want := range []string{"Restart 2 VMs", "running", "db", "yes", "vm db is locked with status stopping"} {
		if !strings.Contains(out, want) {
			t.Errorf("job output lacks %q:\n%s", want, out)
		}
	}

	if out, err := runJobCmd(t, fake.socket, "cancel", "j1"); err != nil || !strings.Contains(out, "Cancel requested for job j1") {
		t.Errorf("cancel: %q, %v", out, err)
	}

	if last := fake.recorded()[len(fake.recorded())-1]; last.method != http.MethodPost {
		t.Errorf("cancel sent %s, want POST", last.method)
	}

	_, err = runJobCmd(t, fake.socket, "get", "j2")

	var apiErr *workflowAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("unknown job: %v, want a 404", err)
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
				t.Fatal(err)
			}

			err = jobOutcome(j)

			got := 0
			if err != nil {
				got = exitCode(err)
			}

			if got != want {
				t.Errorf("exit code %d (%v), want %d", got, err, want)
			}

			if polls.Load() < 2 {
				t.Errorf("polled %d times, want until it finished", polls.Load())
			}
		})
	}
}

func TestJobWaitTimesOut(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, testJob(job.StatusRunning))
	})

	_, err := runJobCmd(t, fake.socket, "wait", "j1", "--timeout", "50ms")
	if err == nil || !strings.Contains(err.Error(), "timed out after 50ms") || exitCode(err) != 1 {
		t.Errorf("wait on a job that never ends: %v, want a timeout with exit code 1", err)
	}
}

func TestJobCommandsWithoutServer(t *testing.T) {
	socket := shortSocketPath(t)

	_, err := runJobCmd(t, socket, "list")
	if err == nil || !strings.Contains(err.Error(), "phenix ui is not running (no socket at "+socket+")") {
		t.Errorf("list without a server: %v", err)
	}
}
