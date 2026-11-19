package web

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// deleteWorkflowRequest builds a delete request for branch with the given raw
// query. Like the auth middleware, it puts role and the user "test-user" in
// the request context.
func deleteWorkflowRequest(role rbac.Role, branch, rawQuery string) *http.Request {
	target := "/api/v1/workflow/apply/" + url.PathEscape(branch)
	if rawQuery != "" {
		target += "?" + rawQuery
	}

	req := httptest.NewRequest(http.MethodDelete, target, nil)
	req = mux.SetURLVars(req, map[string]string{"branch": branch})

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

// serveDeleteWorkflow serves req with DeleteWorkflow behind weberror's error
// handler, as the router does, and returns the recorded response.
func serveDeleteWorkflow(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(DeleteWorkflow).ServeHTTP(rec, req)

	return rec
}

// decodeDeleteResult checks that rec is a 200 JSON response and returns its
// decoded body.
func decodeDeleteResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteWorkflow status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != mimeJSON {
		t.Fatalf("DeleteWorkflow Content-Type = %q, want %q", got, mimeJSON)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	return got
}

// deleteResultBody is the decoded JSON body of a delete response.
func deleteResultBody(branch string, dryRun bool, exp string, stopped bool, dir string, users ...any) map[string]any {
	if users == nil {
		users = []any{}
	}

	return map[string]any{
		"branch": branch, "dryRun": dryRun, "experiment": exp, "stopped": stopped,
		"injectsDir": dir, "injectsUsedBy": users,
	}
}

// useInjectsBase sets the server's injects base directory to dir until the
// test ends.
func useInjectsBase(t *testing.T, dir string) {
	t.Helper()

	original := common.InjectsBase
	t.Cleanup(func() { common.InjectsBase = original }) //nolint:reassign // restore the injects base

	common.InjectsBase = dir //nolint:reassign // a known injects base
}

// workflowInjectExperiment returns a stored experiment config named name,
// mapped to branch when branch is not empty, with one VM that injects src.
func workflowInjectExperiment(name, branch string, running bool, src string) store.Config {
	c := workflowTestExperiment(name, branch, running)
	c.Spec = map[string]any{
		"experimentName": name,
		"topology": map[string]any{"nodes": []any{map[string]any{
			"type":       "VirtualMachine",
			"general":    map[string]any{"hostname": "vm1"},
			"injections": []any{map[string]any{"src": src, "dst": "/dst"}},
		}}},
	}

	return c
}

// TestDeleteWorkflowRejectsRequest checks requests that are refused before
// experiments are listed: the store mock expects no calls.
func TestDeleteWorkflowRejectsRequest(t *testing.T) {
	tests := []struct {
		name        string
		deny        bool
		branch      string
		query       string
		wantStatus  int
		wantMessage string
	}{
		{
			name: "role without workflow delete", deny: true, branch: "main", wantStatus: http.StatusForbidden,
			wantMessage: "deleting phenix workflow deployment is not allowed for user test-user",
		},
		{
			name: "role without workflow delete, invalid branch name", deny: true, branch: ".main",
			wantStatus:  http.StatusForbidden,
			wantMessage: "deleting phenix workflow deployment is not allowed for user test-user",
		},
		{
			name: "branch name that starts with a dot", branch: ".main", wantStatus: http.StatusBadRequest,
			wantMessage: "invalid branch name",
		},
		{
			name: "branch name with a space", branch: "a b", wantStatus: http.StatusBadRequest,
			wantMessage: "invalid branch name",
		},
		{
			name: "dryRun not a boolean", branch: "main", query: "dryRun=yes", wantStatus: http.StatusBadRequest,
			wantMessage: `dryRun must be true or false, not "yes"`,
		},
		{
			name: "bare dryRun", branch: "main", query: "dryRun", wantStatus: http.StatusBadRequest,
			wantMessage: `dryRun must be true or false, not ""`,
		},
		{
			name: "force not a boolean", branch: "main", query: "force=maybe", wantStatus: http.StatusBadRequest,
			wantMessage: `force must be true or false, not "maybe"`,
		},
		{
			name: "relative injects", branch: "main", query: "injects=injects%2Fmain", wantStatus: http.StatusBadRequest,
			wantMessage: `injects must be an absolute path, not "injects/main"`,
		},
		{
			name: "empty injects", branch: "main", query: "injects=", wantStatus: http.StatusBadRequest,
			wantMessage: `injects must be an absolute path, not ""`,
		},
		{
			name: "injects given twice", branch: "main", query: "injects=%2Fa&injects=%2Fb",
			wantStatus: http.StatusBadRequest, wantMessage: "injects must be given at most once",
		},
		{
			name: "dryRun with a bad escape", branch: "main", query: "dryRun=%zz",
			wantStatus: http.StatusBadRequest, wantMessage: "invalid query string",
		},
		{
			name: "pairs split by a semicolon", branch: "main", query: "dryRun=true;force=true",
			wantStatus: http.StatusBadRequest, wantMessage: "invalid query string",
		},
		{
			name: "injects with a bad escape", branch: "main", query: "injects=%zz",
			wantStatus: http.StatusBadRequest, wantMessage: "invalid query string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installValidationTestStore(t)

			rec := serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(!tc.deny), tc.branch, tc.query))

			body := decodeWorkflowError(t, rec, tc.wantStatus)
			if body.Message != tc.wantMessage {
				t.Errorf("DeleteWorkflow message = %q, want %q", body.Message, tc.wantMessage)
			}
		})
	}
}

