package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	v1 "phenix/types/version/v1"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// syncBuffer is a buffer the logger may write while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// capture records the audit records logged during the test, as JSON lines.
func capture(t *testing.T) func() []string {
	t.Helper()

	var buf syncBuffer

	plog.AddHandler(t.Name(), slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	return func() []string {
		var lines []string

		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if strings.Contains(line, `"type":"AUDIT"`) {
				lines = append(lines, line)
			}
		}

		return lines
	}
}

func request(role rbac.Role) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/x?token=secret", nil)
	r.RemoteAddr = "192.0.2.10:4242"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")

	ctx := context.WithValue(r.Context(), middleware.ContextKeyUser, "alice")
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)

	return r.WithContext(ctx)
}

func TestEvents(t *testing.T) {
	var (
		iface = 2
		admin = rbac.Role{Spec: &v1.RoleSpec{Name: "Experiment Admin", Policies: nil}}
		f     = audit.Fields{Experiment: "exp1", VM: "vm1", Interface: &iface, File: "/phenix/images/a.qc2"}
	)

	cases := []struct {
		name    string
		log     func()
		level   string
		types   []any
		outcome string
		want    map[string]any
		absent  []string
	}{
		{
			name:    "success",
			log:     func() { audit.Success(request(admin), audit.FileDelete, "deleted file", f) },
			level:   "INFO",
			types:   []any{"deletion"},
			outcome: "success",
			want:    map[string]any{"phenix.vm.interface": float64(2)},
			absent:  []string{"event.reason", "error.message"},
		},
		{
			name:    "denied",
			log:     func() { audit.Denied(request(admin), audit.FileDelete, "deleting file not allowed", f) },
			level:   "WARN",
			types:   []any{"deletion", "denied"},
			outcome: "failure",
			want:    map[string]any{"event.reason": "forbidden"},
			absent:  []string{"error.message"},
		},
		{
			name: "failure",
			log: func() {
				audit.Failure(request(admin), audit.FileDelete, "deleting file failed", f, errors.New("disk full"))
			},
			level:   "WARN",
			types:   []any{"deletion"},
			outcome: "failure",
			want:    map[string]any{"error.message": "disk full"},
			absent:  []string{"event.reason"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := capture(t)

			c.log()

			lines := events()
			if len(lines) != 1 {
				t.Fatalf("got %d audit records, want 1: %q", len(lines), lines)
			}

			if strings.Contains(lines[0], "token") || strings.Contains(lines[0], "secret") {
				t.Errorf("record carries the URL query: %s", lines[0])
			}

			var got map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
				t.Fatal(err)
			}

			want := map[string]any{
				"level":                  c.level,
				"type":                   "AUDIT",
				"event.kind":             "event",
				"event.dataset":          "phenix.audit",
				"event.category":         "file",
				"event.type":             c.types,
				"event.action":           "file-delete",
				"event.outcome":          c.outcome,
				"user.name":              "alice",
				"user.roles":             []any{"Experiment Admin"},
				"source.ip":              "192.0.2.10",
				"network.forwarded_ip":   "203.0.113.7",
				"http.request.method":    "DELETE",
				"url.path":               "/api/v1/x",
				"phenix.experiment.name": "exp1",
				"phenix.vm.name":         "vm1",
				"file.path":              "/phenix/images/a.qc2",
				"file.name":              "a.qc2",
			}

			maps.Copy(want, c.want)

			for k, v := range want {
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}

			for _, k := range c.absent {
				if _, ok := got[k]; ok {
					t.Errorf("%s = %#v, want it absent", k, got[k])
				}
			}
		})
	}
}

func TestEventWithoutRoleOrInterface(t *testing.T) {
	events := capture(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/disks", nil)
	r.RemoteAddr = "@" // a unix socket's peer has no port

	audit.Denied(r, audit.DiskDownload, "downloading disk not allowed", audit.Fields{Reason: "not owner"})

	lines := events()
	if len(lines) != 1 {
		t.Fatalf("got %d audit records, want 1", len(lines))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}

	if got["user.name"] != "" || got["source.ip"] != "@" || got["event.reason"] != "not owner" {
		t.Errorf("user.name %q, source.ip %q, event.reason %q", got["user.name"], got["source.ip"], got["event.reason"])
	}

	for _, k := range []string{"user.roles", "network.forwarded_ip", "phenix.vm.interface", "file.path"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s present: %v", k, got[k])
		}
	}
}

func TestBackgroundEvent(t *testing.T) {
	events := capture(t)

	audit.Background("bob", audit.ConsoleEnd, "minimega console ended", audit.OutcomeSuccess, audit.Fields{
		PID: 42, Extra: []any{"phenix.console.end_reason", "idle"},
	})

	lines := events()
	if len(lines) != 1 {
		t.Fatalf("got %d audit records, want 1", len(lines))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"user.name": "bob", "event.action": "console-end", "event.outcome": "success",
		"process.pid": float64(42), "phenix.console.end_reason": "idle",
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}

	for _, k := range []string{"source.ip", "url.path", "http.request.method"} {
		if _, ok := got[k]; ok {
			t.Errorf("background event has %s", k)
		}
	}
}

func TestGrantUserOverridesContext(t *testing.T) {
	events := capture(t)

	r := httptest.NewRequest(http.MethodGet, "/webshark/captures/x", nil)

	audit.Success(r, audit.CaptureWebSharkRead, "read capture", audit.Fields{User: "carol"})

	lines := events()
	if len(lines) != 1 || !strings.Contains(lines[0], `"user.name":"carol"`) {
		t.Errorf("records = %q, want one for carol", lines)
	}
}
