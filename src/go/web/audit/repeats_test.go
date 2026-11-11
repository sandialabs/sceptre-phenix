package audit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/web/audit"
	"phenix/web/middleware"
)

// attachRequest is a VNC websocket request by user.
func attachRequest(user string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/experiments/exp/vms/vm/vnc/ws", nil)
	r.RemoteAddr = "192.0.2.10:4242"

	return r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyUser, user))
}

// attachRecord is what tells audited attaches apart.
type attachRecord struct {
	Outcome, User, Experiment, VM, Error string
	Suppressed                           float64
}

// attachRecords returns the records as attachRecords.
func attachRecords(t *testing.T, lines []string) []attachRecord {
	t.Helper()

	records := decoded(t, lines)
	got := make([]attachRecord, 0, len(records))

	for _, r := range records {
		outcome, _ := r["event.outcome"].(string)
		user, _ := r["user.name"].(string)
		exp, _ := r["phenix.experiment.name"].(string)
		vm, _ := r["phenix.vm.name"].(string)
		msg, _ := r["error.message"].(string)
		suppressed, _ := r["phenix.suppressed"].(float64)

		got = append(got, attachRecord{
			Outcome: outcome, User: user, Experiment: exp, VM: vm, Error: msg, Suppressed: suppressed,
		})

		if r["event.action"] != "vnc-attach" {
			t.Errorf("record event.action = %v, want vnc-attach", r["event.action"])
		}
	}

	return got
}

// A user's successes on one experiment and VM are audited at most once a
// success window, the next one counting those skipped; other users,
// experiments and VMs have their own windows.
func TestRepeatsSuccessWindow(t *testing.T) {
	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRepeats(5*time.Minute, time.Minute, 16, clock.Now)
		attach = func(user, exp, vm string) bool {
			return limit.Success(attachRequest(user), audit.VNCAttach, "vnc attached", audit.Fields{Experiment: exp, VM: vm})
		}
	)

	steps := []struct {
		advance       time.Duration
		user, exp, vm string
		want          bool
	}{
		{user: "alice", exp: "exp", vm: "vm", want: true},
		{user: "alice", exp: "exp", vm: "vm", want: false},
		{user: "bob", exp: "exp", vm: "vm", want: true},
		{user: "alice", exp: "exp", vm: "other", want: true},
		{user: "alice", exp: "other", vm: "vm", want: true},
		{advance: 5*time.Minute - time.Second, user: "alice", exp: "exp", vm: "vm", want: false},
		{advance: time.Second, user: "alice", exp: "exp", vm: "vm", want: true},
		{user: "alice", exp: "exp", vm: "vm", want: false},
	}

	for i, s := range steps {
		clock.advance(s.advance)

		if got := attach(s.user, s.exp, s.vm); got != s.want {
			t.Errorf("step %d: Success(%s, %s/%s) = %t, want %t", i, s.user, s.exp, s.vm, got, s.want)
		}
	}

	want := []attachRecord{
		{Outcome: "success", User: "alice", Experiment: "exp", VM: "vm"},
		{Outcome: "success", User: "bob", Experiment: "exp", VM: "vm"},
		{Outcome: "success", User: "alice", Experiment: "exp", VM: "other"},
		{Outcome: "success", User: "alice", Experiment: "other", VM: "vm"},
		{Outcome: "success", User: "alice", Experiment: "exp", VM: "vm", Suppressed: 2},
	}

	if diff := cmp.Diff(want, attachRecords(t, events())); diff != "" {
		t.Errorf("audited successes mismatch (-want +got):\n%s", diff)
	}
}

// A user's failures on one VM with one error are audited at most once a
// failure window; another error has its own window, and failures never hold
// back a success.
func TestRepeatsFailureWindow(t *testing.T) {
	var (
		events   = capture(t)
		clock    = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit    = audit.NewRepeats(5*time.Minute, time.Minute, 16, clock.Now)
		notFound = errors.New("vm not found")
		timeout  = errors.New("minimega timed out")
		fail     = func(user string, err error) bool {
			return limit.Failure(attachRequest(user), audit.VNCAttach, "vnc attach failed",
				audit.Fields{Experiment: "exp", VM: "vm"}, err)
		}
	)

	steps := []struct {
		advance time.Duration
		user    string
		err     error
		want    bool
	}{
		{user: "alice", err: notFound, want: true},
		{user: "alice", err: notFound, want: false},
		{user: "alice", err: notFound, want: false},
		{user: "alice", err: timeout, want: true},
		{user: "bob", err: notFound, want: true},
		{advance: time.Minute, user: "alice", err: notFound, want: true},
	}

	for i, s := range steps {
		clock.advance(s.advance)

		if got := fail(s.user, s.err); got != s.want {
			t.Errorf("step %d: Failure(%s, %v) = %t, want %t", i, s.user, s.err, got, s.want)
		}
	}

	if !limit.Success(attachRequest("alice"), audit.VNCAttach, "vnc attached", audit.Fields{Experiment: "exp", VM: "vm"}) {
		t.Error("Success(alice, exp/vm) after failures = false, want true")
	}

	want := []attachRecord{
		{Outcome: "failure", User: "alice", Experiment: "exp", VM: "vm", Error: "vm not found"},
		{Outcome: "failure", User: "alice", Experiment: "exp", VM: "vm", Error: "minimega timed out"},
		{Outcome: "failure", User: "bob", Experiment: "exp", VM: "vm", Error: "vm not found"},
		{Outcome: "failure", User: "alice", Experiment: "exp", VM: "vm", Error: "vm not found", Suppressed: 2},
		{Outcome: "success", User: "alice", Experiment: "exp", VM: "vm"},
	}

	if diff := cmp.Diff(want, attachRecords(t, events())); diff != "" {
		t.Errorf("audited failures mismatch (-want +got):\n%s", diff)
	}
}

// When it remembers max keys and none has a passed window, it audits every
// event of a new key; once a window has passed, it forgets the old keys and
// limits the new one.
func TestRepeatsBounded(t *testing.T) {
	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRepeats(5*time.Minute, time.Minute, 2, clock.Now)
		attach = func(vm string) bool {
			return limit.Success(attachRequest("alice"), audit.VNCAttach, "vnc attached", audit.Fields{VM: vm})
		}
	)

	for _, vm := range []string{"vm1", "vm2", "vm3", "vm3"} {
		if !attach(vm) {
			t.Errorf("Success(%s) with room for 2 VMs = false, want true", vm)
		}
	}

	clock.advance(5 * time.Minute)

	if !attach("vm3") {
		t.Error("Success(vm3) after the window = false, want true")
	}

	if attach("vm3") {
		t.Error("Success(vm3) again after vm1 and vm2 are forgotten = true, want false")
	}

	if n := len(events()); n != 5 {
		t.Errorf("audited %d successes, want 5", n)
	}
}
