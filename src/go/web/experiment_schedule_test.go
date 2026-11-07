package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/scheduler"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
)

// experimentScheduleMM is the test minimega, with the given cluster hosts or
// error.
type experimentScheduleMM struct {
	*testMM

	hosts mm.Hosts
	err   error
}

func (m experimentScheduleMM) GetClusterHosts(bool) (mm.Hosts, error) { return m.hosts, m.err }

// experimentScheduleStore is a store whose Get fails once a config was
// updated.
type experimentScheduleStore struct {
	store.Store

	updated atomic.Bool
}

func (s *experimentScheduleStore) Update(c *store.Config) error {
	s.updated.Store(true)

	return s.Store.Update(c) //nolint:wrapcheck // test double
}

func (s *experimentScheduleStore) Get(c *store.Config) error {
	if s.updated.Load() {
		return errors.New("store closed")
	}

	return s.Store.Get(c) //nolint:wrapcheck // test double
}

// useScheduleExperiment stores the experiment sched, not running, of VMs vm1
// and vm2, with vm1 scheduled on the host given unless it is empty.
func useScheduleExperiment(t *testing.T, vm1Host string) {
	t.Helper()

	vm := func(name string) map[string]any {
		return map[string]any{"type": "VirtualMachine", "general": map[string]any{"hostname": name}}
	}

	c := namedExperiment(t, "sched", "", vm("vm1"), vm("vm2"))
	if vm1Host != "" {
		c.Spec["schedules"] = map[string]any{"vm1": vm1Host}
	}

	useTestStore(t, c)
}

// experimentScheduleBody decodes a schedule response as a map of VM to host.
func experimentScheduleBody(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()

	var sched proto.ExperimentSchedule
	if err := unmarshaler.Unmarshal(w.Body.Bytes(), &sched); err != nil {
		t.Fatalf("unmarshaling schedule %s error = %v", w.Body.String(), err)
	}

	got := make(map[string]string)
	for _, s := range sched.GetSchedule() {
		got[s.GetVm()] = s.GetHost()
	}

	return got
}

// experimentScheduleLock locks the experiment name for an update until the
// test ends.
func experimentScheduleLock(t *testing.T, name string) {
	t.Helper()

	if err := cache.LockExperimentForUpdate(name); err != nil {
		t.Fatalf("cache.LockExperimentForUpdate(%s) error = %v", name, err)
	}

	t.Cleanup(func() { cache.UnlockExperiment(name) })
}

