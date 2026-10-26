package scorch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"

	"phenix/api/scorch/scorchexe"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/polltest"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

var startOnce sync.Once //nolint:gochecknoglobals // the processors run for the whole test binary

// useScorch starts the SCORCH processors and stores a running experiment exp
// whose SCORCH app, running run 3, has the given runs.
func useScorch(t *testing.T, runs ...map[string]any) {
	t.Helper()

	startOnce.Do(func() { Start(context.Background(), "/") })

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
				t.Errorf("%s %s (%s) as %s = %d, want 403", req.method, req.path, name, who, rec.Code)
			}
		}
	}

	viewer := roleFor("experiments", "get")

	for path, want := range map[string]map[string]any{
		"/experiments/exp/scorch/terminals":               {"terminals": nil},
		"/experiments/exp/scorch/components/0/0/start/cc": {"output": ""},
	} {
		rec := serveScorch(http.MethodGet, path, viewer)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s as a viewer = %d %s, want 200", path, rec.Code, rec.Body)

			continue
		}

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("GET %s as a viewer: decoding %q: %v", path, rec.Body, err)

			continue
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET %s as a viewer mismatch (-want +got):\n%s", path, diff)
		}
	}
}

// Clearing a run forgets its component statuses; cleaning up runs only its
// cleanup stage; neither touches an executing or missing run.
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
		t.Fatalf("UpdatePipeline(%+v) = %v, want nil", update, err)
	}

	if got := componentStatus(t, 0, "a"); got != statusSuccess {
		t.Fatalf("after UpdatePipeline(%+v), component a status = %q, want %q", update, got, statusSuccess)
	}

	const clearPath = "/experiments/exp/scorch/pipelines/0/clear"
	if rec := serveScorch(http.MethodPost, clearPath, operator); rec.Code != http.StatusNoContent {
		t.Fatalf("POST %s = %d %s, want 204", clearPath, rec.Code, rec.Body)
	}

	if got := componentStatus(t, 0, "a"); got != statusUnknown {
		t.Errorf("after POST %s, component a status = %q, want %q", clearPath, got, statusUnknown)
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
			path := "/experiments/exp/scorch/" + tc.path

			rec := serveScorch(http.MethodPost, path, tc.role)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Errorf("POST %s = %d %s, want %d with %q", path, rec.Code, rec.Body, tc.status, tc.message)
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
		t.Fatalf("store.Create(experiment/plain) = %v", err)
	}

	operator := roleFor("experiments", "get", "experiments/trigger", "create")
	operator.Spec.Policies[0].ResourceNames = []string{"plain"}
	operator.Spec.Policies[1].ResourceNames = []string{"plain"}

	for path, want := range map[string]int{
		"/experiments/plain/scorch/pipelines/0/clear":   http.StatusNotFound,
		"/experiments/plain/scorch/pipelines/0/cleanup": http.StatusBadRequest,
	} {
		if rec := serveScorch(http.MethodPost, path, operator); rec.Code != want {
			t.Errorf("POST %s = %d %s, want %d", path, rec.Code, rec.Body, want)
		}
	}
}

// componentStatus returns the status of the named component in a run's
// pipeline, as GET /experiments/exp/scorch/pipelines lists it.
func componentStatus(t *testing.T, run int, name string) string {
	t.Helper()

	rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/pipelines", roleFor("experiments", "get"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /experiments/exp/scorch/pipelines = %d %s, want 200", rec.Code, rec.Body)
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
		t.Fatalf("decoding GET /experiments/exp/scorch/pipelines: %v", err)
	}

	if run >= len(body.Pipelines) {
		t.Fatalf("GET /experiments/exp/scorch/pipelines lists %d runs, want run %d", len(body.Pipelines), run)
	}

	for _, n := range body.Pipelines[run].Pipeline {
		if n.Name == name {
			return n.Status
		}
	}

	t.Fatalf("GET /experiments/exp/scorch/pipelines: run %d has no component %s", run, name)

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

