package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// holdForwardsMinimega points the real mm package at fake minimega and holds its shared connection with a "hold"
// command until the test ends, so every other command waits; it returns the other commands minimega received.
func holdForwardsMinimega(t *testing.T) func() []string {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	release := make(chan struct{})

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-release
		}

		return nil
	})

	held := make(chan error, 1)

	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() { <-held })
	t.Cleanup(sync.OnceFunc(func() { close(release) }))

	mmtest.Await(t, "hold")

	return func() []string {
		return slices.DeleteFunc(mmtest.Bases(received()), func(base string) bool { return base == "hold" })
	}
}

// forwardChanges are the requests that create and close user u's forward to VM vm of experiment exp.
func forwardChanges() map[string]struct {
	handler weberror.ErrorHandler
	method  string
	target  string
} {
	const path = "/experiments/exp/vms/vm/forwards"

	return map[string]struct {
		handler weberror.ErrorHandler
		method  string
		target  string
	}{
		"create": {handler: CreatePortForward, method: http.MethodPost, target: path + "?src=2222&dst=22"},
		"delete": {handler: DeletePortForward, method: http.MethodDelete, target: path + "?dst=22"},
	}
}

// serveForwardChange serves the request as user u, who may change every forward, in ctx, and returns its answer
// once the handler returns, failing the test if that takes over 5 seconds.
func serveForwardChange(
	ctx context.Context, t *testing.T, handler weberror.ErrorHandler, method, target string,
) *httptest.ResponseRecorder {
	t.Helper()

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms/forwards"}, ResourceNames: []string{"*/*"}, Verbs: []string{"create", "delete"}},
	}}}

	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "u")

	req := httptest.NewRequestWithContext(ctx, method, target, nil)
	req = mux.SetURLVars(req, map[string]string{"exp": "exp", "name": "vm"})

	var (
		rec  = httptest.NewRecorder()
		done = make(chan struct{})
	)

	go func() {
		defer close(done)

		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s %s kept waiting for a stuck minimega", method, target)
	}

	return rec
}

// Creating or closing a forward gives up at its deadline while minimega is stuck, answering 504 rather than holding
// its handler or saying the VM is missing, and sends nothing.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestForwardChangesGiveUpAtTheirDeadline(t *testing.T) {
	for name, tc := range forwardChanges() {
		t.Run(name, func(t *testing.T) {
			useForwards(t, []string{"exp"}, nil)

			received := holdForwardsMinimega(t)

			original := changeTimeout
			changeTimeout = 50 * time.Millisecond

			t.Cleanup(func() { changeTimeout = original })

			start := time.Now()
			rec := serveForwardChange(t.Context(), t, tc.handler, tc.method, tc.target)

			if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
				t.Errorf("%s %s answered after %s, before its deadline", tc.method, tc.target, elapsed)
			}

			if rec.Code != http.StatusGatewayTimeout {
				t.Errorf("%s %s with minimega stuck = %d %q, want 504", tc.method, tc.target, rec.Code, rec.Body)
			}

			if diff := cmp.Diff([]string(nil), received(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s %s minimega commands mismatch (-want +got):\n%s", tc.method, tc.target, diff)
			}
		})
	}
}

// Creating or closing a forward whose client left gives up and writes nothing.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestForwardChangesGiveUpWhenTheClientLeaves(t *testing.T) {
	for name, tc := range forwardChanges() {
		t.Run(name, func(t *testing.T) {
			useForwards(t, []string{"exp"}, nil)

			received := holdForwardsMinimega(t)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if rec := serveForwardChange(ctx, t, tc.handler, tc.method, tc.target); rec.Body.Len() != 0 {
				t.Errorf("%s %s wrote %q (status %d) to a client that left, want nothing",
					tc.method, tc.target, rec.Body.String(), rec.Code)
			}

			if diff := cmp.Diff([]string(nil), received(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s %s minimega commands mismatch (-want +got):\n%s", tc.method, tc.target, diff)
			}
		})
	}
}