// TestDeleteWorkflowBranchNameError checks that the error of an invalid branch
// name wraps workflow.ErrInvalidName.
func TestDeleteWorkflowBranchNameError(t *testing.T) {
	installValidationTestStore(t)

	err := DeleteWorkflow(httptest.NewRecorder(), deleteWorkflowRequest(workflowTestRole(true), ".main", ""))
	if !errors.Is(err, workflow.ErrInvalidName) {
		t.Errorf("DeleteWorkflow() error = %v, want %v", err, workflow.ErrInvalidName)
	}
}

// TestDeleteWorkflowPlanErrors checks the refusals that depend on the stored
// experiments. A dry run is refused the same way. Nothing is written.
func TestDeleteWorkflowPlanErrors(t *testing.T) {
	const msg = "unable to delete phenix workflow deployment"

	tests := []struct {
		name       string
		query      string
		configs    []store.Config
		listErr    error
		wantStatus int
		wantMsg    string
		wantIs     error
	}{
		{
			name: "two experiments mapped",
			configs: []store.Config{
				workflowTestExperiment("b", "main", false), workflowTestExperiment("a", "main", true),
			},
			query: "force=true", wantStatus: http.StatusConflict, wantMsg: msg, wantIs: workflow.ErrConflict,
		},
		{
			name: "two experiments mapped, dry run",
			configs: []store.Config{
				workflowTestExperiment("b", "main", false), workflowTestExperiment("a", "main", false),
			},
			query: "dryRun=true", wantStatus: http.StatusConflict, wantMsg: msg, wantIs: workflow.ErrConflict,
		},
		{
			name:    "running without force",
			configs: []store.Config{workflowTestExperiment("foo", "main", true)},
			query:   "force=false", wantStatus: http.StatusConflict, wantMsg: msg, wantIs: workflow.ErrRunning,
		},
		{
			name:    "running without force, dry run",
			configs: []store.Config{workflowTestExperiment("foo", "main", true)},
			query:   "dryRun=true", wantStatus: http.StatusConflict, wantMsg: msg, wantIs: workflow.ErrRunning,
		},
		{
			name: "list fails", listErr: errors.New("disk full"),
			wantStatus: http.StatusInternalServerError, wantMsg: "unable to get list of experiments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			if tc.listErr != nil {
				m.EXPECT().List("Experiment").Return(nil, tc.listErr)
			} else {
				expectWorkflowList(m, tc.configs...)
			}

			req := deleteWorkflowRequest(workflowTestRole(true), "main", tc.query)

			body := decodeWorkflowError(t, serveDeleteWorkflow(t, req), tc.wantStatus)
			if body.Message != tc.wantMsg {
				t.Errorf("DeleteWorkflow message = %q, want %q", body.Message, tc.wantMsg)
			}

			if tc.wantIs == nil {
				return
			}

			req = deleteWorkflowRequest(workflowTestRole(true), "main", tc.query)
			expectWorkflowList(m, tc.configs...)

			if err := DeleteWorkflow(httptest.NewRecorder(), req); !errors.Is(err, tc.wantIs) {
				t.Errorf("DeleteWorkflow() error = %v, want %v", err, tc.wantIs)
			}
		})
	}
}

