package scorch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// testRequest returns a request with route variables and a role built from
// the given policies.
func testRequest(method string, vars map[string]string, policies ...*v1.PolicySpec) *http.Request {
	role := rbac.Role{Spec: &v1.RoleSpec{Policies: policies}}
	ctx := context.WithValue(context.Background(), middleware.ContextKeyRole, role)
	req := httptest.NewRequestWithContext(ctx, method, "/", nil)

	return mux.SetURLVars(req, vars)
}

func experimentPolicy(names ...string) *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     []string{"experiments"},
		ResourceNames: names,
		Verbs:         []string{"get"},
	}
}

func scorchPolicy(verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{appNameScorch}, ResourceNames: nil, Verbs: verbs}
}

// terminalWritePolicy grants typing into Scorch terminals.
func terminalWritePolicy() *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{"scorch/terminals"}, ResourceNames: nil, Verbs: []string{"write"}}
}

// TestCanWriteTerminal verifies only scorch/terminals write, not any scorch
// verb or read-only wildcards, allows typing into Scorch terminals.
func TestCanWriteTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{
			name: "scorch/terminals write",
			req:  testRequest(http.MethodGet, nil, scorchPolicy("get"), terminalWritePolicy()),
			want: true,
		},
		{
			name: "all scorch verbs",
			req:  testRequest(http.MethodGet, nil, scorchPolicy("*")),
			want: false,
		},
		{
			name: "read everything",
			req: testRequest(http.MethodGet, nil, &v1.PolicySpec{
				Resources:     []string{"*", "*/*"},
				ResourceNames: []string{"*", "*/*"},
				Verbs:         []string{"list", "get"},
			}),
			want: false,
		},
		{
			name: "everything",
			req: testRequest(http.MethodGet, nil, &v1.PolicySpec{
				Resources:     []string{"*", "*/*"},
				ResourceNames: []string{"*", "*/*"},
				Verbs:         []string{"*"},
			}),
			want: true,
		},
		{name: "scorch get only", req: testRequest(http.MethodGet, nil, scorchPolicy("get")), want: false},
		{name: "missing role", req: httptest.NewRequest(http.MethodGet, "/", nil), want: false},
	}

	for _, test := range tests {
		if got := canWriteTerminal(test.req); got != test.want {
			t.Errorf("%s: got %t, want %t", test.name, got, test.want)
		}
	}
}

