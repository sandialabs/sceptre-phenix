package scorch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/scorch/scorchexe"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

var startOnce sync.Once //nolint:gochecknoglobals // the processors run for the whole test binary

// useScorch starts the SCORCH processors and stores a running experiment exp
// whose SCORCH app, running run 3, has the given runs.
func useScorch(t *testing.T, runs ...map[string]any) {
	t.Helper()

	startOnce.Do(func() { Start("/") })

	// forget the pipelines built from this test's runs
	t.Cleanup(func() { DeletePipeline("exp", -1, -1, false) })

	storetest.Use(t)

	c, _ := store.NewConfig("experiment/exp")
	c.Spec = map[string]any{
		"experimentName": "exp",
		"scenario": map[string]any{
			"apps": []map[string]any{{"name": "scorch", "metadata": map[string]any{"runs": runs}}},
		},
	}
	c.Status = map[string]any{
		"startTime":             "2026-01-01T00:00:00Z",
		"appRunningStageStatus": map[string]any{"scorch": true},
		"apps":                  map[string]any{"scorch": map[string]any{"runID": 3}},
	}

	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
}

// roleFor may take the given verbs on the given resources of experiment exp.
func roleFor(resourceVerbs ...string) *rbac.Role {
	role := &rbac.Role{Spec: &v1.RoleSpec{}}

	for i := 0; i < len(resourceVerbs); i += 2 {
		role.Spec.Policies = append(role.Spec.Policies, &v1.PolicySpec{
			Resources: []string{resourceVerbs[i]}, ResourceNames: []string{"exp"}, Verbs: []string{resourceVerbs[i+1]},
		})
	}

	return role
}

// serveScorch serves a request through the SCORCH routes as a user with role,
// or with no role when role is nil.
func serveScorch(method, path string, role *rbac.Role) *httptest.ResponseRecorder {
	router := mux.NewRouter()
	RegisterRoutes(router)

	req := httptest.NewRequest(method, path, nil)

	if role != nil {
		ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, *role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")
		req = req.WithContext(ctx)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// Only a role that may get an experiment sees its SCORCH component output and
// terminals.
//
//nolint:paralleltest // starts the SCORCH processors
func TestViewingOutputNeedsExperimentGet(t *testing.T) {
	useScorch(t)

	requests := map[string]struct{ method, path string }{
		"component output":        {http.MethodGet, "/experiments/exp/scorch/components/0/0/start/cc"},
		"component output stream": {http.MethodGet, "/experiments/exp/scorch/components/0/0/start/cc/ws"},
		"terminals":               {http.MethodGet, "/experiments/exp/scorch/terminals"},
		"terminal by PID":         {http.MethodGet, "/experiments/exp/scorch/terminals/1"},
		"component terminal":      {http.MethodGet, "/experiments/exp/scorch/terminals/0/0/start/cc"},
		"terminal stream":         {http.MethodGet, "/experiments/exp/scorch/terminals/1/ws/id"},
		"terminal exit":           {http.MethodPost, "/experiments/exp/scorch/terminals/1/exit/id"},
	}

	other := roleFor("experiments", "get")
	other.Spec.Policies[0].ResourceNames = []string{"other"}

	denied := map[string]*rbac.Role{
		"no role":                        nil,
		"a role for other experiments":   other,
		"a role that may only list them": roleFor("experiments", "list"),
	}

	for name, req := range requests {
		for who, role := range denied {
			if rec := serveScorch(req.method, req.path, role); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: status %d, want 403", name, who, rec.Code)
			}
		}
	}

	viewer := roleFor("experiments", "get")

	for path, want := range map[string]string{
		"/experiments/exp/scorch/terminals":               `{"terminals":null}`,
		"/experiments/exp/scorch/components/0/0/start/cc": `{"output":""}`,
	} {
		rec := serveScorch(http.MethodGet, path, viewer)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s as a viewer: %d %s, want 200 %s", path, rec.Code, rec.Body, want)
		}
	}
}

