package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/job"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/polltest"
	"phenix/web/broker"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
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
		r.ShutdownContext(context.Background(), 0)

		jobs = original
	})

	return r
}

// startTestJob starts owner's job on exp, running until release closes (at once if nil)
// and writing an artifact of content if set. Its one item's result is {"name": "a"}.
func startTestJob(t *testing.T, r *job.Registry, owner, exp string, release chan struct{}, content string) job.Job {
	t.Helper()

	j, err := r.Start(job.Spec{
		Kind: vmActionKind, Owner: owner, Experiment: exp, Cancelable: true, Items: []string{"a"},
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
		t.Fatalf("Registry.Start(%s's test job on %q) error = %v", owner, exp, err)
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
		t.Fatalf("GET /api/v1/jobs = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Jobs []job.Job `json:"jobs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding GET /api/v1/jobs: %v", err)
	}

	ids := make([]string, 0, len(body.Jobs))
	for _, j := range body.Jobs {
		ids = append(ids, j.ID)

		for _, item := range j.Items {
			if item.Result != nil {
				t.Errorf("GET /api/v1/jobs job %s item %s result = %s, want none", j.ID, item.Name, item.Result)
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
		t.Run("list as "+name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, listedJobs(t, tc.url)); diff != "" {
				t.Errorf("GET /api/v1/jobs as %s mismatch (-want +got):\n%s", name, diff)
			}
		})
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
		path := "/api/v1/jobs/" + tc.id

		if resp := do(t, http.MethodGet, tc.url+path, ""); resp.StatusCode != tc.want {
			t.Errorf("GET %s as %s = %d, want %d", path, name, resp.StatusCode, tc.want)
		}
	}

	// one job, unlike the list, has its items' results
	resp := do(t, http.MethodGet, alice.URL+"/api/v1/jobs/"+exp1.ID, "")

	var got job.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET /api/v1/jobs/%s: %v", exp1.ID, err)
	}

	if len(got.Items) != 1 {
		t.Fatalf("GET /api/v1/jobs/%s items = %+v, want one", exp1.ID, got.Items)
	}

	var result map[string]string
	if err := json.Unmarshal(got.Items[0].Result, &result); err != nil {
		t.Fatalf("GET /api/v1/jobs/%s item result = %s, not an object: %v", exp1.ID, got.Items[0].Result, err)
	}

	if diff := cmp.Diff(map[string]string{"name": "a"}, result); diff != "" {
		t.Errorf("GET /api/v1/jobs/%s item result mismatch (-want +got):\n%s", exp1.ID, diff)
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
		t.Errorf("POST %s as another user = %d, want %d", cancelURL, resp.StatusCode, http.StatusNotFound)
	}

	if resp := do(t, http.MethodDelete, alice.URL+jobURL, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("DELETE %s while running = %d, want %d", jobURL, resp.StatusCode, http.StatusConflict)
	}

	resp := do(t, http.MethodPost, alice.URL+cancelURL, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s as the owner = %d, want %d", cancelURL, resp.StatusCode, http.StatusAccepted)
	}

	var canceling job.Job
	if err := json.NewDecoder(resp.Body).Decode(&canceling); err != nil {
		t.Errorf("decoding POST %s: %v", cancelURL, err)
	} else if !canceling.CancelRequested {
		t.Errorf("POST %s cancelRequested = false, want true", cancelURL)
	}

	if j := waitJob(t, r, running.ID); j.Status != job.StatusCanceled {
		t.Errorf("job %s after POST %s status = %s, want %s", running.ID, cancelURL, j.Status, job.StatusCanceled)
	}

	if resp := do(t, http.MethodPost, admin.URL+cancelURL, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("POST %s after the job finished = %d, want %d", cancelURL, resp.StatusCode, http.StatusConflict)
	}

	if resp := do(t, http.MethodDelete, bob.URL+jobURL, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE %s as another user = %d, want %d", jobURL, resp.StatusCode, http.StatusNotFound)
	}

	if resp := do(t, http.MethodDelete, admin.URL+jobURL, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE %s as a jobs admin = %d, want %d", jobURL, resp.StatusCode, http.StatusNoContent)
	}

	if _, ok := r.Get(running.ID); ok {
		t.Errorf("Registry.Get(%s) after DELETE %s found the job, want none", running.ID, jobURL)
	}

	// another user's job of no experiment, such as a disk clone, needs
	// jobs/global, not jobs on every experiment
	noExp := "/api/v1/jobs/" + startTestJob(t, r, "alice", "", release, "").ID + "/cancel"

	if resp := do(t, http.MethodPost, admin.URL+noExp, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST %s as every experiment's jobs admin = %d, want %d", noExp, resp.StatusCode, http.StatusNotFound)
	}

	global := serveAs(t, "global", testRole("jobs/global", "update"))
	if resp := do(t, http.MethodPost, global.URL+noExp, ""); resp.StatusCode != http.StatusAccepted {
		t.Errorf("POST %s as a jobs/global admin = %d, want %d", noExp, resp.StatusCode, http.StatusAccepted)
	}

	// a job that cannot stop
	stuck, err := r.Start(job.Spec{Kind: "test", Owner: "alice", Run: func(ctx context.Context, _ *job.Run) error {
		<-ctx.Done()

		return nil
	}})
	if err != nil {
		t.Fatalf("Registry.Start(a job that cannot be canceled) error = %v", err)
	}

	stuckURL := "/api/v1/jobs/" + stuck.ID + "/cancel"
	resp = do(t, http.MethodPost, alice.URL+stuckURL, "")

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("POST %s of a job that cannot stop = %d, want %d", stuckURL, resp.StatusCode, http.StatusConflict)
	}

	if got, want := errorMessage(t, resp.StatusCode, readBody(t, resp)), job.ErrNotCancelable.Error(); got != want {
		t.Errorf("POST %s of a job that cannot stop = %q, want %q", stuckURL, got, want)
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

	if zipped.Artifact == nil {
		t.Fatalf("job %s finished without an artifact, want out.zip", zipped.ID)
	}

	artifact := "/api/v1/jobs/" + zipped.ID + "/artifact"
	resp := do(t, http.MethodGet, alice.URL+artifact, "", "Range", "bytes=0-9")

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("GET %s with Range bytes=0-9 = %d, want %d", artifact, resp.StatusCode, http.StatusPartialContent)
	}

	if got, want := readBody(t, resp), "0123456789"; got != want {
		t.Errorf("GET %s with Range bytes=0-9 = %q, want %q", artifact, got, want)
	}

	if got, want := resp.Header.Get("Content-Disposition"), "attachment; filename=out.zip"; got != want {
		t.Errorf("GET %s Content-Disposition = %q, want %q", artifact, got, want)
	}

	if got, want := resp.Header.Get("Content-Type"), "application/zip"; got != want {
		t.Errorf("GET %s Content-Type = %q, want %q", artifact, got, want)
	}

	noArtifact := "/api/v1/jobs/" + without.ID + "/artifact"
	if resp := do(t, http.MethodGet, alice.URL+noArtifact, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s of a job without an artifact = %d, want %d", noArtifact, resp.StatusCode, http.StatusNotFound)
	}

	if resp := do(t, http.MethodDelete, alice.URL+"/api/v1/jobs/"+zipped.ID, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /api/v1/jobs/%s = %d, want %d", zipped.ID, resp.StatusCode, http.StatusNoContent)
	}

	if _, err := os.Stat(zipped.Artifact.Path); !os.IsNotExist(err) {
		t.Errorf("os.Stat(artifact) after DELETE /api/v1/jobs/%s error = %v, want it not to exist", zipped.ID, err)
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

	serveStartJob(rec, req, job.Spec{Kind: "test", Run: func(context.Context, *job.Run) error { return nil }})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("startJob() with a full queue = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Error("startJob() with a full queue has no Retry-After header, want one")
	}

	if got := errorMessage(t, rec.Code, rec.Body.String()); got != job.ErrQueueFull.Error() {
		t.Errorf("startJob() with a full queue message = %q, want %q", got, job.ErrQueueFull.Error())
	}
}

// Each job kind has the hooks its routes rely on: an archive is audited, checked and logged as it is downloaded,
// a clone or bulk VM action is audited as it finishes, and any other kind has none.
func TestJobKindOf(t *testing.T) {
	type hooks struct{ served, allowed, finished bool } //nolint:unused // cmp.Diff compares every field

	got := map[string]hooks{}

	for _, kind := range []string{archiveKind, cloneKind, vmActionKind, "unknown"} {
		k := jobKindOf(kind)
		got[kind] = hooks{served: k.artifactServed != nil, allowed: k.artifactAllowed != nil, finished: k.finished != nil}
	}

	want := map[string]hooks{
		archiveKind:  {served: true, allowed: true, finished: true},
		cloneKind:    {served: false, allowed: false, finished: true},
		vmActionKind: {served: false, allowed: false, finished: true},
		"unknown":    {served: false, allowed: false, finished: false},
	}

	if diff := cmp.Diff(want, got, cmp.AllowUnexported(hooks{})); diff != "" {
		t.Errorf("job kind hooks mismatch (-want +got):\n%s", diff)
	}
}

// jobsBrokerClient runs the broker until the test ends and connects a websocket client of alice with role. It
// returns what lists, as "action name", the messages the client has received about resources of the type.
func jobsBrokerClient(t *testing.T, role rbac.Role) func(resource string) []string {
	t.Helper()

	lifecycle.Start()

	background, stopBroker := context.WithCancel(context.Background())
	brokerStopped := make(chan struct{})

	go func() {
		defer close(brokerStopped)

		broker.Start(background)
	}()

	t.Cleanup(func() {
		stopBroker()
		<-brokerStopped
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, role)
		broker.ServeWS(w, r.WithContext(context.WithValue(ctx, middleware.ContextKeyUser, "alice")))
	}))
	t.Cleanup(server.Close)

	// an earlier test's client leaves when this broker reads its unregister, which would hide the new client
	for deadline := time.Now().Add(5 * time.Second); broker.CurrentStats().Clients > 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}

	before := broker.CurrentStats().Clients

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the broker: %v", err)
	}

	resp.Body.Close()

	type resource struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Action string `json:"action"`
	}

	var (
		mu       sync.Mutex
		messages []resource
		reading  = make(chan struct{})
	)

	go func() {
		defer close(reading)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			for line := range strings.SplitSeq(string(data), "\n") {
				var msg struct {
					Resource resource `json:"resource"`
				}

				if json.Unmarshal([]byte(line), &msg) == nil {
					mu.Lock()
					messages = append(messages, msg.Resource)
					mu.Unlock()
				}
			}
		}
	}()

	t.Cleanup(func() {
		conn.Close()
		<-reading

		// a client the broker still holds would be forgotten by the next test's broker
		for deadline := time.Now().Add(5 * time.Second); broker.CurrentStats().Clients > before; {
			if time.Now().After(deadline) {
				break
			}

			time.Sleep(time.Millisecond)
		}
	})

	polltest.Until(t, "the broker has the client", func() bool { return broker.CurrentStats().Clients > before })

	return func(resource string) []string {
		mu.Lock()
		defer mu.Unlock()

		var got []string

		for _, m := range messages {
			if m.Type == resource {
				got = append(got, m.Action+" "+m.Name)
			}
		}

		return got
	}
}