// TestScorchHandlersRejectUnreadableExperiment verifies Scorch service access
// does not expose experiments outside the user's experiment scope.
func TestScorchHandlersRejectUnreadableExperiment(t *testing.T) {
	t.Parallel()

	vars := map[string]string{
		"name":  "denied",
		"run":   "0",
		"loop":  "0",
		"stage": "stage",
		"cmp":   "cmp",
		"pid":   "1",
		"id":    "id",
	}

	handlers := map[string]http.HandlerFunc{
		"GetTerminals":          GetTerminals,
		"ConnectTerminal":       ConnectTerminal,
		"StreamTerminal":        StreamTerminal,
		"ExitTerminal":          ExitTerminal,
		"StreamComponentOutput": StreamComponentOutput,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler(rec, testRequest(http.MethodGet, vars, experimentPolicy("allowed"), scorchPolicy("*")))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}

	t.Run("GetComponentOutput", func(t *testing.T) {
		t.Parallel()

		err := GetComponentOutput(
			httptest.NewRecorder(),
			testRequest(http.MethodGet, vars, experimentPolicy("allowed"), scorchPolicy("*")),
		)

		var webErr *weberror.WebError
		if !errors.As(err, &webErr) || webErr.Status != http.StatusForbidden {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestClaimTerminalClient(t *testing.T) {
	tests := []struct {
		name       string
		owner      bool
		writable   bool
		wantWriter bool
	}{
		{name: "owner with write permission", owner: true, writable: true, wantWriter: true},
		{name: "owner without write permission", owner: true, writable: false, wantWriter: false},
		{name: "reader with write permission", owner: false, writable: true, wantWriter: false},
		{name: "reader without write permission", owner: false, writable: false, wantWriter: false},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var (
				pid  = 1000 + i
				id   = fmt.Sprintf("client-%d", i)
				done = make(chan struct{})
			)

			mu.Lock()

			termClientIDs[id] = done
			rwTerm[pid] = "other-client"

			if test.owner {
				rwTerm[pid] = id
			}

			mu.Unlock()

			t.Cleanup(func() {
				mu.Lock()
				delete(rwTerm, pid)
				delete(termClientIDs, id)
				mu.Unlock()
			})

			writer, ok := claimTerminalClient(pid, id, test.writable)
			if !ok {
				t.Fatal("issued client ID was rejected")
			}

			if writer != test.wantWriter {
				t.Fatalf("unexpected write access: got %t, want %t", writer, test.wantWriter)
			}

			select {
			case <-done:
			default:
				t.Fatal("client done channel was not closed")
			}

			mu.Lock()
			claim := rwTerm[pid]
			_, pending := termClientIDs[id]
			mu.Unlock()

			switch {
			case !test.owner && claim != "other-client":
				t.Fatalf("another client's write claim changed to %q", claim)
			case test.owner && test.writable && claim != id:
				t.Fatalf("writer lost its write claim: %q", claim)
			case test.owner && !test.writable && claim != "":
				t.Fatalf("write claim kept without write permission: %q", claim)
			}

			if pending {
				t.Fatal("client ID was not consumed")
			}

			if _, ok := claimTerminalClient(pid, id, test.writable); ok {
				t.Fatal("reused client ID was accepted")
			}
		})
	}
}

func TestStreamTerminalRejectsUnknownClientID(t *testing.T) {
	term := newWebTerm("allowed", 0, 0, "stage", "cmp")
	term.Pid = 3000

	webTermMu.Lock()
	webTermsPid[term.Pid] = term
	webTermMu.Unlock()

	t.Cleanup(func() {
		webTermMu.Lock()
		delete(webTermsPid, term.Pid)
		webTermMu.Unlock()
	})

	vars := map[string]string{"name": "allowed", "pid": strconv.Itoa(term.Pid), "id": "unknown"}
	rec := httptest.NewRecorder()

	StreamTerminal(rec, testRequest(http.MethodGet, vars, experimentPolicy("allowed"), scorchPolicy("get")))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestPipelineControlScopedByExperimentRead verifies starting and canceling
// Scorch runs need read access to the experiment but not experiments/trigger;
// the routes separately require scorch post or delete.
func TestPipelineControlScopedByExperimentRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		exp     string
		want    int
	}{
		{name: "start out of scope", handler: StartPipeline, method: http.MethodPost, exp: "denied", want: http.StatusForbidden},
		{name: "cancel out of scope", handler: CancelPipeline, method: http.MethodDelete, exp: "denied", want: http.StatusForbidden},
		{
			name:    "cancel in scope without trigger",
			handler: CancelPipeline,
			method:  http.MethodDelete,
			exp:     "allowed",
			want:    http.StatusNoContent,
		},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		vars := map[string]string{"name": test.exp, "run": "0"}
		req := testRequest(test.method, vars, experimentPolicy("allowed"), scorchPolicy("*"))

		err := test.handler(rec, req)
		got := rec.Code

		var webErr *weberror.WebError
		if errors.As(err, &webErr) {
			got = webErr.Status
		} else if err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}

		if got != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, got, test.want)
		}
	}
}

func TestExitTerminalRequiresWriteClaim(t *testing.T) {
	const pid = 4000

	mu.Lock()
	rwTerm[pid] = "owner"
	mu.Unlock()

	t.Cleanup(func() {
		mu.Lock()
		delete(rwTerm, pid)
		mu.Unlock()
	})

	vars := map[string]string{"name": "allowed", "pid": strconv.Itoa(pid), "id": "intruder"}
	rec := httptest.NewRecorder()

	ExitTerminal(rec, testRequest(http.MethodPost, vars, experimentPolicy("allowed"), scorchPolicy("post")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}
