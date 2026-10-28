package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/api/scorch/scorchexe"
	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/polltest"
	"phenix/util/pubsub"
	"phenix/util/testenv"
	"phenix/web/lifecycle"
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

// The terminals of a comma-separated list of experiments need experiments get
// on each experiment in the list. A wildcard name such as "ex*" also matches
// the whole list "exp,secret", so the list itself does not count.
//
//nolint:paralleltest // shares the terminal maps
func TestGetTerminalsChecksEachListedExperiment(t *testing.T) {
	fakeTerminal(t, 4444)

	wildcard := roleFor("experiments", "get")
	wildcard.Spec.Policies[0].ResourceNames = []string{"ex*"}

	both := roleFor("experiments", "get")
	both.Spec.Policies[0].ResourceNames = []string{"exp", "secret"}

	tests := map[string]struct {
		path string
		role *rbac.Role
		want int
	}{
		"one allowed experiment":           {"/experiments/exp/scorch/terminals", wildcard, http.StatusOK},
		"a list with a denied experiment":  {"/experiments/exp,secret/scorch/terminals", wildcard, http.StatusForbidden},
		"a list that starts with a denied": {"/experiments/secret,exp/scorch/terminals", wildcard, http.StatusForbidden},
		"a list of allowed experiments":    {"/experiments/exp,secret/scorch/terminals", both, http.StatusOK},
		"a list with an empty name in it":  {"/experiments/exp,/scorch/terminals", wildcard, http.StatusForbidden},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serveScorch(http.MethodGet, tc.path, tc.role)
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d %s, want %d", tc.path, rec.Code, rec.Body, tc.want)
			}

			if tc.want != http.StatusOK {
				return
			}

			var got struct {
				Terminals []WebTerm `json:"terminals"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding GET %s: %v", tc.path, err)
			}

			if len(got.Terminals) != 1 || got.Terminals[0].Exp != "exp" {
				t.Errorf("GET %s = terminals %+v, want the one terminal of exp", tc.path, got.Terminals)
			}
		})
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
			"pipelines/1/cleanup", operator, http.StatusConflict, "already executing",
		},
		"cannot clean up a run without cleanup components": {
			"pipelines/2/cleanup", operator, http.StatusBadRequest, "has no cleanup components",
		},
		"cannot clean up a run that does not exist": {
			"pipelines/3/cleanup", operator, http.StatusNotFound, "not found",
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
		"/experiments/plain/scorch/pipelines/0/cleanup": http.StatusNotFound,
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

	// the writer attaches, as StreamTerminal does, before its timeout is short
	if readOnly, status := claimTerminalClient(pid, writerID, true); readOnly || status != http.StatusOK {
		t.Fatalf("claimTerminalClient(%d, writer) = %t, %d; want false, 200", pid, readOnly, status)
	}

	original := terminalInitTimeout
	terminalInitTimeout = 20 * time.Millisecond

	t.Cleanup(func() { terminalInitTimeout = original })

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

// handlersRole is roleFor on experiment name instead of exp.
func handlersRole(name string, resourceVerbs ...string) *rbac.Role {
	role := roleFor(resourceVerbs...)

	for _, policy := range role.Spec.Policies {
		policy.ResourceNames = []string{name}
	}

	return role
}

// handlersStore stores experiment name with the given spec and status in the
// store useScorch set up.
func handlersStore(t *testing.T, name string, spec, status map[string]any) {
	t.Helper()

	c, _ := store.NewConfig("experiment/" + name)
	c.Spec, c.Status = spec, status
	c.Spec["experimentName"] = name

	if err := store.Create(c); err != nil {
		t.Fatalf("store.Create(experiment/%s) = %v", name, err)
	}
}

// handlersScorchSpec is the spec of an experiment whose SCORCH app has metadata md.
func handlersScorchSpec(md map[string]any) map[string]any {
	return map[string]any{"scenario": map[string]any{"apps": []map[string]any{{"name": "scorch", "metadata": md}}}}
}

// handlersServer serves the SCORCH routes over HTTP to a user with role, for
// websocket clients, and returns its websocket URL.
func handlersServer(t *testing.T, role *rbac.Role) string {
	t.Helper()

	router := mux.NewRouter()
	RegisterRoutes(router)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, *role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")
		router.ServeHTTP(w, r.WithContext(ctx))
	}))

	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// handlersDial opens a websocket to path on the server at base.
func handlersDial(t *testing.T, base, path string) *websocket.Conn {
	t.Helper()

	ws, err := websocket.Dial(base+path, "", "http://localhost/")
	if err != nil {
		t.Fatalf("dialing %s: %v", path, err)
	}

	t.Cleanup(func() { _ = ws.Close() })

	if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	return ws
}

// handlersRead reads from r until what it read contains want, failing the
// test at the first error.
func handlersRead(t *testing.T, r io.Reader, want string) {
	t.Helper()

	var got []byte

	buf := make([]byte, 4096)

	for !strings.Contains(string(got), want) {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)

		if err != nil && !strings.Contains(string(got), want) {
			t.Fatalf("reading until %q: got %q, then %v", want, got, err)
		}
	}
}

// handlersReadToEnd reads r until the connection ends, failing the test when it
// ends with an error other than the end of the stream.
func handlersReadToEnd(t *testing.T, r io.Reader) string {
	t.Helper()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading to the end: got %q, then %v", got, err)
	}

	return string(got)
}

// handlersTerm is a terminal of experiment exp's component cmp at stage start
// of run 0, loop 0: a pseudo-terminal, read through tty, whose PID is that of a
// sleep process the test started.
type handlersTerm struct {
	WebTerm

	tty *os.File
}

// handlersTerminal registers a handlersTerm for cmp, and ends and forgets it when the test ends.
func handlersTerminal(t *testing.T, cmp string) handlersTerm {
	t.Helper()

	testenv.RequireCommands(t, "sleep")

	ptmx, tty, err := pty.Open()
	if err != nil {
		testenv.Skipf(t, "opening a pseudo-terminal: %v", err)
	}

	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}

	term := newWebTerm("exp", 0, 0, "start", cmp)
	term.Pid, term.Pty = sleep.Process.Pid, ptmx

	webTermMu.Lock()
	webTermsPid[term.Pid] = term
	webTermsExp[term.key] = term
	webTermMu.Unlock()

	if err := tty.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("setting the pseudo-terminal's read deadline: %v", err)
	}

	t.Cleanup(func() {
		webTermMu.Lock()
		_, live := webTermsPid[term.Pid]
		delete(webTermsPid, term.Pid)
		delete(webTermsExp, term.key)
		webTermMu.Unlock()

		mu.Lock()
		delete(rwTerm, term.Pid)
		delete(roTerms, term.Pid)
		delete(history, term.Pid)
		mu.Unlock()

		_ = tty.Close()
		_ = ptmx.Close()

		// KillTerminal already reaped a terminal it ended
		if live {
			_ = sleep.Process.Kill()
			_ = sleep.Wait()
		}
	})

	return handlersTerm{WebTerm: term, tty: tty}
}

// handlersStreamPath is the path of the websocket a terminal connection's loc names.
func handlersStreamPath(term WebTerm) string {
	_, path, _ := strings.Cut(term.Loc, "/api/v1")

	return path
}

// The writer's websocket types into the terminal and gets its output; a
// read-only viewer gets the output so far, then the rest, then the exit marker.
// The writer leaving frees the terminal's read/write side.
//
//nolint:paralleltest // shares the terminal maps
func TestStreamTerminal(t *testing.T) {
	startComponents()

	var (
		term     = handlersTerminal(t, "stream-cc")
		path     = fmt.Sprintf("/experiments/exp/scorch/terminals/%d", term.Pid)
		viewer   = roleFor("experiments", "get")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	writer := connectTerminal(t, path, operator)
	if writer.RO {
		t.Fatalf("GET %s as the first operator = read-only true, want false", path)
	}

	rw := handlersDial(t, handlersServer(t, operator), handlersStreamPath(writer))

	if _, err := rw.Write([]byte("hello\n")); err != nil {
		t.Fatalf("writing to the writer's websocket: %v", err)
	}

	handlersRead(t, term.tty, "hello")

	if _, err := term.tty.WriteString("world\n"); err != nil {
		t.Fatalf("writing to the terminal: %v", err)
	}

	handlersRead(t, rw, "world")

	reader := connectTerminal(t, path, viewer)
	if !reader.RO || reader.Exit != "" {
		t.Fatalf("GET %s as a viewer = read-only %t, exit %q; want read-only, no exit", path, reader.RO, reader.Exit)
	}

	ro := handlersDial(t, handlersServer(t, viewer), handlersStreamPath(reader))

	// the output so far, then what follows
	handlersRead(t, ro, "world")

	if _, err := term.tty.WriteString("again\n"); err != nil {
		t.Fatalf("writing to the terminal: %v", err)
	}

	handlersRead(t, ro, "again")
	handlersRead(t, rw, "again")

	_ = rw.Close()

	polltest.Until(t, "the writer that left freeing the terminal", func() bool {
		_, held := writerOf(term.Pid)

		return !held
	})

	close(term.Done)

	if got := handlersReadToEnd(t, ro); !strings.HasSuffix(got, exitedMarker) {
		t.Errorf("read-only viewer after the terminal exited read %q, want it to end with %q", got, exitedMarker)
	}

	mu.Lock()
	_, viewers := roTerms[term.Pid]
	mu.Unlock()

	if viewers {
		t.Errorf("roTerms[%d] after the terminal exited is listed, want it removed", term.Pid)
	}
}

// A read-only viewer that leaves is forgotten, and the terminal goes on.
//
//nolint:paralleltest // shares the terminal maps
func TestStreamTerminalViewerLeaves(t *testing.T) {
	startComponents()

	var (
		term   = handlersTerminal(t, "leave-cc")
		path   = fmt.Sprintf("/experiments/exp/scorch/terminals/%d", term.Pid)
		viewer = roleFor("experiments", "get")
	)

	reader := connectTerminal(t, path, viewer)
	ro := handlersDial(t, handlersServer(t, viewer), handlersStreamPath(reader))

	polltest.Until(t, "the viewer attaching", func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(roTerms[term.Pid]) == 1
	})

	_ = ro.Close()

	polltest.Until(t, "the viewer that left being forgotten", func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(roTerms[term.Pid]) == 0
	})
}

// StreamTerminal refuses a terminal it does not know, one of another experiment
// and any while the server stops, before it upgrades the connection.
//
//nolint:paralleltest // shares the terminal maps and stops the server's lifecycle
func TestStreamTerminalRefusals(t *testing.T) {
	term := handlersTerminal(t, "refused-cc")
	stream := fmt.Sprintf("/scorch/terminals/%d/ws/id", term.Pid)

	tests := []struct {
		name, path string
		role       *rbac.Role
		stopping   bool
		want       int
	}{
		{"unknown PID", "/experiments/exp/scorch/terminals/1/ws/id", roleFor("experiments", "get"), false, http.StatusNotFound},
		{"PID not a number", "/experiments/exp/scorch/terminals/x/ws/id", roleFor("experiments", "get"), false, http.StatusNotFound},
		{"another experiment", "/experiments/other" + stream, handlersRole("other", "experiments", "get"), false, http.StatusNotFound},
		{"server stopping", "/experiments/exp" + stream, roleFor("experiments", "get"), true, http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stopping {
				lifecycle.Start()
				t.Cleanup(lifecycle.Start)
				lifecycle.Stop()
			}

			if rec := serveScorch(http.MethodGet, tc.path, tc.role); rec.Code != tc.want {
				t.Errorf("GET %s = %d %s, want %d", tc.path, rec.Code, rec.Body, tc.want)
			}
		})
	}
}

// The writer's client may exit its terminal, which ends it; no other client
// may, and a terminal that is gone or of another experiment is not found.
//
//nolint:paralleltest // shares the terminal maps
func TestExitTerminal(t *testing.T) {
	var (
		events   = captureAudit(t)
		term     = handlersTerminal(t, "exit-cc")
		operator = roleFor("experiments", "get", "experiments/trigger", "create")
	)

	writer := connectTerminal(t, fmt.Sprintf("/experiments/exp/scorch/terminals/%d", term.Pid), operator)
	id := writer.Exit[strings.LastIndex(writer.Exit, "/")+1:]

	const gonePID = 777001

	mu.Lock()
	rwTerm[gonePID] = "gone-writer"
	mu.Unlock()

	t.Cleanup(func() {
		mu.Lock()
		delete(rwTerm, gonePID)
		mu.Unlock()
	})

	refused := map[string]struct {
		path    string
		role    *rbac.Role
		status  int
		message string
	}{
		"not the writer": {
			fmt.Sprintf("/experiments/exp/scorch/terminals/%d/exit/someone-else", term.Pid), operator,
			http.StatusForbidden, "not allowed to exit",
		},
		"a terminal that is gone": {
			fmt.Sprintf("/experiments/exp/scorch/terminals/%d/exit/gone-writer", gonePID), operator,
			http.StatusNotFound, "web terminal not found",
		},
		"another experiment's terminal": {
			fmt.Sprintf("/experiments/other/scorch/terminals/%d/exit/%s", term.Pid, id),
			handlersRole("other", "experiments", "get", "experiments/trigger", "create"),
			http.StatusNotFound, "no web terminal found",
		},
	}

	for name, tc := range refused {
		rec := serveScorch(http.MethodPost, tc.path, tc.role)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
			t.Errorf("POST %s (%s) = %d %s, want %d with %q", tc.path, name, rec.Code, rec.Body, tc.status, tc.message)
		}
	}

	mu.Lock()
	history[term.Pid] = *bytes.NewBufferString("output")
	mu.Unlock()

	exit := fmt.Sprintf("/experiments/exp/scorch/terminals/%d/exit/%s", term.Pid, id)
	if rec := serveScorch(http.MethodPost, exit, operator); rec.Code != http.StatusNoContent {
		t.Fatalf("POST %s as the writer = %d %s, want 204", exit, rec.Code, rec.Body)
	}

	select {
	case <-term.Done:
	default:
		t.Errorf("after POST %s, the terminal is not done, want it done", exit)
	}

	if _, err := GetTerminalByPID(term.Pid); !errors.Is(err, ErrTerminalNotFound) {
		t.Errorf("GetTerminalByPID(%d) after POST %s error = %v, want %v", term.Pid, exit, err, ErrTerminalNotFound)
	}

	mu.Lock()
	_, kept := history[term.Pid]
	mu.Unlock()

	if kept {
		t.Errorf("after POST %s, history[%d] is kept, want it dropped", exit, term.Pid)
	}

	wantAudit(t, events(), "scorch-terminal-exit", "success", map[string]any{"process.pid": term.Pid})
	wantAudit(t, events(), "scorch-terminal-exit", "failure", map[string]any{"process.pid": term.Pid, "event.reason": "not writer"})
}

// Connecting to a terminal refuses run and loop numbers that are not numbers,
// and finds no terminal for a component without one or a PID whose component
// has none.
//
//nolint:paralleltest // shares the terminal maps
func TestConnectTerminalRefusals(t *testing.T) {
	term := handlersTerminal(t, "pid-only")

	// the terminal of the PID no longer has its component's
	webTermMu.Lock()
	delete(webTermsExp, term.key)
	webTermMu.Unlock()

	viewer := roleFor("experiments", "get")

	for path, want := range map[string]struct {
		status  int
		message string
	}{
		"/experiments/exp/scorch/terminals/x/0/start/cc":              {http.StatusBadRequest, "invalid run ID"},
		"/experiments/exp/scorch/terminals/0/x/start/cc":              {http.StatusBadRequest, "invalid loop number"},
		"/experiments/exp/scorch/terminals/0/0/start/no-terminal":     {http.StatusNotFound, "no web terminal found"},
		fmt.Sprintf("/experiments/exp/scorch/terminals/%d", term.Pid): {http.StatusNotFound, "no web terminal found"},
	} {
		if rec := serveScorch(http.MethodGet, path, viewer); rec.Code != want.status || !strings.Contains(rec.Body.String(), want.message) {
			t.Errorf("GET %s = %d %s, want %d with %q", path, rec.Code, rec.Body, want.status, want.message)
		}
	}

	// a component's entry naming another experiment's terminal is not found
	other := newWebTerm("other", 0, 0, "start", "cc")
	other.key = "exp|0|0|start|mismatched"

	webTermMu.Lock()
	webTermsExp[other.key] = other
	webTermMu.Unlock()

	t.Cleanup(func() {
		webTermMu.Lock()
		delete(webTermsExp, other.key)
		webTermMu.Unlock()
	})

	if _, err := initTerminal("exp", 0, 0, "start", "mismatched", true); err == nil {
		t.Error("initTerminal(exp, mismatched) of another experiment's terminal error = nil, want one")
	}
}

// handlersComponentPath is the output route of experiment exp's component cmp at stage start of run 0, loop 0.
func handlersComponentPath(cmp string) string {
	return "/experiments/exp/scorch/components/0/0/start/" + cmp
}

// handlersFinish marks cmp finished when the test ends, so no output stays running.
func handlersFinish(t *testing.T, cmp string) {
	t.Helper()

	t.Cleanup(func() { UpdateComponent(update(cmp, statusSuccess, "")) })
}

// A running component's output is a stream location, or its terminal for a
// break; a finished one's is its output. Run and loop must be numbers.
//
//nolint:paralleltest // uses the package's component state and terminal maps
func TestGetComponentOutput(t *testing.T) {
	useScorch(t)

	operator := roleFor("experiments", "get", "experiments/trigger", "create")

	UpdateComponent(update("output-done", statusStart, ""))
	UpdateComponent(update("output-done", statusRunning, "one\n"))
	UpdateComponent(update("output-done", statusBackground, "two\n"))
	UpdateComponent(update("output-done", statusFailure, ""))

	UpdateComponent(update("output-running", statusStart, ""))
	UpdateComponent(update("output-running", statusRunning, "one\n"))
	handlersFinish(t, "output-running")

	for _, cmp := range []string{"output-break", "output-break-no-term"} {
		brk := update(cmp, statusStart, "")
		brk.CmpType = componentTypeBreak
		UpdateComponent(brk)
		UpdateComponent(update(cmp, statusRunning, ""))
		handlersFinish(t, cmp)
	}

	// the break's terminal, known only by its component, so nothing ends its PID
	brk := newWebTerm("exp", 0, 0, "start", "output-break")
	brk.Pid = 777002

	webTermMu.Lock()
	webTermsExp[brk.key] = brk
	webTermMu.Unlock()

	t.Cleanup(func() {
		webTermMu.Lock()
		delete(webTermsExp, brk.key)
		webTermMu.Unlock()

		mu.Lock()
		delete(rwTerm, brk.Pid)
		mu.Unlock()
	})

	tests := map[string]struct {
		path   string
		status int
		want   map[string]any
	}{
		"finished": {handlersComponentPath("output-done"), http.StatusOK, map[string]any{"output": "one\ntwo\n"}},
		"running": {handlersComponentPath("output-running"), http.StatusOK, map[string]any{
			"stream": "/api/v1/experiments/exp/scorch/components/0/0/start/output-running/ws",
		}},
		"a break": {handlersComponentPath("output-break"), http.StatusOK, map[string]any{"terminal": map[string]any{
			"exp": "exp", "run": 0.0, "loop": 0.0, "stage": "start", "name": "output-break", "readOnly": false,
		}}},
		"break without a terminal": {handlersComponentPath("output-break-no-term"), http.StatusNotFound, nil},
		"run not a number":         {"/experiments/exp/scorch/components/x/0/start/output-done", http.StatusBadRequest, nil},
		"loop not a number":        {"/experiments/exp/scorch/components/0/x/start/output-done", http.StatusBadRequest, nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serveScorch(http.MethodGet, tc.path, operator)
			if rec.Code != tc.status {
				t.Fatalf("GET %s = %d %s, want %d", tc.path, rec.Code, rec.Body, tc.status)
			}

			if tc.want == nil {
				return
			}

			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding GET %s: %v", tc.path, err)
			}

			// a terminal's locations hold a new client ID each time
			if term, ok := got["terminal"].(map[string]any); ok {
				if loc, exit := fmt.Sprint(term["loc"]), fmt.Sprint(term["exit"]); !strings.Contains(loc, "/terminals/777002/ws/") ||
					!strings.Contains(exit, "/terminals/777002/exit/") {
					t.Errorf("GET %s terminal locations = %q, %q; want its stream and exit", tc.path, loc, exit)
				}

				delete(term, "loc")
				delete(term, "exit")
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GET %s mismatch (-want +got):\n%s", tc.path, diff)
			}
		})
	}
}

// A running component's output streams to a websocket until it finishes; a
// finished component's streams at once, each ending with the finished marker.
//
//nolint:paralleltest // uses the package's component state
func TestStreamComponentOutput(t *testing.T) {
	startComponents()

	base := handlersServer(t, roleFor("experiments", "get"))

	UpdateComponent(update("stream-running", statusStart, ""))
	UpdateComponent(update("stream-running", statusRunning, "one\n"))
	handlersFinish(t, "stream-running")

	running := handlersDial(t, base, handlersComponentPath("stream-running")+"/ws")

	handlersRead(t, running, "one\n")
	UpdateComponent(update("stream-running", statusRunning, "two\n"))
	UpdateComponent(update("stream-running", statusSuccess, ""))

	if got := handlersReadToEnd(t, running); !strings.HasSuffix(got, "two\n"+finishedMarker) {
		t.Errorf("stream of a running component read %q, want two and then %q", got, finishedMarker)
	}

	finished := handlersDial(t, base, handlersComponentPath("stream-running")+"/ws")

	if got, want := handlersReadToEnd(t, finished), "one\ntwo\n"+finishedMarker; got != want {
		t.Errorf("stream of a finished component read %q, want %q", got, want)
	}
}

// StreamComponentOutput refuses run and loop numbers that are not numbers, and
// any stream while the server stops.
//
//nolint:paralleltest // stops the server's lifecycle
func TestStreamComponentOutputRefusals(t *testing.T) {
	viewer := roleFor("experiments", "get")

	for _, tc := range []struct {
		name, path string
		stopping   bool
		want       int
	}{
		{"run not a number", "/experiments/exp/scorch/components/x/0/start/cc/ws", false, http.StatusBadRequest},
		{"loop not a number", "/experiments/exp/scorch/components/0/x/start/cc/ws", false, http.StatusBadRequest},
		{"server stopping", "/experiments/exp/scorch/components/0/0/start/cc/ws", true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stopping {
				lifecycle.Start()
				t.Cleanup(lifecycle.Start)
				lifecycle.Stop()
			}

			if rec := serveScorch(http.MethodGet, tc.path, viewer); rec.Code != tc.want {
				t.Errorf("GET %s = %d %s, want %d", tc.path, rec.Code, rec.Body, tc.want)
			}
		})
	}
}

// handlersPublications subscribes to the trigger-app publications until the test ends.
func handlersPublications(t *testing.T) chan any {
	t.Helper()

	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	return ch
}

// handlersNextPublication returns the next trigger-app publication, or marks the
// test failed and returns none if none comes soon.
func handlersNextPublication(t *testing.T, ch chan any) app.TriggerPublication {
	t.Helper()

	select {
	case msg := <-ch:
		pub, _ := msg.(app.TriggerPublication)

		return pub
	case <-time.After(5 * time.Second):
		t.Error("no trigger-app publication within 5s")
	}

	return app.TriggerPublication{}
}

// Starting a run or its cleanup answers at once and publishes the run's start,
// then its failure, here because the test binary has no SCORCH app to run.
//
//nolint:paralleltest // starts the SCORCH processors, replaces the store and subscribes to publications
func TestStartPipeline(t *testing.T) {
	useScorch(t)

	run := map[string]any{"start": []string{"a"}, "cleanup": []string{"c"}}
	handlersStore(t, "ready", handlersScorchSpec(map[string]any{"runs": []map[string]any{run}}),
		map[string]any{"startTime": "2026-01-01T00:00:00Z"})
	t.Cleanup(func() { DeletePipeline("ready", -1, -1, false) })

	var (
		pubs     = handlersPublications(t)
		events   = captureAudit(t)
		operator = handlersRole("ready", "experiments", "get", "experiments/trigger", "create")
	)

	for _, path := range []string{"/experiments/ready/scorch/pipelines/0", "/experiments/ready/scorch/pipelines/0/cleanup"} {
		if rec := serveScorch(http.MethodPost, path, operator); rec.Code != http.StatusNoContent {
			t.Fatalf("POST %s = %d %s, want 204", path, rec.Code, rec.Body)
		}

		started := handlersNextPublication(t, pubs)
		want := app.TriggerPublication{Experiment: "ready", App: "scorch", Resource: "ready/0", State: "start"}

		if diff := cmp.Diff(want, started, cmpopts.EquateErrors()); diff != "" {
			t.Errorf("POST %s first publication mismatch (-want +got):\n%s", path, diff)
		}

		failed := handlersNextPublication(t, pubs)
		if failed.State != "error" || !errors.Is(failed.Error, app.ErrUserAppNotFound) {
			t.Errorf("POST %s second publication = %+v, want an error that the app is not found", path, failed)
		}

		polltest.Until(t, "the run's canceler being dropped", func() bool { return !scorchexe.HasCanceler("ready", 0) })
	}

	wantAudit(t, events(), "scorch-start", "success", map[string]any{"phenix.scorch.run": 0, "phenix.scorch.cleanup_only": false})
	wantAudit(t, events(), "scorch-start", "success", map[string]any{"phenix.scorch.run": 0, "phenix.scorch.cleanup_only": true})
}

// Canceling a run that executes cancels it and publishes that; canceling one
// that does not succeeds and changes nothing.
//
//nolint:paralleltest // starts the SCORCH processors and subscribes to publications
func TestCancelPipeline(t *testing.T) {
	useScorch(t)

	var (
		pubs    = handlersPublications(t)
		trigger = roleFor("experiments/trigger", "delete")
		ctx     = scorchexe.AddCanceler(context.Background(), "exp", 5)
	)

	t.Cleanup(func() {
		if cancel := scorchexe.GetCanceler("exp", 5); cancel != nil {
			cancel()
		}
	})

	const path = "/experiments/exp/scorch/pipelines/5"

	// the handler publishes before it answers
	published := make(chan app.TriggerPublication, 1)

	go func() { published <- handlersNextPublication(t, pubs) }()

	if rec := serveScorch(http.MethodDelete, path, trigger); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d %s, want 204", path, rec.Code, rec.Body)
	}

	if ctx.Err() == nil || scorchexe.HasCanceler("exp", 5) {
		t.Errorf("after DELETE %s, run context error = %v and canceler kept %t; want canceled and dropped",
			path, ctx.Err(), scorchexe.HasCanceler("exp", 5))
	}

	want := app.TriggerPublication{Experiment: "exp", Verb: "delete", App: "scorch", Resource: "exp/5", State: "success"}
	if diff := cmp.Diff(want, <-published, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("DELETE %s publication mismatch (-want +got):\n%s", path, diff)
	}

	for name, tc := range map[string]struct {
		path string
		role *rbac.Role
		want int
	}{
		"run not a number":         {"/experiments/exp/scorch/pipelines/x", trigger, http.StatusBadRequest},
		"without delete rights":    {path, roleFor("experiments/trigger", "create"), http.StatusForbidden},
		"a run that does not exec": {path, trigger, http.StatusNoContent},
	} {
		if rec := serveScorch(http.MethodDelete, tc.path, tc.role); rec.Code != tc.want {
			t.Errorf("DELETE %s (%s) = %d %s, want %d", tc.path, name, rec.Code, rec.Body, tc.want)
		}
	}
}

// The pipeline, clear and start routes refuse a run or loop that is not a
// number, a role without the rights they need, an experiment that is not in
// the store and SCORCH metadata that does not decode.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestPipelineRefusals(t *testing.T) {
	useScorch(t, map[string]any{"start": []string{"a"}})

	handlersStore(t, "bad", handlersScorchSpec(map[string]any{"runs": "not a list"}), nil)
	handlersStore(t, "plain", map[string]any{}, nil)

	var (
		all     = handlersRole("*", "experiments", "get", "experiments/trigger", "create")
		trigger = handlersRole("*", "experiments/trigger", "create")
		lister  = roleFor("experiments", "list")
	)

	tests := map[string]struct {
		method, path string
		role         *rbac.Role
		status       int
		message      string
	}{
		"pipelines of a missing experiment": {
			http.MethodGet, "/experiments/missing/scorch/pipelines", all, http.StatusNotFound, "experiment missing not found",
		},
		"pipelines without SCORCH": {
			http.MethodGet, "/experiments/plain/scorch/pipelines", all, http.StatusInternalServerError, "unable to decode",
		},
		"pipelines with bad metadata": {
			http.MethodGet, "/experiments/bad/scorch/pipelines", all, http.StatusInternalServerError, "unable to decode",
		},
		"pipeline with a loop that is not a number": {
			http.MethodGet, "/experiments/exp/scorch/pipelines/0/x", all, http.StatusBadRequest, "invalid loop number",
		},
		"pipeline without get": {
			http.MethodGet, "/experiments/exp/scorch/pipelines/0/0", lister, http.StatusForbidden, "not allowed",
		},
		"clear a run that is not a number": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/x/clear", all, http.StatusBadRequest, "invalid run ID",
		},
		"clear in a missing experiment": {
			http.MethodPost, "/experiments/missing/scorch/pipelines/0/clear", all, http.StatusNotFound, "experiment missing not found",
		},
		"clear with bad metadata": {
			http.MethodPost, "/experiments/bad/scorch/pipelines/0/clear", all, http.StatusInternalServerError, "unable to decode",
		},
		"start a run that is not a number": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/x", all, http.StatusBadRequest, "invalid run ID",
		},
		"start without get": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/0", trigger, http.StatusForbidden, "getting experiment exp not allowed",
		},
		"start in a missing experiment": {
			http.MethodPost, "/experiments/missing/scorch/pipelines/0", all, http.StatusNotFound, "experiment missing not found",
		},
		"clean up with bad metadata": {
			http.MethodPost, "/experiments/bad/scorch/pipelines/0/cleanup", all, http.StatusInternalServerError, "unable to decode",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serveScorch(tc.method, tc.path, tc.role)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Errorf("%s %s = %d %s, want %d with %q", tc.method, tc.path, rec.Code, rec.Body, tc.status, tc.message)
			}
		})
	}
}

// An experiment whose SCORCH app is not running lists its pipelines with no
// running run.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestGetPipelinesOfAnIdleApp(t *testing.T) {
	useScorch(t)

	handlersStore(t, "idle", handlersScorchSpec(map[string]any{"runs": []map[string]any{{"name": "only"}}}), nil)
	t.Cleanup(func() { DeletePipeline("idle", -1, -1, false) })

	const path = "/experiments/idle/scorch/pipelines"

	rec := serveScorch(http.MethodGet, path, handlersRole("idle", "experiments", "get"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, rec.Code, rec.Body)
	}

	var got struct {
		Pipelines []struct {
			Name string `json:"name"`
		} `json:"pipelines"`
		Running    int            `json:"running"`
		AppRunning bool           `json:"app_running"`
		Experiment map[string]any `json:"experiment"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding GET %s: %v", path, err)
	}

	if len(got.Pipelines) != 1 || got.Pipelines[0].Name != "only" || got.Running != -1 || got.AppRunning ||
		got.Experiment["status"] != "stopped" {
		t.Errorf("GET %s = %+v, want run only, no running run, the app and experiment stopped", path, got)
	}
}