// ConfigureJobs keeps artifacts in <PhenixBase>/jobs, first removing the job folders a previous run left there and
// keeping anything else, and tells websocket clients of each change to a job.
//
//nolint:paralleltest // replaces the job registry, PhenixBase and the logs
func TestConfigureJobs(t *testing.T) {
	const stale = "j0123456789abcdef"

	tests := map[string]struct {
		setup     func(t *testing.T, dir string)
		wantLeft  []string
		wantLevel string // the level ConfigureJobs logs at, if it logs
		wantMsg   string
	}{
		"stale job folders": {
			setup: func(t *testing.T, dir string) {
				t.Helper()

				writeDisks(t, filepath.Join(dir, stale, "out.zip"), filepath.Join(dir, "keep", "a"))
			},
			wantLeft:  []string{"keep"},
			wantLevel: "INFO",
			wantMsg:   "removed stale job artifacts",
		},
		"no folder": {setup: func(*testing.T, string) {}},
		"a file in its place": {
			setup: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatalf("writing %s: %v", dir, err)
				}
			},
			wantLevel: "WARN",
			wantMsg:   "removing stale job artifacts",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			original := common.PhenixBase
			common.PhenixBase = base //nolint:reassign // the artifacts' parent

			t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

			dir := filepath.Join(base, "jobs")
			tc.setup(t, dir)

			r := useJobs(t)
			logs := captureLogs(t)

			ConfigureJobs()

			got := logs()
			for _, level := range []string{"INFO", "WARN"} {
				for _, msg := range got[level] {
					if strings.Contains(msg, "stale job artifacts") && (level != tc.wantLevel || msg != tc.wantMsg) {
						t.Errorf("ConfigureJobs() logged %q at %s, want only %q at %q", msg, level, tc.wantMsg, tc.wantLevel)
					}
				}
			}

			if tc.wantMsg != "" {
				wantLogged(t, got, tc.wantLevel, tc.wantMsg)
			}

			if tc.wantLevel == "WARN" {
				return
			}

			var left []string

			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				left = append(left, e.Name())
			}

			if diff := cmp.Diff(tc.wantLeft, left); diff != "" {
				t.Errorf("ConfigureJobs() left in %s mismatch (-want +got):\n%s", dir, diff)
			}

			messages := jobsBrokerClient(t, testRole("experiments", "get"))

			j := waitJob(t, r, startTestJob(t, r, "alice", "", nil, "zipped").ID)
			if j.Artifact == nil || filepath.Dir(filepath.Dir(j.Artifact.Path)) != dir {
				t.Errorf("job artifact after ConfigureJobs() = %+v, want one in %s", j.Artifact, dir)
			}

			polltest.Until(t, "the job's owner is told it finished", func() bool {
				return slices.Contains(messages("job"), "update "+j.ID)
			})
		})
	}
}