// TestDeleteWorkflowChangesNothing checks each request that answers 200 with
// no change: a dry run, or no mapped experiment. The store mock allows only
// the listing of experiments.
func TestDeleteWorkflowChangesNothing(t *testing.T) {
	const base = "/phenix/injects"

	var (
		stopped = workflowTestExperiment("foo", "main", false)
		running = workflowTestExperiment("foo", "main", true)
		user    = workflowInjectExperiment("user", "", false, base+"/main/a.txt")
		prefix  = workflowInjectExperiment("prefix", "", false, base+"/main-a/a.txt")
		self    = workflowInjectExperiment("foo", "main", false, base+"/main/b.txt")
		custom  = workflowInjectExperiment("custom", "dev", false, "/srv/files/x")
	)

	tests := []struct {
		name    string
		query   string
		configs []store.Config
		want    map[string]any
	}{
		{
			name: "no experiment mapped", configs: []store.Config{workflowTestExperiment("bar", "dev", true)},
			want: deleteResultBody("main", false, "", false, base+"/main"),
		},
		{
			name: "no experiment mapped, dry run", query: "dryRun=true",
			want: deleteResultBody("main", true, "", false, base+"/main"),
		},
		{
			name: "no experiment mapped, injects users", query: "force=true", configs: []store.Config{user, prefix},
			want: deleteResultBody("main", false, "", false, base+"/main", "user"),
		},
		{
			name: "stopped, dry run", query: "dryRun=true", configs: []store.Config{stopped},
			want: deleteResultBody("main", true, "foo", false, base+"/main"),
		},
		{
			name: "stopped, force, dry run", query: "dryRun=true&force=true", configs: []store.Config{stopped},
			want: deleteResultBody("main", true, "foo", false, base+"/main"),
		},
		{
			name: "running, force, dry run", query: "dryRun=true&force=true", configs: []store.Config{running},
			want: deleteResultBody("main", true, "foo", true, base+"/main"),
		},
		{
			name: "injects users leave out the mapped experiment", query: "dryRun=true",
			configs: []store.Config{self, prefix, user},
			want:    deleteResultBody("main", true, "foo", false, base+"/main", "user"),
		},
		{
			name: "injects query", query: "dryRun=true&injects=%2Fsrv%2F%2Ffiles%2F",
			configs: []store.Config{stopped, custom, user},
			want:    deleteResultBody("main", true, "foo", false, "/srv/files", "custom"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useInjectsBase(t, base)
			expectWorkflowList(installValidationTestStore(t), tc.configs...)

			got := decodeDeleteResult(t, serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(true), "main", tc.query)))
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Errorf("DeleteWorkflow body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDeleteWorkflowDeletesExperiment checks that a real delete removes the
// stopped experiment mapped to the branch, keeps the others and names those
// that inject files from the branch's injects directory.
func TestDeleteWorkflowDeletesExperiment(t *testing.T) { //nolint:paralleltest // replaces package globals
	useBaseDirTest(t)
	useInjectsBase(t, "/phenix/injects")

	for _, c := range []store.Config{
		workflowTestExperiment("lab", "lab", false),
		workflowInjectExperiment("other", "", true, "/phenix/injects/lab/a.txt"),
	} {
		if err := store.Create(&c); err != nil {
			t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
		}
	}

	got := decodeDeleteResult(t, serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(true), "lab", "")))

	if diff := gocmp.Diff(deleteResultBody("lab", false, "lab", false, "/phenix/injects/lab", "other"), got); diff != "" {
		t.Errorf("DeleteWorkflow body mismatch (-want +got):\n%s", diff)
	}

	deleted, _ := store.NewConfig("experiment/lab")
	if err := store.Get(deleted); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("store.Get(experiment/lab) error = %v, want %v", err, store.ErrNotExist)
	}

	kept, _ := store.NewConfig("experiment/other")
	if err := store.Get(kept); err != nil {
		t.Errorf("store.Get(experiment/other) error = %v, want nil", err)
	}
}