// Clearing a run forgets its component statuses, and cleaning one up runs
// only its cleanup stage. Neither may touch a run that is executing or does
// not exist, and cleaning up needs cleanup components.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestClearAndCleanUpRuns(t *testing.T) {
	withCleanup := map[string]any{"start": []string{"a"}, "cleanup": []string{"c"}}

	useScorch(t, withCleanup, withCleanup, map[string]any{"start": []string{"a"}})

	// run 1 is executing
	scorchexe.AddCanceler(context.Background(), "exp", 1)
	t.Cleanup(func() { scorchexe.GetCanceler("exp", 1)() })

	var (
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
		viewer   = roleFor("experiments", "get")
	)

	// a component's status shows in its run's pipeline until the run is cleared
	update := ComponentUpdate{Exp: "exp", Run: 0, Stage: stageStart, CmpName: "a", Status: statusSuccess}
	if err := UpdatePipeline(update); err != nil {
		t.Fatal(err)
	}

	if got := componentStatus(t, 0, "a"); got != statusSuccess {
		t.Fatalf("status before clearing = %q, want %q", got, statusSuccess)
	}

	if rec := serveScorch(http.MethodPost, "/experiments/exp/scorch/pipelines/0/clear", operator); rec.Code != http.StatusNoContent {
		t.Fatalf("clearing run 0: %d %s", rec.Code, rec.Body)
	}

	if got := componentStatus(t, 0, "a"); got != statusUnknown {
		t.Errorf("status after clearing = %q, want %q", got, statusUnknown)
	}

	tests := map[string]struct {
		path    string
		role    *rbac.Role
		status  int
		message string
	}{
		"cannot clear an executing run": {
			"pipelines/1/clear", operator, http.StatusConflict, "is executing",
		},
		"cannot clear a run that does not exist": {
			"pipelines/3/clear", operator, http.StatusNotFound, "not found",
		},
		"cannot clear without trigger rights": {
			"pipelines/0/clear", viewer, http.StatusForbidden, "not allowed",
		},
		"cannot clean up an executing run": {
			"pipelines/1/cleanup", operator, http.StatusBadRequest, "already executing",
		},
		"cannot clean up a run without cleanup components": {
			"pipelines/2/cleanup", operator, http.StatusBadRequest, "has no cleanup components",
		},
		"cannot clean up a run that does not exist": {
			"pipelines/3/cleanup", operator, http.StatusBadRequest, "has no cleanup components",
		},
		"cannot clean up without trigger rights": {
			"pipelines/0/cleanup", viewer, http.StatusForbidden, "not allowed",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serveScorch(http.MethodPost, "/experiments/exp/scorch/"+tc.path, tc.role)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Errorf("%d %s, want %d with %q", rec.Code, rec.Body, tc.status, tc.message)
			}
		})
	}
}

// An experiment without a SCORCH app has no runs to clear or clean up.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestClearAndCleanUpWithoutScorch(t *testing.T) {
	useScorch(t)

	c, _ := store.NewConfig("experiment/plain")
	c.Spec = map[string]any{"experimentName": "plain"}

	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}

	operator := roleFor("experiments", "get", "experiments/trigger", "create")
	operator.Spec.Policies[0].ResourceNames = []string{"plain"}
	operator.Spec.Policies[1].ResourceNames = []string{"plain"}

	for path, want := range map[string]int{
		"/experiments/plain/scorch/pipelines/0/clear":   http.StatusNotFound,
		"/experiments/plain/scorch/pipelines/0/cleanup": http.StatusBadRequest,
	} {
		if rec := serveScorch(http.MethodPost, path, operator); rec.Code != want {
			t.Errorf("POST %s: %d %s, want %d", path, rec.Code, rec.Body, want)
		}
	}
}