// notifyJob tells websocket clients of a change to a job, and logs the job's end once: when it has finished, not
// while it runs or when it is removed.
//
//nolint:paralleltest // runs the broker and swaps the logs
func TestNotifyJob(t *testing.T) {
	var (
		started  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		finished = started.Add(time.Minute)
	)

	tests := map[string]struct {
		j          job.Job
		removed    bool
		wantAction string
		wantLogged bool
	}{
		"running": {
			j:          job.Job{ID: "j0000000000000001", Kind: "test", Owner: "alice", Status: job.StatusRunning},
			wantAction: "update",
		},
		"finished": {
			j: job.Job{
				ID: "j0000000000000002", Kind: "test", Owner: "alice", Status: job.StatusSucceeded,
				Started: &started, Finished: &finished,
			},
			wantAction: "update",
			wantLogged: true,
		},
		"canceled while queued": {
			j:          job.Job{ID: "j0000000000000003", Kind: "test", Owner: "alice", Status: job.StatusCanceled},
			wantAction: "update",
			wantLogged: true,
		},
		"removed": {
			j:          job.Job{ID: "j0000000000000004", Kind: "test", Owner: "alice", Status: job.StatusSucceeded},
			removed:    true,
			wantAction: "remove",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			messages := jobsBrokerClient(t, testRole("experiments", "get"))
			records := captureRecords(t)

			notifyJob(tc.j, tc.removed)

			want := tc.wantAction + " " + tc.j.ID
			polltest.Until(t, "the owner is told of the job", func() bool { return slices.Contains(messages("job"), want) })

			var logged []string

			for _, line := range records() {
				if strings.Contains(line, `"msg":"job finished"`) && strings.Contains(line, tc.j.ID) {
					logged = append(logged, line)
				}
			}

			if tc.wantLogged != (len(logged) == 1) || len(logged) > 1 {
				t.Errorf("notifyJob(%s job, removed %t) logged %q, want the job's end logged %t",
					tc.j.Status, tc.removed, logged, tc.wantLogged)
			}

			if tc.j.Finished != nil && len(logged) == 1 && !strings.Contains(logged[0], `"duration":"1m0s"`) {
				t.Errorf("notifyJob(%s job) logged %s, want its 1m0s duration", tc.j.Status, logged[0])
			}
		})
	}
}

