package scorch

import (
	"context"
	"net/http/httptest"
	"path"
	"testing"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

func TestCanReadExperiment(t *testing.T) {
	t.Parallel()

	role := rbac.Role{Spec: &v1.RoleSpec{
		Policies: []*v1.PolicySpec{{
			Resources:     []string{"experiments"},
			ResourceNames: []string{"allowed"},
			Verbs:         []string{"get"},
		}},
	}}
	ctx := context.WithValue(context.Background(), middleware.ContextKeyRole, role)
	req := httptest.NewRequestWithContext(ctx, "GET", "/scorch", nil)

	if !canReadExperiment(req, "allowed") {
		t.Fatal("allowed experiment was rejected")
	}

	if canReadExperiment(req, "denied") {
		t.Fatal("denied experiment was allowed")
	}
}

func TestInitTerminalRespectsWritePermission(t *testing.T) {
	tests := []struct {
		name     string
		writable bool
		wantRO   bool
	}{
		{name: "read only", writable: false, wantRO: true},
		{name: "read write", writable: true, wantRO: false},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			term := newWebTerm("exp", i, 0, "stage", "component")
			term.Pid = i + 1

			webTermMu.Lock()
			webTermsExp[term.key] = term
			webTermMu.Unlock()

			t.Cleanup(func() {
				webTermMu.Lock()
				delete(webTermsExp, term.key)
				webTermMu.Unlock()

				mu.Lock()
				delete(rwTerm, term.Pid)
				mu.Unlock()
			})

			got, err := initTerminal(
				term.Exp,
				term.Run,
				term.Loop,
				term.Stage,
				term.Name,
				test.writable,
			)
			if err != nil {
				t.Fatalf("initializing terminal: %v", err)
			}

			if got.RO != test.wantRO {
				t.Fatalf("unexpected read-only state: got %t, want %t", got.RO, test.wantRO)
			}

			id := path.Base(got.Loc)

			mu.Lock()
			owner := rwTerm[term.Pid]
			done := termClientIDs[id]
			mu.Unlock()

			if test.writable && owner != id {
				t.Fatalf("unexpected terminal owner: got %q, want %q", owner, id)
			}

			if !test.writable && owner != "" {
				t.Fatalf("read-only terminal claimed write ownership: %q", owner)
			}

			if test.writable && got.Exit == "" {
				t.Fatal("writable terminal is missing exit endpoint")
			}

			if !test.writable && got.Exit != "" {
				t.Fatalf("read-only terminal has exit endpoint: %q", got.Exit)
			}

			close(done)
		})
	}
}