// TestDeleteWorkflowLockedExperiment checks that a real delete answers 409,
// and deletes nothing, when another request holds the experiment's lock.
func TestDeleteWorkflowLockedExperiment(t *testing.T) {
	expectWorkflowList(installValidationTestStore(t), workflowTestExperiment("foo", "main", false))

	if err := cache.LockExperimentForUpdate("foo"); err != nil {
		t.Fatalf("locking foo: %v", err)
	}

	t.Cleanup(func() { cache.UnlockExperiment("foo") })

	rec := serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(true), "main", ""))

	body := decodeWorkflowError(t, rec, http.StatusConflict)
	if want := "unable to lock experiment foo for deletion"; body.Message != want {
		t.Errorf("DeleteWorkflow message = %q, want %q", body.Message, want)
	}
}

// afterExperimentLists makes the test store run hooks[i] after its listing i
// of experiments, counted from 0, as another request would between the steps
// of a delete. A nil hook does nothing.
func afterExperimentLists(t *testing.T, hooks ...func()) {
	t.Helper()

	s, ok := store.DefaultStore.(*testStore)
	if !ok {
		t.Fatal("afterExperimentLists: the store is not a testStore; call useTestStore first")
	}

	var count atomic.Int32

	s.mu.Lock()
	s.listed = func(kinds []string) {
		if !slices.ContainsFunc(kinds, func(k string) bool { return strings.EqualFold(k, "Experiment") }) {
			return
		}

		if i := int(count.Add(1)) - 1; i < len(hooks) && hooks[i] != nil {
			hooks[i]()
		}
	}
	s.mu.Unlock()

	t.Cleanup(func() {
		s.mu.Lock()
		s.listed = nil
		s.mu.Unlock()
	})
}

// workflowLabExperiment returns the stored experiment config lab, mapped to
// the branch lab, running when running is true, with a topology of no nodes.
// Its base directory is in the experiments directory of the phenix base.
func workflowLabExperiment(running bool) store.Config {
	c := workflowTestExperiment("lab", "lab", running)
	c.Spec = map[string]any{
		"experimentName": "lab",
		"baseDir":        filepath.Join(experiment.BaseDirRoot(), "lab"),
		"topology":       map[string]any{"nodes": []any{}},
	}

	return c
}

// createWorkflowTestConfig stores c, or fails the test.
func createWorkflowTestConfig(t *testing.T, c store.Config) {
	t.Helper()

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
	}
}

