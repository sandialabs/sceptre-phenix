package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"phenix/api/job"
	"phenix/util/polltest"
	"phenix/web/middleware"
)

// useJobs gives the test a job registry of its own, keeping artifacts in a
// temporary folder.
func useJobs(t *testing.T) *job.Registry {
	t.Helper()

	return useJobsIn(t, t.TempDir())
}

// useJobsIn is useJobs with artifacts in dir and further options; its jobs are
// logged and audited as they finish, but not broadcast.
func useJobsIn(t *testing.T, dir string, opts ...job.Option) *job.Registry {
	t.Helper()

	r := job.NewRegistry(append([]job.Option{job.WithArtifactDir(dir), job.WithNotifier(logJobEnd)}, opts...)...)

	original := jobs
	jobs = r

	t.Cleanup(func() {
		r.Shutdown()

		jobs = original
	})

	return r
}

// startTestJob starts owner's job on exp, running until release closes (at once if nil)
// and writing an artifact of content if set. Its one item's result is {"name": "a"}.
func startTestJob(t *testing.T, r *job.Registry, owner, exp string, release chan struct{}, content string) job.Job {
	t.Helper()

	j, err := r.Start(job.Spec{
		Kind: "test", Owner: owner, Experiment: exp, Cancelable: true, Items: []string{"a"},
		Run: func(ctx context.Context, run *job.Run) error {
			if release != nil {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			run.ItemSucceeded("a", map[string]string{"name": "a"})

			if content == "" {
				return nil
			}

			dir, err := run.Dir()
			if err != nil {
				return err
			}

			path := filepath.Join(dir, "out.zip")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return err
			}

			return run.SetArtifact(path, "out.zip", false)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return j
}

// waitJob returns the job once it has finished.
func waitJob(t *testing.T, r *job.Registry, id string) job.Job {
	t.Helper()

	var j job.Job

	polltest.Until(t, "job "+id+" finishes", func() bool {
		var ok bool
		j, ok = r.Get(id)

		return ok && j.Status.Terminal()
	})

	return j
}

// waitAudit returns the audit events once one matches action, outcome and fields (jobs
// audit just after finishing), failing the test if none does.
func waitAudit(t *testing.T, events func() []auditEvent, action, outcome string, fields map[string]any) []auditEvent {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if got := events(); countAudit(got, action, outcome, fields) > 0 {
			return got
		}

		time.Sleep(time.Millisecond)
	}

	got := events()
	wantAudit(t, got, action, outcome, fields)

	return got
}

// listedJobs returns the IDs GET /jobs lists, which leaves out the items'
// results.
func listedJobs(t *testing.T, url string) []string {
	t.Helper()

	resp := do(t, http.MethodGet, url+"/api/v1/jobs", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs: %d", resp.StatusCode)
	}

	var body struct {
		Jobs []job.Job `json:"jobs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	ids := make([]string, 0, len(body.Jobs))
	for _, j := range body.Jobs {
		ids = append(ids, j.ID)

		for _, item := range j.Items {
			if item.Result != nil {
				t.Errorf("GET /jobs: job %s item %s has result %s", j.ID, item.Name, item.Result)
			}
		}
	}

	return ids
}

//nolint:paralleltest // replaces the job registry and store
func TestJobVisibility(t *testing.T) {
	r := useJobs(t)

	var (
		alice = serveAs(t, "alice", testRole("experiments", "get"))
		bob   = serveAs(t, "bob", testRole("experiments", "get"))
		admin = serveAs(t, "admin", combinedRole(
			testRole("jobs", "list", "*"), testRole("jobs", "get", "*"),
			testRole("jobs/global", "list"), testRole("jobs/global", "get"),
		))
		// an Experiment Admin of every experiment, who does not reach jobs of
		// none
		expAdmin = serveAs(t, "exp-admin", combinedRole(testRole("jobs", "list", "*"), testRole("jobs", "get", "*")))
		// an Experiment Admin of exp1
		scoped = serveAs(t, "scoped", combinedRole(testRole("jobs", "list", "exp1"), testRole("jobs", "get", "exp1")))

		exp1   = startTestJob(t, r, "alice", "exp1", nil, "")
		exp2   = startTestJob(t, r, "alice", "exp2", nil, "")
		noExp  = startTestJob(t, r, "alice", "", nil, "")
		bobs   = startTestJob(t, r, "bob", "exp1", nil, "")
		allIDs = []string{bobs.ID, noExp.ID, exp2.ID, exp1.ID}
	)

	for _, id := range allIDs {
		waitJob(t, r, id)
	}

	for name, tc := range map[string]struct {
		url  string
		want []string
	}{
		"owner":                  {alice.URL, []string{noExp.ID, exp2.ID, exp1.ID}},
		"another user":           {bob.URL, []string{bobs.ID}},
		"jobs admin":             {admin.URL, allIDs},
		"experiments jobs admin": {expAdmin.URL, []string{bobs.ID, exp2.ID, exp1.ID}},
		"experiment jobs admin":  {scoped.URL, []string{bobs.ID, exp1.ID}},
	} {
		got := listedJobs(t, tc.url)
		if len(got) != len(tc.want) {
			t.Errorf("%s: listed %v, want %v", name, got, tc.want)

			continue
		}

		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: listed %v, want %v", name, got, tc.want)

				break
			}
		}
	}

	for name, tc := range map[string]struct {
		url, id string
		want    int
	}{
		"owner":                            {alice.URL, exp1.ID, http.StatusOK},
		"another user":                     {bob.URL, exp1.ID, http.StatusNotFound},
		"jobs admin":                       {admin.URL, noExp.ID, http.StatusOK},
		"experiment admin, its own":        {scoped.URL, exp1.ID, http.StatusOK},
		"experiment admin, another":        {scoped.URL, exp2.ID, http.StatusNotFound},
		"experiment admin, no exp":         {scoped.URL, noExp.ID, http.StatusNotFound},
		"every experiment's admin, no exp": {expAdmin.URL, noExp.ID, http.StatusNotFound},
		"unknown job":                      {admin.URL, "j0000000000000000", http.StatusNotFound},
		"another user's artifact":          {bob.URL, exp1.ID + "/artifact", http.StatusNotFound},
	} {
		url := tc.url + "/api/v1/jobs/" + tc.id

		if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != tc.want {
			t.Errorf("%s: GET %s = %d, want %d", name, url, resp.StatusCode, tc.want)
		}
	}

	// one job, unlike the list, has its items' results
	resp := do(t, http.MethodGet, alice.URL+"/api/v1/jobs/"+exp1.ID, "")

	var got job.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Items) != 1 || string(got.Items[0].Result) != `{"name":"a"}` {
		t.Errorf("GET /jobs/%s items = %+v, want a's result", exp1.ID, got.Items)
	}
}

//nolint:paralleltest // replaces the job registry and store
func TestCancelAndDismissJobs(t *testing.T) {
	r := useJobs(t)

	var (
		alice   = serveAs(t, "alice", testRole("experiments", "get"))
		bob     = serveAs(t, "bob", testRole("experiments", "get"))
		admin   = serveAs(t, "admin", combinedRole(testRole("jobs", "update", "*"), testRole("jobs", "delete", "*")))
		release = make(chan struct{})
		running = startTestJob(t, r, "alice", "exp1", release, "")
	)

	t.Cleanup(func() { close(release) })

	cancelURL := "/api/v1/jobs/" + running.ID + "/cancel"
	jobURL := "/api/v1/jobs/" + running.ID

	if resp := do(t, http.MethodPost, bob.URL+cancelURL, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another user's cancel: %d, want 404", resp.StatusCode)
	}

	if resp := do(t, http.MethodDelete, alice.URL+jobURL, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("dismissing a running job: %d, want 409", resp.StatusCode)
	}

	resp := do(t, http.MethodPost, alice.URL+cancelURL, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("owner's cancel: %d, want 202", resp.StatusCode)
	}

	var canceling job.Job
	if err := json.NewDecoder(resp.Body).Decode(&canceling); err != nil || !canceling.CancelRequested {
		t.Errorf("cancel answered %+v (%v), want the job with its cancel requested", canceling, err)
	}

	if j := waitJob(t, r, running.ID); j.Status != job.StatusCanceled {
		t.Errorf("canceled job is %s", j.Status)
	}

	if resp := do(t, http.MethodPost, admin.URL+cancelURL, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("canceling a finished job: %d, want 409", resp.StatusCode)
	}

	if resp := do(t, http.MethodDelete, bob.URL+jobURL, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another user's dismissal: %d, want 404", resp.StatusCode)
	}

	if resp := do(t, http.MethodDelete, admin.URL+jobURL, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("jobs admin's dismissal: %d, want 204", resp.StatusCode)
	}

	if _, ok := r.Get(running.ID); ok {
		t.Error("dismissed job is still held")
	}

	// another user's job of no experiment, such as a disk clone, needs
	// jobs/global, not jobs on every experiment
	noExp := "/api/v1/jobs/" + startTestJob(t, r, "alice", "", release, "").ID + "/cancel"

	if resp := do(t, http.MethodPost, admin.URL+noExp, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("every experiment's jobs admin canceling a job of none: %d, want 404", resp.StatusCode)
	}

	global := serveAs(t, "global", testRole("jobs/global", "update"))
	if resp := do(t, http.MethodPost, global.URL+noExp, ""); resp.StatusCode != http.StatusAccepted {
		t.Errorf("jobs/global admin canceling a job of none: %d, want 202", resp.StatusCode)
	}

	// a job that cannot stop
	stuck, err := r.Start(job.Spec{Kind: "test", Owner: "alice", Run: func(ctx context.Context, _ *job.Run) error {
		<-ctx.Done()

		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	resp = do(t, http.MethodPost, alice.URL+"/api/v1/jobs/"+stuck.ID+"/cancel", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusConflict || body != "this job cannot be canceled\n" {
		t.Errorf("canceling a job that cannot stop: %d %q, want 409", resp.StatusCode, body)
	}
}

//nolint:paralleltest // replaces the job registry and store
func TestJobArtifact(t *testing.T) {
	r := useJobs(t)

	var (
		alice   = serveAs(t, "alice", testRole("experiments", "get"))
		zipped  = waitJob(t, r, startTestJob(t, r, "alice", "exp1", nil, "0123456789abcdef").ID)
		without = waitJob(t, r, startTestJob(t, r, "alice", "exp1", nil, "").ID)
	)

	resp := do(t, http.MethodGet, alice.URL+"/api/v1/jobs/"+zipped.ID+"/artifact", "", "Range", "bytes=0-9")
	if body := readBody(t, resp); resp.StatusCode != http.StatusPartialContent || body != "0123456789" {
		t.Errorf("ranged artifact GET: %d %q, want 206 with the first ten bytes", resp.StatusCode, body)
	}

	if cd := resp.Header.Get("Content-Disposition"); cd != "attachment; filename=out.zip" {
		t.Errorf("Content-Disposition %q", cd)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type %q, want application/zip", ct)
	}

	if resp := do(t, http.MethodGet, alice.URL+"/api/v1/jobs/"+without.ID+"/artifact", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("job without an artifact: %d, want 404", resp.StatusCode)
	}

	if resp := do(t, http.MethodDelete, alice.URL+"/api/v1/jobs/"+zipped.ID, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dismissing: %d", resp.StatusCode)
	}

	if _, err := os.Stat(zipped.Artifact.Path); !os.IsNotExist(err) {
		t.Errorf("dismissed job's artifact remains: %v", err)
	}
}

// A job refused because too many are waiting answers 429 with Retry-After.
//
//nolint:paralleltest // replaces the job registry
func TestStartJobAnswersQueueFull(t *testing.T) {
	r := useJobsIn(t, t.TempDir(), job.WithLimits(1, 1), job.WithQueueLimits(0, 0))
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	startTestJob(t, r, "alice", "", release, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, "alice"))
	rec := httptest.NewRecorder()

	startJob(rec, req, job.Spec{Kind: "test", Run: func(context.Context, *job.Run) error { return nil }})

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("status %d, Retry-After %q, want 429 with one", rec.Code, rec.Header().Get("Retry-After"))
	}
}