// connectTerminal connects a client with role to the terminal at path and
// returns the terminal it gets.
func connectTerminal(t *testing.T, path string, role *rbac.Role) WebTerm {
	t.Helper()

	rec := serveScorch(http.MethodGet, path, role)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, rec.Code, rec.Body)
	}

	var term WebTerm
	if err := json.Unmarshal(rec.Body.Bytes(), &term); err != nil {
		t.Fatalf("decoding GET %s: %v", path, err)
	}

	return term
}

// writerOf returns the client ID holding the read/write side of terminal pid.
func writerOf(pid int) (string, bool) {
	mu.Lock()
	defer mu.Unlock()

	id, ok := rwTerm[pid]

	return id, ok
}

// A view-only role gets a read-only terminal it cannot stop; a trigger role
// gets the read/write side and exit location, by component or by PID.
//
//nolint:paralleltest // shares the terminal maps
func TestConnectTerminal(t *testing.T) {
	var (
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	for _, tc := range []struct {
		route, path string
		pid         int
	}{
		{"component", "/experiments/exp/scorch/terminals/0/0/start/cc", 999999},
		{"pid", "/experiments/exp/scorch/terminals/4242", 4242},
	} {
		t.Run(tc.route, func(t *testing.T) {
			fakeTerminal(t, tc.pid)

			if term := connectTerminal(t, tc.path, viewer); !term.RO || term.Exit != "" {
				t.Fatalf("GET %s as a viewer = read-only %v, exit %q; want read-only, no exit", tc.path, term.RO, term.Exit)
			}

			if id, held := writerOf(tc.pid); held {
				t.Fatalf("after GET %s as a viewer, writerOf(%d) = %q, true; want none", tc.path, tc.pid, id)
			}

			term := connectTerminal(t, tc.path, operator)

			want := WebTerm{Exp: "exp", Stage: "start", Name: "cc"}
			opts := cmp.Options{cmpopts.IgnoreFields(WebTerm{}, "Loc", "Exit"), cmpopts.IgnoreUnexported(WebTerm{})}

			if diff := cmp.Diff(want, term, opts); diff != "" {
				t.Fatalf("GET %s as an operator mismatch (-want +got):\n%s", tc.path, diff)
			}

			if loc := fmt.Sprintf("/scorch/terminals/%d/ws/", tc.pid); term.Exit == "" || !strings.Contains(term.Loc, loc) {
				t.Fatalf("GET %s as an operator = loc %q, exit %q; want a loc under %q and an exit", tc.path, term.Loc, term.Exit, loc)
			}

			for _, role := range []*rbac.Role{operator, viewer} {
				if other := connectTerminal(t, tc.path, role); !other.RO {
					t.Errorf("GET %s after the writer connected = read-only false, want true", tc.path)
				}
			}

			id, _ := writerOf(tc.pid)

			exit := fmt.Sprintf("/experiments/exp/scorch/terminals/%d/exit/%s", tc.pid, id)
			if rec := serveScorch(http.MethodPost, exit, viewer); rec.Code != http.StatusForbidden {
				t.Errorf("POST %s as a viewer with the writer's ID = %d, want 403", exit, rec.Code)
			}

			stream := fmt.Sprintf("/experiments/exp/scorch/terminals/%d/ws/%s", tc.pid, id)
			if rec := serveScorch(http.MethodGet, stream, viewer); rec.Code != http.StatusForbidden {
				t.Errorf("GET %s as a viewer with the writer's ID = %d, want 403", stream, rec.Code)
			}
		})
	}

	fakeTerminal(t, 4242)

	for path, want := range map[string]int{
		"/experiments/exp/scorch/terminals/1":               http.StatusNotFound,
		"/experiments/exp/scorch/terminals/x":               http.StatusBadRequest,
		"/experiments/other/scorch/terminals/4242":          http.StatusNotFound,
		"/experiments/exp/scorch/terminals/4242/ws/unknown": http.StatusNotFound,
	} {
		role := roleFor("experiments", "get")
		role.Spec.Policies[0].ResourceNames = []string{"*"}

		if rec := serveScorch(http.MethodGet, path, role); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

// The writing client keeps its rights when a read-only client connects and
// never attaches, and loses them only when it never attaches itself.
//
//nolint:paralleltest // shortens the attach timeout and shares the terminal maps
func TestTerminalWriterOutlastsUnattachedViewer(t *testing.T) {
	const (
		pid  = 4343
		path = "/experiments/exp/scorch/terminals/4343"
	)

	original := terminalInitTimeout
	terminalInitTimeout = 20 * time.Millisecond

	t.Cleanup(func() { terminalInitTimeout = original })

	fakeTerminal(t, pid)

	var (
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	clientID := func(term WebTerm) string { return term.Loc[strings.LastIndex(term.Loc, "/")+1:] }

	writer := connectTerminal(t, path, operator)
	if writer.RO {
		t.Fatalf("GET %s as the first operator = read-only true, want false", path)
	}

	writerID := clientID(writer)

	// the writer attaches, as StreamTerminal does
	mu.Lock()
	close(termClientIDs[writerID])
	mu.Unlock()

	pending := make([]string, 0, 2)

	for _, role := range []*rbac.Role{viewer, operator} {
		other := connectTerminal(t, path, role)
		if !other.RO {
			t.Fatalf("GET %s after the writer connected = read-only false, want true", path)
		}

		pending = append(pending, clientID(other))
	}

	polltest.Until(t, "the unattached clients time out", func() bool {
		mu.Lock()
		defer mu.Unlock()

		for _, id := range pending {
			if _, ok := termClientIDs[id]; ok {
				return false
			}
		}

		return true
	})

	if owner, _ := writerOf(pid); owner != writerID {
		t.Fatalf("writerOf(%d) = %q, want the attached writer %q", pid, owner, writerID)
	}

	// a writer that never attaches frees the terminal for the next client
	mu.Lock()
	delete(rwTerm, pid)
	mu.Unlock()

	if unattached := connectTerminal(t, path, operator); unattached.RO {
		t.Fatalf("GET %s with no writer = read-only true, want false", path)
	}

	polltest.Until(t, "the unattached writer is freed", func() bool {
		_, held := writerOf(pid)

		return !held
	})

	if next := connectTerminal(t, path, operator); next.RO {
		t.Errorf("GET %s after the unattached writer timed out = read-only true, want false", path)
	}
}

// A terminal client ID is claimed once; the writer's ID sent by a role that may
// not write is refused and kept for the writer.
//
//nolint:paralleltest // shares the terminal maps
func TestClaimTerminalClient(t *testing.T) {
	for i, tc := range []struct {
		name                  string
		writer, writable      bool
		wantStatus            int
		wantReadOnly, claimed bool
	}{
		{"writer that may write", true, true, http.StatusOK, false, true},
		{"writer that may not write", true, false, http.StatusForbidden, false, false},
		{"reader that may write", false, true, http.StatusOK, true, true},
		{"reader that may not write", false, false, http.StatusOK, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid, id, done := 5000+i, fmt.Sprintf("client-%d", i), make(chan struct{})

			mu.Lock()
			termClientIDs[id] = done
			rwTerm[pid] = "other-client"

			if tc.writer {
				rwTerm[pid] = id
			}
			mu.Unlock()

			t.Cleanup(func() {
				mu.Lock()
				delete(termClientIDs, id)
				delete(rwTerm, pid)
				mu.Unlock()
			})

			readOnly, status := claimTerminalClient(pid, id, tc.writable)
			if status != tc.wantStatus || (status == http.StatusOK && readOnly != tc.wantReadOnly) {
				t.Errorf("claimTerminalClient(%d, %q, %t) = %t, %d; want %t, %d",
					pid, id, tc.writable, readOnly, status, tc.wantReadOnly, tc.wantStatus)
			}

			mu.Lock()
			_, pending := termClientIDs[id]
			mu.Unlock()

			if pending == tc.claimed {
				t.Errorf("after claimTerminalClient(%d, %q, %t), ID pending = %t, want %t",
					pid, id, tc.writable, pending, !tc.claimed)
			}

			if !tc.claimed {
				return
			}

			select {
			case <-done:
			default:
				t.Errorf("claimTerminalClient(%d, %q, %t) left the ID's done channel open", pid, id, tc.writable)
			}

			if _, status := claimTerminalClient(pid, id, tc.writable); status != http.StatusNotFound {
				t.Errorf("claimTerminalClient(%d, %q, %t) a second time = status %d, want 404", pid, id, tc.writable, status)
			}
		})
	}
}