// TestDeleteWorkflowStopsAndDeletes checks a real delete against the fake
// minimega: a forced stop of the running experiment, the changes another
// request makes between the plan and the delete, and a delete that fails
// after the stop. Each request logs one audit record.
func TestDeleteWorkflowStopsAndDeletes(t *testing.T) { //nolint:paralleltest // replaces package globals
	const dir = "/phenix/injects/lab"

	tests := []struct {
		name    string
		running bool
		// others are stored before the request; hook runs after the first listing of experiments
		others     []store.Config
		hook       func(t *testing.T)
		failDelete bool
		want       map[string]any // the body of a 200
		wantStatus int
		wantMsg    string
		wantGone   bool
		outcome    string
		audit      map[string]any
	}{
		{
			name: "running, force", running: true,
			want:     deleteResultBody("lab", false, "lab", true, dir),
			wantGone: true, outcome: "success",
			audit: map[string]any{"phenix.workflow.force": true, "phenix.workflow.stopped": true},
		},
		{
			name: "stopped by another request", running: true,
			hook: func(t *testing.T) {
				t.Helper()

				c := workflowLabExperiment(false)
				if err := store.Update(&c); err != nil {
					t.Errorf("store.Update(lab) error = %v", err)
				}
			},
			want:     deleteResultBody("lab", false, "lab", false, dir),
			wantGone: true, outcome: "success",
			audit: map[string]any{"phenix.workflow.force": true, "phenix.workflow.stopped": nil},
		},
		{
			name: "running, deleted by another request before the stop", running: true,
			hook: func(t *testing.T) {
				t.Helper()

				c := workflowLabExperiment(true)
				if err := store.Delete(&c); err != nil {
					t.Errorf("store.Delete(lab) error = %v", err)
				}
			},
			want:     deleteResultBody("lab", false, "lab", false, dir),
			wantGone: true, outcome: "success",
			audit: map[string]any{"phenix.workflow.force": true, "phenix.workflow.stopped": nil},
		},
		{
			name: "deleted by another request",
			hook: func(t *testing.T) {
				t.Helper()

				c := workflowLabExperiment(false)
				if err := store.Delete(&c); err != nil {
					t.Errorf("store.Delete(lab) error = %v", err)
				}
			},
			want:     deleteResultBody("lab", false, "lab", false, dir),
			wantGone: true, outcome: "success",
			audit: map[string]any{"phenix.workflow.force": true, "phenix.workflow.stopped": nil},
		},
		{
			name: "injects users from after the delete",
			others: []store.Config{
				workflowInjectExperiment("early", "", false, dir+"/a.txt"),
			},
			hook: func(t *testing.T) {
				t.Helper()

				createWorkflowTestConfig(t, workflowInjectExperiment("late", "", false, dir+"/b.txt"))

				early, _ := store.NewConfig("experiment/early")
				if err := store.Delete(early); err != nil {
					t.Errorf("store.Delete(early) error = %v", err)
				}
			},
			want:     deleteResultBody("lab", false, "lab", false, dir, "late"),
			wantGone: true, outcome: "success",
			audit: map[string]any{"phenix.workflow.stopped": nil},
		},
		{
			name: "stopped, then the delete fails", running: true, failDelete: true,
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "experiment lab was stopped; unable to delete experiment lab",
			outcome:    "failure",
			audit:      map[string]any{"phenix.workflow.force": true, "phenix.workflow.stopped": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useBaseDirTest(t)
			useInjectsBase(t, "/phenix/injects")
			experimentStopClearCancelers(t, "lab")

			fake := &snapshotMM{teardownMM: &teardownMM{testMM: &testMM{vms: mm.VMs{}}}}
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

			mm.DefaultMM = fake //nolint:reassign // install test double

			createWorkflowTestConfig(t, workflowLabExperiment(tt.running))

			for _, c := range tt.others {
				createWorkflowTestConfig(t, c)
			}

			if tt.hook != nil {
				afterExperimentLists(t, func() { tt.hook(t) })
			}

			if tt.failDelete {
				failTestStore(t, "delete", "Experiment", errors.New("disk full"))
			}

			events := captureAudit(t)

			rec := serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(true), "lab", "force=true"))

			if tt.want != nil {
				if diff := gocmp.Diff(tt.want, decodeDeleteResult(t, rec)); diff != "" {
					t.Errorf("DeleteWorkflow body mismatch (-want +got):\n%s", diff)
				}
			} else if body := decodeWorkflowError(t, rec, tt.wantStatus); body.Message != tt.wantMsg {
				t.Errorf("DeleteWorkflow message = %q, want %q", body.Message, tt.wantMsg)
			}

			c, _ := store.NewConfig("experiment/lab")
			if gone := errors.Is(store.Get(c), store.ErrNotExist); gone != tt.wantGone {
				t.Errorf("experiment lab deleted = %t, want %t", gone, tt.wantGone)
			}

			fields := map[string]any{"phenix.workflow.branch": "lab", "phenix.experiment.name": "lab"}
			maps.Copy(fields, tt.audit)

			wantAudit(t, events(), "workflow-delete", tt.outcome, fields)

			if n := len(events()); n != 1 {
				t.Errorf("DELETE /workflow/apply/lab logged %d audit events, want 1", n)
			}
		})
	}
}

// TestDeleteWorkflowFailsWhenTheListAfterTheDeleteFails checks that a delete
// whose second listing of experiments fails answers 500, so that the client
// keeps the injects directory, and says that the experiment is deleted.
func TestDeleteWorkflowFailsWhenTheListAfterTheDeleteFails(t *testing.T) { //nolint:paralleltest // replaces package globals
	useBaseDirTest(t)
	useInjectsBase(t, "/srv/injects")

	createWorkflowTestConfig(t, workflowLabExperiment(false))

	// no experiment uses the directory in the first listing
	afterExperimentLists(t, func() { failTestStore(t, "list", "Experiment", errors.New("disk busy")) })

	rec := serveDeleteWorkflow(t, deleteWorkflowRequest(workflowTestRole(true), "lab", ""))

	body := decodeWorkflowError(t, rec, http.StatusInternalServerError)

	want := "experiment lab was deleted, but phenix cannot tell which experiments use the injects directory " +
		"/srv/injects/lab; keep the directory and send the delete again"
	if body.Message != want {
		t.Errorf("DeleteWorkflow message = %q, want %q", body.Message, want)
	}

	deleted, _ := store.NewConfig("experiment/lab")
	if err := store.Get(deleted); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("store.Get(experiment/lab) error = %v, want %v", err, store.ErrNotExist)
	}
}