// jobError is why a finished job failed: canceled, its first failed item's error, its own error, or its status.
func TestJobError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		j    job.Job
		want string
	}{
		"canceled": {
			j:    job.Job{Status: job.StatusCanceled, Error: "ignored", Items: []job.Item{{Status: job.ItemFailed, Error: "x"}}},
			want: "canceled",
		},
		"first failed item": {
			j: job.Job{Status: job.StatusPartial, Error: "job error", Items: []job.Item{
				{Name: "a", Status: job.ItemSucceeded},
				{Name: "b", Status: job.ItemFailed},
				{Name: "c", Status: job.ItemFailed, Error: "c failed"},
				{Name: "d", Status: job.ItemFailed, Error: "d failed"},
			}},
			want: "c failed",
		},
		"the job's own error": {
			j:    job.Job{Status: job.StatusFailed, Error: "no space left", Items: []job.Item{{Status: job.ItemFailed}}},
			want: "no space left",
		},
		"no error given": {j: job.Job{Status: job.StatusFailed}, want: "job failed"},
		"no items":       {j: job.Job{Status: job.StatusPartial, Items: []job.Item{}}, want: "job partial"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := jobError(tc.j); got == nil || got.Error() != tc.want {
				t.Errorf("jobError(%+v) = %v, want %q", tc.j, got, tc.want)
			}
		})
	}
}