// handlersWriter is a terminal client that takes at most limit bytes of each
// write, or fails every write when err is set.
type handlersWriter struct {
	limit  int
	err    error
	writes int
}

func (w *handlersWriter) Write(b []byte) (int, error) {
	w.writes++

	if w.err != nil {
		return 0, w.err
	}

	return min(len(b), w.limit), nil
}

// readTerm stops at the first write its client fails or takes only part of,
// keeping no history of that output.
//
//nolint:paralleltest // uses the package's terminal state
func TestReadTermStopsWhenTheClientFails(t *testing.T) {
	for name, client := range map[string]*handlersWriter{
		"failed write": {err: errors.New("broken pipe")},
		"short write":  {limit: 1},
	} {
		t.Run(name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

			term := WebTerm{Pid: -3, Pty: r, Done: make(chan struct{})}

			t.Cleanup(func() {
				mu.Lock()
				delete(history, term.Pid)
				mu.Unlock()
			})

			if _, err := w.WriteString("output"); err != nil {
				t.Fatal(err)
			}

			polltest.Within(t, "readTerm returning", func() { readTerm(term, client) })

			mu.Lock()
			_, kept := history[term.Pid]
			mu.Unlock()

			if client.writes != 1 || kept {
				t.Errorf("readTerm with a %s: writes = %d, history kept %t; want 1, false", name, client.writes, kept)
			}
		})
	}
}

// writeTerm copies its client's input to the terminal until the input ends, or
// until the terminal cannot be written.
func TestWriteTerm(t *testing.T) {
	t.Parallel()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.Close() })

	polltest.Within(t, "writeTerm copying its input", func() {
		writeTerm(WebTerm{Pty: w}, strings.NewReader("ls -l\n"))
	})

	_ = w.Close()

	got, err := io.ReadAll(r)
	if err != nil || string(got) != "ls -l\n" {
		t.Errorf("terminal after writeTerm(ls -l) read %q, %v; want %q", got, err, "ls -l\n")
	}

	// the terminal closed: writeTerm stops after the first chunk it cannot pass on
	input := strings.NewReader(strings.Repeat("x", 2*TerminalBufferSize))

	polltest.Within(t, "writeTerm to a closed terminal", func() { writeTerm(WebTerm{Pty: w}, input) })

	if input.Len() != TerminalBufferSize {
		t.Errorf("writeTerm to a closed terminal left %d bytes unread, want %d (one chunk read)",
			input.Len(), TerminalBufferSize)
	}
}
