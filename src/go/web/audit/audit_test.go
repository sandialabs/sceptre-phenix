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

// auditRequest is a GET of path from addr, with headers as name, value pairs,
// and a role named role when role is not "-".
func auditRequest(path, addr, role string, headers ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = addr

	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}

	if role == "-" {
		return r
	}

	ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, rbac.Role{Spec: &v1.RoleSpec{Name: role, Policies: nil}})

	return r.WithContext(ctx)
}

// Each event field comes from the request or Fields as documented, and is
// left out when there is nothing to put in it.
func TestEventFields(t *testing.T) {
	iface0 := 0
	forwarded := strings.Repeat("a", 63) + "é" + "b"

	cases := []struct {
		name   string
		log    func()
		want   map[string]any
		absent []string
	}{
		{
			name: "no role, interface or forwarded address",
			log: func() {
				r := auditRequest("/api/v1/disks", "@", "-", "X-Forwarded-For", " , 10.0.0.1")
				audit.Denied(r, audit.DiskDownload, "downloading disk not allowed", audit.Fields{Reason: "not owner"})
			},
			want:   map[string]any{"user.name": "", "source.ip": "@", "event.reason": "not owner"},
			absent: []string{"user.roles", "network.forwarded_ip", "phenix.vm.interface", "file.path"},
		},
		{
			name: "a role with no name",
			log: func() {
				audit.Success(auditRequest("/api/v1/disks", "192.0.2.1:1", ""), audit.DiskDownload, "downloaded", audit.Fields{})
			},
			want:   map[string]any{"source.ip": "192.0.2.1"},
			absent: []string{"user.roles"},
		},
		{
			name: "an IPv6 client",
			log: func() {
				audit.Success(auditRequest("/x", "[2001:db8::1]:443", "-"), audit.SignIn, "signed in", audit.Fields{})
			},
			want: map[string]any{"source.ip": "2001:db8::1", "event.category": "authentication", "event.type": []any{"start"}},
		},
		{
			name: "the user and path Fields give",
			log: func() {
				r := auditRequest("/webshark/captures/secret-token", "192.0.2.1:1", "-")
				audit.Success(r, audit.CaptureWebSharkRead, "read capture", audit.Fields{User: "carol", URLPath: "/webshark/captures/x"})
			},
			want: map[string]any{"user.name": "carol", "url.path": "/webshark/captures/x"},
		},
		{
			name: "a forwarded address cut on a character boundary",
			log: func() {
				r := auditRequest("/x", "192.0.2.1:1", "-", "X-Forwarded-For", forwarded+", 10.0.0.1")
				audit.Success(r, audit.SignIn, "signed in", audit.Fields{})
			},
			want: map[string]any{"network.forwarded_ip": strings.Repeat("a", 63)},
		},
		{
			name: "every subject field",
			log: func() {
				audit.Success(auditRequest("/x", "192.0.2.1:1", "-"), audit.VMFileCopy, "copied file", audit.Fields{
					TargetUser: "dave", Config: "Topology/topo", Experiment: "exp", VM: "vm", Interface: &iface0,
					File: "/a/b.txt", Target: "/c/d.txt", PID: 7, Extra: []any{"phenix.extra", "x"},
				})
			},
			want: map[string]any{
				"user.target.name": "dave", "phenix.config.name": "Topology/topo", "phenix.experiment.name": "exp",
				"phenix.vm.name": "vm", "phenix.vm.interface": float64(0), "file.path": "/a/b.txt", "file.name": "b.txt",
				"phenix.file.target_path": "/c/d.txt", "process.pid": float64(7), "phenix.extra": "x",
			},
		},
		{
			name: "no subject fields",
			log: func() {
				audit.Success(auditRequest("/x", "192.0.2.1:1", "-"), audit.SignOut, "signed out", audit.Fields{})
			},
			want: map[string]any{"event.type": []any{"end"}},
			absent: []string{
				"user.target.name", "phenix.config.name", "phenix.experiment.name", "phenix.vm.name",
				"phenix.vm.interface", "file.path", "file.name", "phenix.file.target_path", "process.pid",
				"phenix.dry_run", "event.reason", "error.message",
			},
		},
		{
			name: "an action with no ECS kind",
			log: func() {
				audit.Success(auditRequest("/x", "192.0.2.1:1", "-"), audit.Action("unknown"), "did something", audit.Fields{})
			},
			want: map[string]any{"event.action": "unknown", "event.category": "", "event.type": []any{"info"}},
		},
		{
			name: "a refused dry run",
			log: func() {
				r := auditRequest("/x", "192.0.2.1:1", "-")
				audit.Denied(r, audit.ConfigDelete, "deleting config not allowed", audit.Fields{DryRun: true})
			},
			want: map[string]any{"event.type": []any{"info", "denied"}, "phenix.dry_run": true, "event.reason": "forbidden"},
		},
		{
			name: "a background success with no user",
			log: func() {
				audit.Background("", audit.ConsoleEnd, "console ended", audit.OutcomeSuccess, audit.Fields{})
			},
			want:   map[string]any{"level": "INFO", "user.name": "", "event.outcome": "success"},
			absent: []string{"error.message", "source.ip"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := capture(t)

			c.log()

			if diff := cmp.Diff(c.want, pick(onlyEvent(t, events), c.want, c.absent)); diff != "" {
				t.Errorf("%s: record mismatch (-want +got):\n%s", c.name, diff)
			}
		})
	}
}