// Each check of another user's job needs the verb on the job's experiment, or jobs/global for a job of none.
func TestJobAccess(t *testing.T) {
	t.Parallel()

	var (
		ofExp  = job.Job{Experiment: "exp1"}
		ofNone = job.Job{}
	)

	checks := map[string]struct {
		may  jobAccess
		verb string
	}{
		"list": {mayListJob, "list"}, "get": {mayGetJob, "get"},
		"cancel": {mayCancelJob, "update"}, "remove": {mayRemoveJob, "delete"},
	}

	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tests := map[string]struct {
				role rbac.Role
				j    job.Job
				want bool
			}{
				"the experiment's":                 {testRole("jobs", check.verb, "exp1"), ofExp, true},
				"another experiment's":             {testRole("jobs", check.verb, "exp2"), ofExp, false},
				"every experiment's, a job of one": {testRole("jobs", check.verb, "*"), ofExp, true},
				"every experiment's, a job of none": {
					testRole("jobs", check.verb, "*"), ofNone, false,
				},
				"global, a job of none":         {testRole("jobs/global", check.verb), ofNone, true},
				"global, a job of one":          {testRole("jobs/global", check.verb), ofExp, false},
				"another verb, a job of none":   {testRole("jobs/global", "nothing"), ofNone, false},
				"another verb, a job of one":    {testRole("jobs", "nothing", "*"), ofExp, false},
				"no permissions, a job of none": {rbac.Role{Spec: &v1.RoleSpec{}}, ofNone, false},
			}

			for role, tc := range tests {
				if got := check.may(tc.role, tc.j); got != tc.want {
					t.Errorf("may %s a job of %q as %s = %t, want %t", name, tc.j.Experiment, role, got, tc.want)
				}
			}
		})
	}
}

