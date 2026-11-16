package scorch

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"

	"phenix/util/plog"
	"phenix/web/rbac"
)

// auditWriter keeps the audit events written to it, decoded.
type auditWriter struct {
	mu     sync.Mutex
	events []map[string]any
}

func (w *auditWriter) Write(p []byte) (int, error) {
	var e map[string]any
	if err := json.Unmarshal(p, &e); err == nil && e["type"] == string(plog.TypeAudit) {
		w.mu.Lock()
		w.events = append(w.events, e)
		w.mu.Unlock()
	}

	return len(p), nil
}

// captureAudit records the audit events logged during the test; the
// returned function lists those logged so far.
func captureAudit(t *testing.T) func() []map[string]any {
	t.Helper()

	w := new(auditWriter)

	plog.AddHandler(t.Name(), slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	return func() []map[string]any {
		w.mu.Lock()
		defer w.mu.Unlock()

		return slices.Clone(w.events)
	}
}

// wantAudit fails the test unless one of events is of the action and
// outcome with each of fields.
func wantAudit(t *testing.T, events []map[string]any, action, outcome string, fields map[string]any) {
	t.Helper()

	for _, e := range events {
		if e["event.action"] != action || e["event.outcome"] != outcome {
			continue
		}

		matched := true

		for k, v := range fields {
			if fmt.Sprint(e[k]) != fmt.Sprint(v) {
				matched = false
			}
		}

		if matched {
			return
		}
	}

	t.Errorf("no %s %s audit event with %v among %v", action, outcome, fields, events)
}

// Starting, canceling and clearing SCORCH runs, and exiting their terminals,
// audit their refusals and successes with the run.
//
//nolint:paralleltest // starts the SCORCH processors
func TestRunActionsAreAudited(t *testing.T) {
	useScorch(t)

	var (
		events  = captureAudit(t)
		viewer  = roleFor("experiments", "get")
		trigger = roleFor("experiments/trigger", "delete")
	)

	requests := []struct {
		method, path string
		role         *rbac.Role
		want         int
	}{
		{http.MethodPost, "/experiments/exp/scorch/pipelines/0", viewer, http.StatusForbidden},
		{http.MethodPost, "/experiments/exp/scorch/pipelines/0/clear", viewer, http.StatusForbidden},
		{http.MethodPost, "/experiments/exp/scorch/terminals/7/exit/x", viewer, http.StatusForbidden},
		{http.MethodDelete, "/experiments/exp/scorch/pipelines/2", trigger, http.StatusNoContent},
	}

	for _, req := range requests {
		if rec := serveScorch(req.method, req.path, req.role); rec.Code != req.want {
			t.Errorf("%s %s = %d, want %d", req.method, req.path, rec.Code, req.want)
		}
	}

	got := events()

	wantAudit(t, got, "scorch-start", "failure", map[string]any{
		"phenix.experiment.name": "exp", "phenix.scorch.run": 0, "phenix.scorch.cleanup_only": false,
		"event.reason": "forbidden", "event.category": "process",
	})
	wantAudit(t, got, "scorch-clear", "failure", map[string]any{"phenix.scorch.run": 0, "event.reason": "forbidden"})
	wantAudit(t, got, "scorch-terminal-exit", "failure", map[string]any{"process.pid": 7, "event.reason": "forbidden"})
	wantAudit(t, got, "scorch-cancel", "success", map[string]any{
		"user.name": "test-user", "phenix.scorch.run": 2, "phenix.scorch.running": false,
	})
}