// GET /experiments/{name}/schedule answers the stored schedule, and refuses
// a caller without permission, a locked experiment and a missing one.
func TestGetExperimentSchedule(t *testing.T) {
	tests := map[string]struct {
		name   string
		role   string
		locked bool
		want   int
		body   map[string]string
	}{
		"scheduled":       {name: "sched", role: "sched", want: http.StatusOK, body: map[string]string{"vm1": "host-a"}},
		"other name only": {name: "sched", role: "other", want: http.StatusForbidden},
		"locked":          {name: "sched", role: "sched", locked: true, want: http.StatusConflict},
		"missing":         {name: "none", role: "none", want: http.StatusNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useScheduleExperiment(t, "host-a")

			if tc.locked {
				experimentScheduleLock(t, tc.name)
			}

			req := roleRequest(http.MethodGet, "/experiments/"+tc.name+"/schedule", "", map[string]string{"name": tc.name},
				testRole("experiments/schedule", "get", tc.role))

			w := httptest.NewRecorder()
			if got := handlerStatus(t, w, GetExperimentSchedule(w, req)); got != tc.want {
				t.Fatalf("GetExperimentSchedule(%s) status = %d, want %d", tc.name, got, tc.want)
			}

			if tc.body == nil {
				return
			}

			if diff := cmp.Diff(tc.body, experimentScheduleBody(t, w)); diff != "" {
				t.Errorf("GetExperimentSchedule(%s) schedule mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// POST /experiments/{name}/schedule schedules the VMs not yet scheduled, stores
// and answers the schedule, and audits it; each failure has its own status.
func TestScheduleExperiment(t *testing.T) {
	hosts := mm.Hosts{{Name: "host-a", Schedulable: true}, {Name: "host-b", Schedulable: true}}

	tests := map[string]struct {
		role         string
		locked       bool
		body         io.Reader
		hosts        mm.Hosts
		hostsErr     error
		failAfterSet bool
		want         int
		outcome      string
		fields       map[string]any
		schedule     map[string]string
	}{
		"scheduled": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hosts: hosts,
			want: http.StatusOK, outcome: "success", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
			schedule: map[string]string{"vm1": "host-a", "vm2": "host-b"},
		},
		// older clients spell the built-in schedulers with underscores
		"scheduled with an underscored name": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round_robin"}`), hosts: hosts,
			want: http.StatusOK, outcome: "success", fields: map[string]any{"phenix.schedule.algorithm": "round_robin"},
			schedule: map[string]string{"vm1": "host-a", "vm2": "host-b"},
		},
		"forbidden": {
			role: "other", body: strings.NewReader(`{"algorithm":"round-robin"}`), want: http.StatusForbidden,
			outcome: "failure", fields: map[string]any{"event.reason": "forbidden"},
		},
		"locked": {role: "sched", locked: true, body: strings.NewReader(`{"algorithm":"round-robin"}`), want: http.StatusConflict},
		"unreadable body": {
			role: "sched", body: settingsTestErrReader{}, want: http.StatusInternalServerError,
		},
		"not JSON": {role: "sched", body: strings.NewReader("{"), want: http.StatusBadRequest},
		"unknown algorithm": {
			role: "sched", body: strings.NewReader(`{"algorithm":"no-such-scheduler"}`), hosts: hosts,
			want: http.StatusBadRequest, outcome: "failure",
			fields: map[string]any{"phenix.schedule.algorithm": "no-such-scheduler"},
		},
		"no schedulable hosts": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`),
			want: http.StatusConflict, outcome: "failure", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
		},
		"cluster hosts unreadable": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hostsErr: errors.New("minimega gone"),
			want: http.StatusInternalServerError, outcome: "failure", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
		},
		"stored schedule unreadable": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hosts: hosts, failAfterSet: true,
			want: http.StatusInternalServerError, outcome: "success", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useScheduleExperiment(t, "host-a")
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

			fake := experimentScheduleMM{testMM: &testMM{}, hosts: tc.hosts, err: tc.hostsErr}
			mm.DefaultMM = fake //nolint:reassign // install test double

			if tc.failAfterSet {
				storetest.Use(t, &experimentScheduleStore{Store: store.DefaultStore})
			}

			if tc.locked {
				experimentScheduleLock(t, "sched")
			}

			events := captureAudit(t)

			req := roleRequest(http.MethodPost, "/experiments/sched/schedule", "", map[string]string{"name": "sched"},
				testRole("experiments/schedule", "create", tc.role))
			req.Body = io.NopCloser(tc.body)

			w := httptest.NewRecorder()
			if got := handlerStatus(t, w, ScheduleExperiment(w, req)); got != tc.want {
				t.Fatalf("ScheduleExperiment(%s) status = %d, want %d: %s", name, got, tc.want, w.Body.String())
			}

			if tc.outcome == "" {
				if n := countAudit(events(), "experiment-schedule", "failure", nil) +
					countAudit(events(), "experiment-schedule", "success", nil); n != 0 {
					t.Errorf("ScheduleExperiment(%s) audited %d events, want none", name, n)
				}
			} else {
				wantAudit(t, events(), "experiment-schedule", tc.outcome, tc.fields)
			}

			if tc.schedule == nil {
				return
			}

			if diff := cmp.Diff(tc.schedule, experimentScheduleBody(t, w)); diff != "" {
				t.Errorf("ScheduleExperiment(%s) answered schedule mismatch (-want +got):\n%s", name, diff)
			}

			exp, err := experiment.Get("sched")
			if err != nil {
				t.Fatalf("experiment.Get(sched) error = %v", err)
			}

			if diff := cmp.Diff(tc.schedule, exp.Spec.Schedules()); diff != "" {
				t.Errorf("stored schedule after ScheduleExperiment(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// A failed schedule answers 404 for a missing experiment, 409 for a running one or a cluster without
// schedulable hosts, 400 for an unknown algorithm and 500 otherwise.
func TestScheduleErrorStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"missing experiment": {err: fmt.Errorf("getting experiment: %w", store.ErrNotExist), want: http.StatusNotFound},
		"running experiment": {err: fmt.Errorf("%w (started at: now)", experiment.ErrExperimentRunning), want: http.StatusConflict},
		"no schedulable hosts": {
			err:  fmt.Errorf("running scheduler algorithm: %w", scheduler.ErrNoSchedulableHosts),
			want: http.StatusConflict,
		},
		"unknown algorithm": {
			err:  fmt.Errorf("running user scheduler: %w", scheduler.ErrUserSchedulerNotFound),
			want: http.StatusBadRequest,
		},
		"other failure": {err: errors.New("updating experiment config"), want: http.StatusInternalServerError},
		"nil":           {err: nil, want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := scheduleErrorStatus(tc.err); got != tc.want {
				t.Errorf("scheduleErrorStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