// startJob refuses a job that cannot start with a 500 and its error, and adds nothing.
//
//nolint:paralleltest // replaces the job registry
func TestStartJobFails(t *testing.T) {
	r := useJobs(t)

	rec := httptest.NewRecorder()
	serveStartJob(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/jobs", nil), job.Spec{Kind: "test"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("startJob() of a job without work = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	if got, want := errorMessage(t, rec.Code, rec.Body.String()), "starting job: no work given"; got != want {
		t.Errorf("startJob() of a job without work message = %q, want %q", got, want)
	}

	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("startJob() of a job without work Location = %q, want none", got)
	}

	if n := len(r.List(job.Filter{})); n != 0 {
		t.Errorf("jobs after a refused start = %d, want none", n)
	}
}

// writeJob answers with the job as JSON and the status, or, for a job it cannot encode, returns a 500 and writes
// nothing.
func TestWriteJob(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		j          job.Job
		wantErr    bool
		wantStatus int
	}{
		"a job":                   {j: job.Job{ID: "j1", Status: job.StatusRunning}, wantStatus: http.StatusAccepted},
		"an item result not JSON": {j: job.Job{ID: "j2", Items: []job.Item{{Name: "a", Result: json.RawMessage("{")}}}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			err := writeJob(rec, http.StatusAccepted, tc.j)

			if tc.wantErr {
				var webErr *weberror.WebError
				if !errors.As(err, &webErr) || webErr.Status != http.StatusInternalServerError {
					t.Errorf("writeJob(%s) error = %v, want a 500", name, err)
				}

				if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
					t.Errorf("writeJob(%s) wrote %q with headers %v, want nothing", name, rec.Body, rec.Header())
				}

				return
			}

			if err != nil {
				t.Fatalf("writeJob(%s) error = %v", name, err)
			}

			var got job.Job
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("writeJob(%s) wrote %q, not a job: %v", name, rec.Body, err)
			}

			if rec.Code != tc.wantStatus || rec.Header().Get("Content-Type") != "application/json" || got.ID != tc.j.ID {
				t.Errorf("writeJob(%s) = %d %q job %s, want %d application/json job %s",
					name, rec.Code, rec.Header().Get("Content-Type"), got.ID, tc.wantStatus, tc.j.ID)
			}
		})
	}
}

// countOf names n of a noun, plural unless n is 1.
func TestCountOf(t *testing.T) {
	t.Parallel()

	for n, want := range map[int]string{0: "0 VMs", 1: "1 VM", 2: "2 VMs", 10: "10 VMs"} {
		if got := countOf(n, "VM"); got != want {
			t.Errorf("countOf(%d, VM) = %q, want %q", n, got, want)
		}
	}
}