// componentStatus returns the status of the named component in a run's
// pipeline, as GET /experiments/exp/scorch/pipelines lists it.
func componentStatus(t *testing.T, run int, name string) string {
	t.Helper()

	rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/pipelines", roleFor("experiments", "get"))
	if rec.Code != http.StatusOK {
		t.Fatalf("listing pipelines: %d %s", rec.Code, rec.Body)
	}

	var body struct {
		Pipelines []struct {
			Pipeline []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"pipeline"`
		} `json:"pipelines"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	for _, n := range body.Pipelines[run].Pipeline {
		if n.Name == name {
			return n.Status
		}
	}

	t.Fatalf("run %d has no component %s", run, name)

	return ""
}

// fakeTerminal registers a terminal of experiment exp's component cc, with no
// process behind it, and forgets it when the test ends.
func fakeTerminal(t *testing.T, pid int) {
	t.Helper()

	term := newWebTerm("exp", 0, 0, "start", "cc")
	term.Pid = pid

	webTermMu.Lock()
	webTermsPid[pid] = term
	webTermsExp[term.key] = term
	webTermMu.Unlock()

	t.Cleanup(func() {
		webTermMu.Lock()
		delete(webTermsPid, pid)
		delete(webTermsExp, term.key)
		webTermMu.Unlock()

		mu.Lock()
		delete(rwTerm, pid)
		mu.Unlock()
	})
}

// A role that may only view an experiment gets a read-only SCORCH terminal and
// may not stop it, even when no one holds its read/write side; a role that may
// trigger the experiment gets the read/write side and its exit location.
//
//nolint:paralleltest // shares the terminal maps
func TestTerminalWriteNeedsTrigger(t *testing.T) {
	const pid = 999999

	fakeTerminal(t, pid)

	var (
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	connect := func(role *rbac.Role) WebTerm {
		t.Helper()

		rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/terminals/0/0/start/cc", role)
		if rec.Code != http.StatusOK {
			t.Fatalf("connecting: status %d %s", rec.Code, rec.Body)
		}

		var term WebTerm
		if err := json.Unmarshal(rec.Body.Bytes(), &term); err != nil {
			t.Fatal(err)
		}

		return term
	}

	if term := connect(viewer); !term.RO || term.Exit != "" {
		t.Fatalf("viewer got read-only %v and exit %q, want a read-only terminal with no exit", term.RO, term.Exit)
	}

	mu.Lock()
	_, held := rwTerm[pid]
	mu.Unlock()

	if held {
		t.Fatal("a viewer took the terminal's read/write side")
	}

	term := connect(operator)
	if term.RO || term.Exit == "" {
		t.Fatalf("operator got read-only %v and exit %q, want the read/write side", term.RO, term.Exit)
	}

	mu.Lock()
	id := rwTerm[pid]
	mu.Unlock()

	exit := "/experiments/exp/scorch/terminals/999999/exit/" + id
	if rec := serveScorch(http.MethodPost, exit, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer exiting with the read/write ID: status %d, want 403", rec.Code)
	}

	stream := "/experiments/exp/scorch/terminals/999999/ws/" + id
	if rec := serveScorch(http.MethodGet, stream, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer streaming with the read/write ID: status %d, want 403", rec.Code)
	}
}

// A terminal is reached by the PID of the process it runs, as a terminal of
// its experiment only. A role that may only view the experiment
// gets a read-only terminal and never takes its read/write side; the first
// client that may trigger the experiment may write to it, and the others only
// watch.
//
//nolint:paralleltest // shares the terminal maps
func TestConnectTerminalByPID(t *testing.T) {
	const pid = 4242

	fakeTerminal(t, pid)

	var (
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	connect := func(role *rbac.Role, readOnly bool) {
		t.Helper()

		rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/terminals/4242", role)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}

		var got WebTerm
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		if got.Exp != "exp" || got.Stage != "start" || got.Name != "cc" || got.RO != readOnly ||
			!strings.Contains(got.Loc, "/scorch/terminals/4242/ws/") || (got.Exit != "") == readOnly {
			t.Errorf("terminal = %+v, want cc's, read-only %v", got, readOnly)
		}
	}

	connect(viewer, true)

	mu.Lock()
	_, held := rwTerm[pid]
	mu.Unlock()

	if held {
		t.Fatal("a viewer took the terminal's read/write side")
	}

	connect(operator, false)
	connect(operator, true)
	connect(viewer, true)

	for path, want := range map[string]int{
		"/experiments/exp/scorch/terminals/1":      http.StatusNotFound,
		"/experiments/exp/scorch/terminals/x":      http.StatusBadRequest,
		"/experiments/other/scorch/terminals/4242": http.StatusNotFound,
	} {
		role := roleFor("experiments", "get")
		role.Spec.Policies[0].ResourceNames = []string{"*"}

		if rec := serveScorch(http.MethodGet, path, role); rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}

// The writing client keeps its rights when a read-only client connects and
// never attaches, and loses them only when it never attaches itself.
//
//nolint:paralleltest // shortens the attach timeout and shares the terminal maps
func TestTerminalWriterOutlastsUnattachedViewer(t *testing.T) {
	const pid = 4343

	original := terminalInitTimeout
	terminalInitTimeout = 20 * time.Millisecond

	t.Cleanup(func() { terminalInitTimeout = original })

	fakeTerminal(t, pid)

	var (
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	connect := func(role *rbac.Role) WebTerm {
		t.Helper()

		rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/terminals/4343", role)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}

		var got WebTerm
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		return got
	}

	writer := connect(operator)
	if writer.RO {
		t.Fatal("the first operator may not write")
	}

	writerID := writer.Loc[strings.LastIndex(writer.Loc, "/")+1:]

	// the writer attaches, as StreamTerminal does
	mu.Lock()
	close(termClientIDs[writerID])
	mu.Unlock()

	for _, role := range []*rbac.Role{viewer, operator} {
		if other := connect(role); !other.RO {
			t.Fatal("a second client may write")
		}
	}

	time.Sleep(10 * terminalInitTimeout)

	mu.Lock()
	owner := rwTerm[pid]
	mu.Unlock()

	if owner != writerID {
		t.Fatalf("writer %q, want the attached writer %q", owner, writerID)
	}

	// a writer that never attaches frees the terminal for the next client
	mu.Lock()
	delete(rwTerm, pid)
	mu.Unlock()

	if unattached := connect(operator); unattached.RO {
		t.Fatal("the terminal has a writer")
	}

	time.Sleep(10 * terminalInitTimeout)

	if next := connect(operator); next.RO {
		t.Error("an unattached writer kept its rights")
	}
}
