package audit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// fakeClock is a clock the test moves.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// refusedRequest is a request from addr holding a token in its query.
func refusedRequest(addr string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/experiments?token=secret-token", nil)
	r.RemoteAddr = addr + ":4242"

	return r
}

// verified is a refusal for reason of a token whose signature was checked,
// naming user.
func verified(reason, user string) middleware.Refusal {
	return middleware.Refusal{Reason: reason, User: user, Verified: true}
}

// decoded returns the records, decoded.
func decoded(t *testing.T, lines []string) []map[string]any {
	t.Helper()

	records := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decoding audit record %s: %v", line, err)
		}

		records = append(records, record)
	}

	return records
}

// A source and reason's refusals are audited at most once a window, the next
// one audited counting those skipped; other sources and reasons have their
// own windows.
func TestRefusalsRateLimit(t *testing.T) {
	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRefusals(time.Minute, 16, clock.Now)
	)

	for range 5 {
		limit.Refused(refusedRequest("192.0.2.1"), verified(middleware.RefusedExpired, ""))
	}

	limit.Refused(refusedRequest("192.0.2.1"), verified(middleware.RefusedInvalid, ""))
	limit.Refused(refusedRequest("192.0.2.2"), verified(middleware.RefusedExpired, ""))

	clock.advance(59 * time.Second)
	limit.Refused(refusedRequest("192.0.2.1"), verified(middleware.RefusedExpired, ""))

	clock.advance(time.Second)
	limit.Refused(refusedRequest("192.0.2.1"), verified(middleware.RefusedRevoked, "alice"))
	limit.Refused(refusedRequest("192.0.2.1"), verified(middleware.RefusedExpired, ""))

	type refusal struct {
		IP, Reason, User string
		Suppressed       float64
	}

	records := decoded(t, events())
	got := make([]refusal, 0, len(records))

	for _, r := range records {
		user, _ := r["user.name"].(string)
		ip, _ := r["source.ip"].(string)
		reason, _ := r["event.reason"].(string)
		suppressed, _ := r["phenix.suppressed"].(float64)

		got = append(got, refusal{IP: ip, Reason: reason, User: user, Suppressed: suppressed})

		if r["event.action"] != "request-auth" || r["event.category"] != "authentication" ||
			r["event.outcome"] != "failure" || r["url.path"] != "/api/v1/experiments" {
			t.Errorf("refusal record = %v, want a request-auth authentication failure of /api/v1/experiments", r)
		}
	}

	want := []refusal{
		{IP: "192.0.2.1", Reason: "expired"},
		{IP: "192.0.2.1", Reason: "invalid"},
		{IP: "192.0.2.2", Reason: "expired"},
		{IP: "192.0.2.1", Reason: "revoked", User: "alice"},
		{IP: "192.0.2.1", Reason: "expired", Suppressed: 5},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("audited refusals mismatch (-want +got):\n%s", diff)
	}

	for _, line := range events() {
		if strings.Contains(line, "secret-token") {
			t.Errorf("refusal record %s holds the token", line)
		}
	}
}

// Once the sources it remembers reach the bound, those whose window has
// passed are forgotten, and refusals from new sources share one window.
func TestRefusalsBounded(t *testing.T) {
	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRefusals(time.Minute, 2, clock.Now)
	)

	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4"} {
		limit.Refused(refusedRequest(ip), verified(middleware.RefusedMissing, ""))
	}

	if n := len(events()); n != 3 {
		t.Errorf("refusals from 4 sources with room for 2 audited %d times, want 3 (the last two share one window)", n)
	}

	clock.advance(time.Minute)

	// 192.0.2.1 and .2 are forgotten, so .5 has a window of its own
	limit.Refused(refusedRequest("192.0.2.5"), verified(middleware.RefusedMissing, ""))
	limit.Refused(refusedRequest("192.0.2.5"), verified(middleware.RefusedMissing, ""))

	records := decoded(t, events())
	if n := len(records); n != 4 {
		t.Fatalf("audited %d refusals, want 4", n)
	}

	if ip := records[3]["source.ip"]; ip != "192.0.2.5" {
		t.Errorf("last refusal source.ip = %v, want 192.0.2.5", ip)
	}
}

// The user a token names is user.name only when its signature was checked:
// a proxy JWT's is phenix.auth.claimed_user. That user, the forwarded address
// and the path, which a client chooses, are cut short.
func TestRefusalsUnverifiedAndBounded(t *testing.T) {
	var (
		events  = capture(t)
		clock   = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit   = audit.NewRefusals(time.Minute, 16, clock.Now)
		long    = strings.Repeat("a", 1<<16)
		request = func(addr string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/"+long, nil)
			r.RemoteAddr = addr + ":4242"
			r.Header.Set("X-Forwarded-For", "203.0.113.7"+long+", 10.0.0.1")

			return r
		}
	)

	limit.Refused(request("192.0.2.1"), middleware.Refusal{Reason: middleware.RefusedRevoked, User: "admin", Verified: false})
	limit.Refused(request("192.0.2.2"), verified(middleware.RefusedRevoked, "alice"+long))

	type users struct {
		Name, Claimed string
		Lengths       [3]int // user, forwarded address and path
	}

	records := decoded(t, events())
	got := make([]users, 0, len(records))

	for _, r := range records {
		name, _ := r["user.name"].(string)
		claimed, _ := r["phenix.auth.claimed_user"].(string)
		forwarded, _ := r["network.forwarded_ip"].(string)
		path, _ := r["url.path"].(string)

		got = append(got, users{
			Name: name[:min(len(name), 5)], Claimed: claimed,
			Lengths: [3]int{max(len(name), len(claimed)), len(forwarded), len(path)},
		})
	}

	want := []users{
		{Name: "", Claimed: "admin", Lengths: [3]int{5, 64, 1024}},
		{Name: "alice", Claimed: "", Lengths: [3]int{128, 64, 1024}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refusals of a proxy JWT and of a long user mismatch (-want +got):\n%s", diff)
	}
}

// A request over the Unix socket names no user or client address, but says
// it came over the socket; a dry run is recorded as one, with event.type
// info rather than the action's own.
func TestUnixSocketAndDryRun(t *testing.T) {
	events := capture(t)

	admin := rbac.Role{Spec: &v1.RoleSpec{Name: "Global Admin", Policies: nil}}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/apply/main", nil)
	r.RemoteAddr = "@"

	ctx := context.WithValue(r.Context(), middleware.ContextKeyUser, "global-admin")
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, admin)

	middleware.UnixSocket(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		audit.Success(r, audit.WorkflowApply, "applied phenix workflow", audit.Fields{DryRun: true})
	})).ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx))

	want := map[string]any{
		"phenix.auth.method": "unix-socket",
		"user.roles":         []any{"Global Admin"},
		"event.type":         []any{"info"},
		"phenix.dry_run":     true,
		"url.path":           "/api/v1/workflow/apply/main",
	}

	if diff := cmp.Diff(want, pick(onlyEvent(t, events), want, []string{"user.name", "source.ip"})); diff != "" {
		t.Errorf("audit.Success() over the Unix socket record mismatch (-want +got):\n%s", diff)
	}
}