// GET /jobs filters by experiment, kind and activity, and pages after counting.
//
//nolint:paralleltest // replaces the job registry and store
func TestGetJobsFilters(t *testing.T) {
	r := useJobs(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	var (
		done    = waitJob(t, r, startTestJob(t, r, "alice", "exp1", nil, "").ID)
		other   = waitJob(t, r, startTestJob(t, r, "alice", "exp2", nil, "").ID)
		running = startTestJob(t, r, "alice", "exp2", release, "")
		server  = serveAs(t, "alice", testRole("experiments", "get")).URL
	)

	tests := map[string]struct {
		query     string
		wantIDs   []string
		wantTotal int
		wantCode  int
	}{
		"all":            {"", []string{running.ID, other.ID, done.ID}, 3, http.StatusOK},
		"experiment":     {"?experiment=exp2", []string{running.ID, other.ID}, 2, http.StatusOK},
		"active":         {"?active=true", []string{running.ID}, 1, http.StatusOK},
		"not a boolean":  {"?active=maybe", []string{running.ID, other.ID, done.ID}, 3, http.StatusOK},
		"kind":           {"?kind=" + vmActionKind, []string{running.ID, other.ID, done.ID}, 3, http.StatusOK},
		"another kind":   {"?kind=other", []string{}, 0, http.StatusOK},
		"page 2 of 2":    {"?pageNum=2&perPage=2", []string{done.ID}, 3, http.StatusOK},
		"past the pages": {"?pageNum=3&perPage=2", []string{}, 3, http.StatusOK},
		"bad page":       {"?pageNum=0&perPage=2", nil, 0, http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodGet, server+"/api/v1/jobs"+tc.query, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantCode {
				t.Fatalf("GET /api/v1/jobs%s = %d, want %d: %s", tc.query, resp.StatusCode, tc.wantCode, body)
			}

			if tc.wantCode != http.StatusOK {
				return
			}

			var list struct {
				Jobs  []job.Job `json:"jobs"`
				Total int       `json:"total"`
			}

			if err := json.Unmarshal([]byte(body), &list); err != nil {
				t.Fatalf("GET /api/v1/jobs%s = %s, not a job list: %v", tc.query, body, err)
			}

			ids := []string{}
			for _, j := range list.Jobs {
				ids = append(ids, j.ID)
			}

			if diff := cmp.Diff(tc.wantIDs, ids); diff != "" || list.Total != tc.wantTotal {
				t.Errorf("GET /api/v1/jobs%s total = %d, want %d; jobs mismatch (-want +got):\n%s",
					tc.query, list.Total, tc.wantTotal, diff)
			}
		})
	}
}

// A job artifact gone from disk answers 404, and another user's refused download is audited.
//
//nolint:paralleltest // replaces the job registry and store
func TestJobArtifactGone(t *testing.T) {
	r := useJobs(t)

	var (
		alice  = serveAs(t, "alice", testRole("experiments", "get"))
		bob    = serveAs(t, "bob", testRole("experiments", "get"))
		zipped = waitJob(t, r, startTestJob(t, r, "alice", "exp1", nil, "0123").ID)
		path   = "/api/v1/jobs/" + zipped.ID + "/artifact"
		events = captureAudit(t)
	)

	if resp := do(t, http.MethodGet, bob.URL+path, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s as another user = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
	}

	wantAudit(t, events(), "file-download", "failure", map[string]any{
		"event.reason": "forbidden", "user.name": "bob", "phenix.experiment.name": "exp1", "phenix.job.id": zipped.ID,
	})

	if err := os.Remove(zipped.Artifact.Path); err != nil {
		t.Fatalf("removing the artifact: %v", err)
	}

	const want = "job artifact no longer exists"

	resp := do(t, http.MethodGet, alice.URL+path, "")
	if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); resp.StatusCode != http.StatusNotFound || got != want {
		t.Errorf("GET %s of a removed artifact = %d %q, want %d %q", path, resp.StatusCode, got, http.StatusNotFound, want)
	}
}

// GET /jobs and GET /jobs/{id} answer 500, with the error, for a job JSON cannot hold, as one dated past year 9999.
//
//nolint:paralleltest // replaces the job registry and store
func TestJobsThatCannotBeEncoded(t *testing.T) {
	future := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	r := useJobsIn(t, t.TempDir(), job.WithClock(func() time.Time { return future }))

	var (
		j      = waitJob(t, r, startTestJob(t, r, "alice", "exp1", nil, "").ID)
		server = serveAs(t, "alice", testRole("experiments", "get")).URL
	)

	for _, path := range []string{"/api/v1/jobs", "/api/v1/jobs/" + j.ID} {
		resp := do(t, http.MethodGet, server+path, "")
		body := readBody(t, resp)

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("GET %s of a job dated %d = %d, want %d: %s", path, future.Year(), resp.StatusCode,
				http.StatusInternalServerError, body)

			continue
		}

		if got := errorMessage(t, resp.StatusCode, body); !strings.Contains(got, "year outside of range") {
			t.Errorf("GET %s of a job dated %d message = %q, want the encoding error", path, future.Year(), got)
		}
	}
}
