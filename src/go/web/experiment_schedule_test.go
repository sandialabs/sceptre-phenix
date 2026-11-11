package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	gwebsocket "github.com/gorilla/websocket"

	"phenix/api/experiment"
	"phenix/scheduler"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/util/polltest"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// experimentScheduleMM is the test minimega, with the given cluster hosts or
// error.
type experimentScheduleMM struct {
	*testMM

	hosts mm.Hosts
	err   error
}

func (m experimentScheduleMM) GetClusterHosts(bool) (mm.Hosts, error) {
	return slices.Clone(m.hosts), m.err
}

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

// experimentScheduleWarnings decodes the warnings of a schedule response.
func experimentScheduleWarnings(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()

	var sched proto.ExperimentSchedule
	if err := unmarshaler.Unmarshal(w.Body.Bytes(), &sched); err != nil {
		t.Fatalf("unmarshaling schedule %s error = %v", w.Body.String(), err)
	}

	return sched.GetWarnings()
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
		warnings     []string
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
		"pinned to a host not in the cluster": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hosts: hosts[1:],
			want: http.StatusOK, outcome: "success", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
			schedule: map[string]string{"vm1": "host-a", "vm2": "host-b"},
			warnings: []string{"VM vm1 stays on host host-a, which is not a schedulable cluster host"},
		},
		"mesh peers do not answer": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`),
			hostsErr: fmt.Errorf("%w: none of the mesh peers compute1 answered", mm.ErrMeshUnavailable),
			want:     http.StatusServiceUnavailable, outcome: "failure",
			fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
		},
		"cluster hosts unreadable": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hostsErr: errors.New("minimega gone"),
			want: http.StatusInternalServerError, outcome: "failure", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
		},
		// the answer is the experiment as stored, not read again
		"store unreadable after the schedule": {
			role: "sched", body: strings.NewReader(`{"algorithm":"round-robin"}`), hosts: hosts, failAfterSet: true,
			want: http.StatusOK, outcome: "success", fields: map[string]any{"phenix.schedule.algorithm": "round-robin"},
			schedule: map[string]string{"vm1": "host-a", "vm2": "host-b"},
		},
		"empty algorithm": {
			role: "sched", body: strings.NewReader(`{"algorithm":""}`), hosts: hosts,
			want: http.StatusBadRequest, outcome: "failure", fields: map[string]any{"phenix.schedule.algorithm": ""},
		},
		"algorithm with a path": {
			role: "sched", body: strings.NewReader(`{"algorithm":"x/../../bin/sh"}`), hosts: hosts,
			want: http.StatusBadRequest, outcome: "failure", fields: map[string]any{"phenix.schedule.algorithm": "x/../../bin/sh"},
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

			if diff := cmp.Diff(tc.warnings, experimentScheduleWarnings(t, w), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ScheduleExperiment(%s) answered warnings mismatch (-want +got):\n%s", name, diff)
			}

			if tc.failAfterSet {
				return
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

// lockCheckingMM records the experiment lock status while the scheduler reads
// the cluster hosts.
type lockCheckingMM struct {
	*testMM

	status *cache.Status
}

func (m lockCheckingMM) GetClusterHosts(bool) (mm.Hosts, error) {
	*m.status = cache.IsExperimentLocked("sched")

	return mm.Hosts{{Name: "host-a", Schedulable: true}}, nil
}

// POST /experiments/{name}/schedule holds the experiment's update lock while
// it schedules, so a start cannot begin, and frees it after.
func TestScheduleExperimentHoldsTheLock(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM and the store
	useScheduleExperiment(t, "")

	var during cache.Status

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = lockCheckingMM{testMM: &testMM{}, status: &during} //nolint:reassign // install test double

	req := roleRequest(http.MethodPost, "/experiments/sched/schedule", `{"algorithm":"round-robin"}`,
		map[string]string{"name": "sched"}, testRole("experiments/schedule", "create", "sched"))

	w := httptest.NewRecorder()
	if got := handlerStatus(t, w, ScheduleExperiment(w, req)); got != http.StatusOK {
		t.Fatalf("ScheduleExperiment() status = %d, want %d: %s", got, http.StatusOK, w.Body.String())
	}

	if during != cache.StatusUpdating {
		t.Errorf("experiment lock while scheduling = %q, want %q", during, cache.StatusUpdating)
	}

	if after := cache.IsExperimentLocked("sched"); after != "" {
		t.Errorf("experiment lock after scheduling = %q, want none", after)
	}
}

// cancelingMM ends the request while the scheduler reads the cluster hosts.
type cancelingMM struct {
	*testMM

	cancel context.CancelFunc
}

func (m cancelingMM) GetClusterHosts(bool) (mm.Hosts, error) {
	m.cancel()

	return nil, context.Canceled
}

// A schedule whose request ended writes no answer and audits nothing.
func TestScheduleExperimentRequestEnded(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM and the store
	useScheduleExperiment(t, "")

	req := roleRequest(http.MethodPost, "/experiments/sched/schedule", `{"algorithm":"round-robin"}`,
		map[string]string{"name": "sched"}, testRole("experiments/schedule", "create", "sched"))

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	req = req.WithContext(ctx)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = cancelingMM{testMM: &testMM{}, cancel: cancel} //nolint:reassign // install test double

	events := captureAudit(t)

	w := httptest.NewRecorder()
	if err := ScheduleExperiment(w, req); err != nil {
		t.Fatalf("ScheduleExperiment() with an ended request error = %v, want nil", err)
	}

	if w.Body.Len() != 0 {
		t.Errorf("ScheduleExperiment() with an ended request wrote %q, want nothing", w.Body.String())
	}

	if n := countAudit(events(), "experiment-schedule", "failure", nil) +
		countAudit(events(), "experiment-schedule", "success", nil); n != 0 {
		t.Errorf("ScheduleExperiment() with an ended request audited %d events, want none", n)
	}
}

// leavingMM ends the request while the scheduler reads the cluster hosts, and
// gives the hosts.
type leavingMM struct {
	*testMM

	cancel context.CancelFunc
}

func (m leavingMM) GetClusterHosts(bool) (mm.Hosts, error) {
	m.cancel()

	return mm.Hosts{{Name: "host-a", Schedulable: true}}, nil
}

// A schedule that works after its client left is stored and audited, but no
// answer is written.
func TestScheduleExperimentClientLeft(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM and the store
	useScheduleExperiment(t, "")

	req := roleRequest(http.MethodPost, "/experiments/sched/schedule", `{"algorithm":"round-robin"}`,
		map[string]string{"name": "sched"}, testRole("experiments/schedule", "create", "sched"))

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	req = req.WithContext(ctx)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = leavingMM{testMM: &testMM{}, cancel: cancel} //nolint:reassign // install test double

	events := captureAudit(t)

	w := httptest.NewRecorder()
	if err := ScheduleExperiment(w, req); err != nil {
		t.Fatalf("ScheduleExperiment() after its client left error = %v, want nil", err)
	}

	if w.Body.Len() != 0 {
		t.Errorf("ScheduleExperiment() after its client left wrote %q, want nothing", w.Body.String())
	}

	wantAudit(t, events(), "experiment-schedule", "success", map[string]any{"phenix.schedule.algorithm": "round-robin"})

	exp, err := experiment.Get("sched")
	if err != nil {
		t.Fatalf("experiment.Get(sched) error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"vm1": "host-a", "vm2": "host-a"}, exp.Spec.Schedules()); diff != "" {
		t.Errorf("stored schedule after the client left mismatch (-want +got):\n%s", diff)
	}
}

// A failed schedule answers 404 for a missing experiment, 409 for a running or starting one, one that changed
// while it was scheduled, or a cluster without schedulable hosts, 400 for an unknown or invalid algorithm, 504 for a
// scheduler that did not finish in time, 503 for mesh peers that do not answer and 500 otherwise.
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
		"invalid algorithm": {err: fmt.Errorf("%w: %q", scheduler.ErrInvalidName, ""), want: http.StatusBadRequest},
		"starting experiment": {
			err: &experiment.StartingError{Name: "sched"}, want: http.StatusConflict,
		},
		"changed while scheduled": {
			err: fmt.Errorf("experiment sched changed: %w", store.ErrConflict), want: http.StatusConflict,
		},
		"scheduler timed out": {
			err:  fmt.Errorf("running scheduler algorithm: %w", scheduler.ErrTimeout),
			want: http.StatusGatewayTimeout,
		},
		"mesh peers do not answer": {
			err:  fmt.Errorf("running scheduler algorithm: getting cluster hosts: %w", mm.ErrMeshUnavailable),
			want: http.StatusServiceUnavailable,
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

// scheduleListener is a broker client that records the warnings of each
// experiment schedule broadcast it gets, and how many probes it got.
type scheduleListener struct {
	mu        sync.Mutex
	schedules [][]string
	probes    int
}

// got returns the warnings of each schedule broadcast and the probe count.
func (l *scheduleListener) got() ([][]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.schedules), l.probes
}

// scheduleListeners runs the broker for the rest of the test with one client
// of each role, and returns them once each gets broadcasts.
func scheduleListeners(t *testing.T, roles ...rbac.Role) []*scheduleListener {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		broker.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	listeners := make([]*scheduleListener, 0, len(roles))

	for _, role := range roles {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			broker.ServeWS(w, r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyRole, role)))
		}))
		t.Cleanup(server.Close)

		conn, resp, err := gwebsocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dialing the broker: %v", err)
		}

		resp.Body.Close()
		t.Cleanup(func() { conn.Close() })

		l := new(scheduleListener)
		listeners = append(listeners, l)

		go func() {
			for {
				_, b, err := conn.ReadMessage()
				if err != nil {
					return
				}

				// the broker may send several messages in one frame
				for dec := json.NewDecoder(bytes.NewReader(b)); ; {
					var m struct {
						Resource struct {
							Type   string `json:"type"`
							Action string `json:"action"`
						} `json:"resource"`
						Result struct {
							Warnings []string `json:"warnings"`
						} `json:"result"`
					}

					if dec.Decode(&m) != nil {
						break
					}

					l.mu.Lock()

					switch {
					case m.Resource.Type == "schedule/probe":
						l.probes++
					case m.Resource.Type == "experiment" && m.Resource.Action == "schedule":
						l.schedules = append(l.schedules, m.Result.Warnings)
					}

					l.mu.Unlock()
				}
			}
		}()
	}

	scheduleProbe(t, listeners, 1)

	return listeners
}

// scheduleProbe broadcasts probes to every client until each listener has
// got at least want of them.
func scheduleProbe(t *testing.T, listeners []*scheduleListener, want int) {
	t.Helper()

	polltest.Until(t, "the broker clients getting broadcasts", func() bool {
		broker.Broadcast(nil, bt.NewResource("schedule/probe", "x", "probe"), nil)
		time.Sleep(5 * time.Millisecond)

		for _, l := range listeners {
			if _, probes := l.got(); probes < want {
				return false
			}
		}

		return true
	})
}

// POST /experiments/{name}/schedule broadcasts the schedule, with the
// scheduler's warnings, to the roles that may get the experiment's schedule,
// and to no other role, not even one that may only schedule it.
//
//nolint:paralleltest // replaces mm.DefaultMM and the store, and runs the broker
func TestScheduleExperimentBroadcast(t *testing.T) {
	useScheduleExperiment(t, "gone")

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = experimentScheduleMM{ //nolint:reassign // install test double
		testMM: &testMM{}, hosts: mm.Hosts{{Name: "host-a", Schedulable: true}},
	}

	listeners := scheduleListeners(t,
		testRole("experiments/schedule", "get", "sched"),
		testRole("experiments/schedule", "create", "sched"),
		testRole("experiments/schedule", "get", "other"),
	)

	req := roleRequest(http.MethodPost, "/experiments/sched/schedule", `{"algorithm":"round-robin"}`,
		map[string]string{"name": "sched"}, testRole("experiments/schedule", "create", "sched"))

	w := httptest.NewRecorder()
	if got := handlerStatus(t, w, ScheduleExperiment(w, req)); got != http.StatusOK {
		t.Fatalf("ScheduleExperiment() status = %d, want %d: %s", got, http.StatusOK, w.Body.String())
	}

	// the clients get messages in order, so a later probe comes after the schedule
	_, sent := listeners[0].got()
	scheduleProbe(t, listeners, sent+1)

	warnings := []string{"VM vm1 stays on host gone, which is not a schedulable cluster host"}

	for i, want := range [][][]string{{warnings}, nil, nil} {
		got, _ := listeners[i].got()
		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("schedule broadcasts to client %d mismatch (-want +got):\n%s", i, diff)
		}
	}
}
