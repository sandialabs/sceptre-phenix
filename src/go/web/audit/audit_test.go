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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// onlyEvent returns the one audit record logged, decoded.
func onlyEvent(t *testing.T, events func() []string) map[string]any {
	t.Helper()

	lines := events()
	if len(lines) != 1 {
		t.Fatalf("logged %d audit records, want 1: %q", len(lines), lines)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("decoding audit record %s: %v", lines[0], err)
	}

	return got
}

// pick returns the fields of a record named in want or absent, so that
// comparing it with want also reports absent fields that are present.
func pick(record, want map[string]any, absent []string) map[string]any {
	picked := make(map[string]any, len(want))

	for _, k := range slices.Concat(slices.Collect(maps.Keys(want)), absent) {
		if v, ok := record[k]; ok {
			picked[k] = v
		}
	}

	return picked
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
			name:    "Success",
			log:     func() { audit.Success(request(admin), audit.FileDelete, "deleted file", f) },
			level:   "INFO",
			types:   []any{"deletion"},
			outcome: "success",
			want:    map[string]any{"phenix.vm.interface": float64(2)},
			absent:  []string{"event.reason", "error.message"},
		},
		{
			name:    "Denied",
			log:     func() { audit.Denied(request(admin), audit.FileDelete, "deleting file not allowed", f) },
			level:   "WARN",
			types:   []any{"deletion", "denied"},
			outcome: "failure",
			want:    map[string]any{"event.reason": "forbidden"},
			absent:  []string{"error.message"},
		},
		{
			name: "Failure",
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

			got := onlyEvent(t, events)

			if line := events()[0]; strings.Contains(line, "token") || strings.Contains(line, "secret") {
				t.Errorf("audit.%s() record = %s, want no URL query", c.name, line)
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

			if diff := cmp.Diff(want, pick(got, want, c.absent)); diff != "" {
				t.Errorf("audit.%s() record mismatch (-want +got):\n%s", c.name, diff)
			}
		})
	}
}

func TestEventWithoutRoleOrInterface(t *testing.T) {
	events := capture(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/disks", nil)
	r.RemoteAddr = "@" // a unix socket's peer has no port

	audit.Denied(r, audit.DiskDownload, "downloading disk not allowed", audit.Fields{Reason: "not owner"})

	want := map[string]any{"user.name": "", "source.ip": "@", "event.reason": "not owner"}
	absent := []string{"user.roles", "network.forwarded_ip", "phenix.vm.interface", "file.path"}

	if diff := cmp.Diff(want, pick(onlyEvent(t, events), want, absent)); diff != "" {
		t.Errorf("audit.Denied() without a role or interface: record mismatch (-want +got):\n%s", diff)
	}
}

func TestBackgroundEvents(t *testing.T) {
	cases := []struct {
		name   string
		log    func()
		want   map[string]any
		absent []string
	}{
		{
			name: "Background",
			log: func() {
				audit.Background("bob", audit.ConsoleEnd, "minimega console ended", audit.OutcomeSuccess, audit.Fields{
					PID: 42, Extra: []any{"phenix.console.end_reason", "idle"},
				})
			},
			want: map[string]any{
				"user.name": "bob", "event.action": "console-end", "event.outcome": "success",
				"process.pid": float64(42), "phenix.console.end_reason": "idle",
			},
			absent: []string{"source.ip", "url.path", "http.request.method"},
		},
		{
			name: "BackgroundFailure",
			log: func() {
				audit.BackgroundFailure("bob", audit.DiskClone, "cloning disk failed", audit.Fields{
					File: "/files/a.qc2", Target: "/files/b.qc2",
				}, errors.New("disk full"))
			},
			want: map[string]any{
				"level": "WARN", "user.name": "bob", "event.action": "disk-clone", "event.outcome": "failure",
				"error.message": "disk full", "file.path": "/files/a.qc2", "phenix.file.target_path": "/files/b.qc2",
			},
			absent: []string{"event.reason", "source.ip", "url.path"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := capture(t)

			c.log()

			if diff := cmp.Diff(c.want, pick(onlyEvent(t, events), c.want, c.absent)); diff != "" {
				t.Errorf("audit.%s() record mismatch (-want +got):\n%s", c.name, diff)
			}
		})
	}
}

func TestGrantUserOverridesContext(t *testing.T) {
	events := capture(t)

	r := httptest.NewRequest(http.MethodGet, "/webshark/captures/x", nil)

	audit.Success(r, audit.CaptureWebSharkRead, "read capture", audit.Fields{User: "carol"})

	want := map[string]any{"user.name": "carol"}

	if diff := cmp.Diff(want, pick(onlyEvent(t, events), want, nil)); diff != "" {
		t.Errorf("audit.Success(Fields{User: carol}) record mismatch (-want +got):\n%s", diff)
	}
}
