package experiment

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// resumeMM is a delayedMM that also reports VM states, or fails to.
type resumeMM struct {
	*delayedMM

	states map[string]string
	err    error
}

func (m *resumeMM) GetVMStates(...mm.Option) (map[string]string, error) {
	return m.states, m.err
}

// useResumeMM installs a resumeMM reporting states (or err) and returns a lister of the commands sent.
func useResumeMM(t *testing.T, states map[string]string, err error) func() []mmtest.Command {
	t.Helper()

	fake, received := useDelayedMM(t)
	mm.DefaultMM = &resumeMM{delayedMM: fake, states: states, err: err} //nolint:reassign // useDelayedMM restores the default

	return received
}

// pendingStatus is a status recording the given pending starts.
func pendingStatus(timers map[string]string, c2 map[string]map[string]bool, postStart bool) *types.Experiment {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
	exp.Status.SetDelayedStarts(timers, c2, postStart)

	return exp
}

func TestPlanResume(t *testing.T) {
	t.Parallel()

	var (
		now    = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		past   = now.Add(-time.Minute).Format(time.RFC3339Nano)
		future = now.Add(time.Minute).Format(time.RFC3339Nano)
	)

	tests := []struct {
		name      string
		timers    map[string]string
		c2        map[string]map[string]bool
		states    map[string]string
		statesErr error
		want      resumePlan
		wantGone  string // part of the interrupted reason, or "" when not interrupted
	}{
		{
			name:   "re-arms unstarted VMs, an overdue one at once, and the post-start apps",
			timers: map[string]string{"late": past, "soon": future, "done": past},
			c2:     map[string]map[string]bool{"client": {"server": true}},
			states: map[string]string{"late": "BUILDING", "soon": "BUILDING", "done": "RUNNING", "client": "BUILDING", "server": "RUNNING"},
			want: resumePlan{
				delays:    map[string]time.Duration{"late": 0, "soon": time.Minute},
				timers:    map[string]string{"late": past, "soon": future},
				c2s:       map[string]map[string]bool{"client": {"server": true}},
				postStart: true,
			},
		},
		{
			name:   "drops VMs already started, leaving only the post-start apps",
			timers: map[string]string{"done": past},
			c2:     map[string]map[string]bool{"client": {"server": false}},
			states: map[string]string{"done": "RUNNING", "client": "PAUSED", "server": "RUNNING"},
			want:   resumePlan{postStart: true},
		},
		{
			name:     "interrupts a start whose VM is gone",
			timers:   map[string]string{"gone": future, "soon": future},
			states:   map[string]string{"soon": "BUILDING"},
			want:     resumePlan{delays: map[string]time.Duration{"soon": time.Minute}, timers: map[string]string{"soon": future}},
			wantGone: ": gone",
		},
		{
			name:     "interrupts a start whose C2-delayed VM waits on a VM that is gone",
			c2:       map[string]map[string]bool{"client": {"server": false}},
			states:   map[string]string{"client": "BUILDING"},
			wantGone: ": client",
		},
		{
			name:   "re-arms a VM whose due time does not parse at once",
			timers: map[string]string{"odd": "soon"},
			states: map[string]string{"odd": "BUILDING"},
			want: resumePlan{
				delays: map[string]time.Duration{"odd": 0}, timers: map[string]string{"odd": "soon"}, postStart: true,
			},
		},
		{
			name:      "interrupts a start when minimega cannot list the VMs",
			timers:    map[string]string{"soon": future},
			statesErr: errors.New("minimega unreachable"),
			wantGone:  "minimega could not list the VMs: minimega unreachable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			exp := pendingStatus(test.timers, test.c2, true)

			got := planResume(exp.Status, test.states, test.statesErr, now)

			if (got.interrupted != "") != (test.wantGone != "") || !strings.Contains(got.interrupted, test.wantGone) {
				t.Errorf("planResume() interrupted = %q, want it to contain %q", got.interrupted, test.wantGone)
			}

			got.interrupted = ""

			opts := cmp.Options{cmp.AllowUnexported(resumePlan{}), cmpopts.EquateEmpty()}
			if diff := cmp.Diff(test.want, got, opts); diff != "" {
				t.Errorf("planResume() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Whatever the VMs' states, a plan re-arms exactly the pending VMs minimega holds unstarted, a timer VM after the
// time left to its due time or at once, and a C2-delayed VM only when every VM it waits on is present. It runs the
// post-start apps only when nothing is interrupted, and is interrupted exactly when a pending VM is gone.
func TestPlanResumeProperties(t *testing.T) {
	t.Parallel()

	var (
		now   = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		vms   = rapid.SampledFrom([]string{"a", "b", "c", "d", "e"})
		state = rapid.SampledFrom([]string{"BUILDING", "RUNNING", "PAUSED", "QUIT", "ERROR"})
	)

	rapid.Check(t, func(t *rapid.T) {
		var (
			dueIn  = rapid.MapOf(vms, rapid.IntRange(-120, 120)).Draw(t, "dueIn")
			c2     = rapid.MapOf(vms, rapid.MapOfN(vms, rapid.Bool(), 1, 2)).Draw(t, "c2")
			states = rapid.MapOf(vms, state).Draw(t, "states")
			timers = make(map[string]string, len(dueIn))
		)

		for vm, s := range dueIn {
			timers[vm] = now.Add(time.Duration(s) * time.Second).Format(time.RFC3339Nano)

			// a VM has one kind of delay
			delete(c2, vm)
		}

		plan := planResume(pendingStatus(timers, c2, true).Status, states, nil, now)

		want := resumePlan{delays: map[string]time.Duration{}, timers: map[string]string{}, c2s: map[string]map[string]bool{}}

		for vm, s := range dueIn {
			if states[vm] == mmStateBuilding {
				want.delays[vm] = max(time.Duration(s)*time.Second, 0)
				want.timers[vm] = timers[vm]
			}
		}

		for vm, others := range c2 {
			present := true

			for other := range others {
				if _, ok := states[other]; !ok {
					present = false
				}
			}

			if states[vm] == mmStateBuilding && present {
				want.c2s[vm] = others
			}
		}

		opts := cmp.Options{
			cmp.AllowUnexported(resumePlan{}), cmpopts.EquateEmpty(), cmpopts.IgnoreFields(resumePlan{}, "interrupted", "postStart"),
		}
		if diff := cmp.Diff(want, plan, opts); diff != "" {
			t.Fatalf("planResume() re-armed VMs mismatch (-want +got):\n%s", diff)
		}

		var gone bool

		for _, vm := range slices.Concat(slices.Collect(maps.Keys(timers)), slices.Collect(maps.Keys(c2))) {
			if _, ok := states[vm]; !ok {
				gone = true
			}
		}

		for vm, others := range c2 {
			for other := range others {
				if _, ok := states[other]; !ok && states[vm] == mmStateBuilding {
					gone = true
				}
			}
		}

		if (plan.interrupted != "") != gone {
			t.Fatalf("plan interrupted = %q, want interrupted %t", plan.interrupted, gone)
		}

		if plan.postStart == gone {
			t.Fatalf("plan runs the post-start apps %t, want %t", plan.postStart, !gone)
		}
	})
}

// storePendingExperiment stores experiment exp, running and recording the given pending starts.
func storePendingExperiment(t *testing.T, timers map[string]string, c2 map[string]map[string]bool) {
	t.Helper()

	storetest.Use(t)

	c2Status := make(map[string]any, len(c2))
	for vm, others := range c2 {
		c2Status[vm] = map[string]any{}
		for other, byUUID := range others {
			c2Status[vm].(map[string]any)[other] = byUUID
		}
	}

	timerStatus := make(map[string]any, len(timers))
	for vm, due := range timers {
		timerStatus[vm] = due
	}

	storeExperiment(t, "exp", map[string]any{
		"startTime":        stopTestStart,
		"delayedTimers":    timerStatus,
		"delayedC2":        c2Status,
		"postStartPending": true,
	})
}

// waitClosed waits for errChan to close, returning the errors sent on it.
func waitClosed(t *testing.T, errChan chan error) []error {
	t.Helper()

	var errs []error

	timeout := time.After(10 * time.Second)

	for {
		select {
		case err, ok := <-errChan:
			if !ok {
				return errs
			}

			errs = append(errs, err)
		case <-timeout:
			t.Fatal("error channel still open 10s later")
		}
	}
}

func TestResumeStartStartsTheVMsStillWaiting(t *testing.T) { //nolint:paralleltest // replaces package globals
	soon := time.Now().Add(20 * time.Millisecond).UTC().Format(time.RFC3339Nano)

	storePendingExperiment(t,
		map[string]string{"timed": soon, "done": soon},
		map[string]map[string]bool{"client": {"server": false}},
	)

	received := useResumeMM(t, map[string]string{
		"timed": "BUILDING", "done": "RUNNING", "client": "BUILDING", "server": "RUNNING",
	}, nil)
	mm.DefaultMM.(*resumeMM).activate(mm.C2ClientRef{VM: "server", ByUUID: false})

	names, err := PendingStarts()
	if err != nil || !slices.Equal(names, []string{"exp"}) {
		t.Fatalf("PendingStarts() = %q, %v; want [exp], nil", names, err)
	}

	errChan := make(chan error)

	resumed, err := ResumeStart(context.Background(), "exp", errChan)
	if !resumed || err != nil {
		t.Fatalf("ResumeStart() = %t, %v; want true, nil", resumed, err)
	}

	if errs := waitClosed(t, errChan); len(errs) != 0 {
		t.Fatalf("resumed start errors = %v, want none", errs)
	}

	want := []string{"vm start client", "vm start timed"}

	got := mmtest.Bases(received())
	slices.Sort(got)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("resumed start commands mismatch (-want +got):\n%s", diff)
	}

	exp := experimentNamed(t, "exp")

	if hasPendingStart(exp.Status) || exp.Status.Interrupted() != "" || !exp.Running() {
		t.Errorf(
			"status after the resumed start: timers %v, c2 %v, post-start %t, interrupted %q, running %t; want nothing pending and running",
			exp.Status.DelayedTimers(),
			exp.Status.DelayedC2(),
			exp.Status.PostStartPending(),
			exp.Status.Interrupted(),
			exp.Running(),
		)
	}
}

func TestResumeStartRecordsAnInterruptedStart(t *testing.T) { //nolint:paralleltest // replaces package globals
	soon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)

	storePendingExperiment(t, map[string]string{"gone": soon}, nil)
	received := useResumeMM(t, map[string]string{}, nil)

	errChan := make(chan error)

	resumed, err := ResumeStart(context.Background(), "exp", errChan)
	if resumed || err != nil {
		t.Fatalf("ResumeStart() = %t, %v; want false, nil", resumed, err)
	}

	if _, open := <-errChan; open {
		t.Error("error channel open after ResumeStart() resumed nothing, want it closed")
	}

	if got := received(); len(got) != 0 {
		t.Errorf("ResumeStart() sent %q, want nothing", mmtest.Bases(got))
	}

	exp := experimentNamed(t, "exp")

	if got := exp.Status.Interrupted(); !strings.Contains(got, "no longer in minimega: gone") {
		t.Errorf("stored interrupted reason = %q, want it to name gone", got)
	}

	if hasPendingStart(exp.Status) {
		t.Errorf(
			"stored pending starts = %v, %v, %t; want none",
			exp.Status.DelayedTimers(),
			exp.Status.DelayedC2(),
			exp.Status.PostStartPending(),
		)
	}

	// stopping the experiment clears it
	useStartMM(t, &startMM{})

	if err := Stop("exp", StopWithKeepInjectionSnapshots(true)); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := experimentNamed(t, "exp").Status.Interrupted(); got != "" {
		t.Errorf("interrupted reason after Stop() = %q, want none", got)
	}
}

func TestResumeStartLeavesOtherExperimentsAlone(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)
	storeExperiment(t, "stopped", map[string]any{"startTime": "", "postStartPending": true})
	storeExperiment(t, "nothing", map[string]any{"startTime": stopTestStart})

	names, err := PendingStarts()
	if err != nil || len(names) != 0 {
		t.Errorf("PendingStarts() = %q, %v; want none", names, err)
	}

	for _, name := range []string{"stopped", "nothing"} {
		errChan := make(chan error)

		if resumed, err := ResumeStart(context.Background(), name, errChan); resumed || err != nil {
			t.Errorf("ResumeStart(%s) = %t, %v; want false, nil", name, resumed, err)
		}

		if _, open := <-errChan; open {
			t.Errorf("error channel of ResumeStart(%s) open, want it closed", name)
		}
	}
}

// Each delayed VM is dropped from the recorded pending starts as it starts,
// and the post-start apps once they ran.
func TestAfterLaunchRecordsTheStartsAsTheyHappen(t *testing.T) { //nolint:paralleltest // replaces package globals
	soon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)

	storePendingExperiment(t, map[string]string{"first": soon, "second": soon}, nil)
	useResumeMM(t, nil, nil)

	exp := experimentNamed(t, "exp")

	delayedVMStarted("exp", "first")

	if diff := cmp.Diff(map[string]string{"second": soon}, experimentNamed(t, "exp").Status.DelayedTimers()); diff != "" {
		t.Errorf("stored timers after first started mismatch (-want +got):\n%s", diff)
	}

	steps := pendingSteps{delays: map[string]time.Duration{"second": time.Millisecond}, postStart: true, persist: true}

	if _, err := afterLaunch(context.Background(), exp, false, steps, nil, nil); err != nil {
		t.Fatalf("afterLaunch() error = %v", err)
	}

	if got := experimentNamed(t, "exp"); hasPendingStart(got.Status) {
		t.Errorf("stored pending starts after afterLaunch() = %v, %t; want none", got.Status.DelayedTimers(), got.Status.PostStartPending())
	}
}

func TestPendingStepsDelayed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps pendingSteps
		want  bool
	}{
		{name: "nothing pending", steps: pendingSteps{}},
		{name: "only post-start apps", steps: pendingSteps{postStart: true, ccScript: "exp-cc.mm"}},
		{name: "empty maps", steps: pendingSteps{delays: map[string]time.Duration{}, c2s: map[string]map[string]bool{}}},
		{name: "a timer-delayed VM", steps: pendingSteps{delays: map[string]time.Duration{"a": 0}}, want: true},
		{name: "a C2-delayed VM", steps: pendingSteps{c2s: map[string]map[string]bool{"a": {"b": false}}}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.steps.delayed(); got != tc.want {
				t.Errorf("pendingSteps%+v.delayed() = %t, want %t", tc.steps, got, tc.want)
			}
		})
	}
}

func TestDueTimes(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 1, 1, 6, 0, 0, 0, time.FixedZone("", -6*60*60))

	tests := []struct {
		name   string
		delays map[string]time.Duration
		want   map[string]string
	}{
		{name: "nil", want: map[string]string{}},
		{name: "empty", delays: map[string]time.Duration{}, want: map[string]string{}},
		{
			name:   "in UTC, with fractions of a second",
			delays: map[string]time.Duration{"now": 0, "soon": 1500 * time.Millisecond, "later": time.Hour},
			want: map[string]string{
				"now": "2024-01-01T12:00:00Z", "soon": "2024-01-01T12:00:01.5Z", "later": "2024-01-01T13:00:00Z",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dueTimes(now, tc.delays)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("dueTimes() mismatch (-want +got):\n%s", diff)
			}

			// a due time parses back to now plus its delay, as planResume reads it
			for vm, due := range got {
				at, err := time.Parse(time.RFC3339Nano, due)
				if err != nil || !at.Equal(now.Add(tc.delays[vm])) {
					t.Errorf("due time of %s = %q (%v), want %v", vm, due, err, now.Add(tc.delays[vm]))
				}
			}
		})
	}
}

func TestHasPendingStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timers    map[string]string
		c2        map[string]map[string]bool
		postStart bool
		want      bool
	}{
		{name: "nothing pending"},
		{name: "empty maps", timers: map[string]string{}, c2: map[string]map[string]bool{}},
		{name: "a timer", timers: map[string]string{"a": "2024-01-01T00:00:00Z"}, want: true},
		{name: "a C2 wait", c2: map[string]map[string]bool{"a": {"b": true}}, want: true},
		{name: "the post-start apps", postStart: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := hasPendingStart(pendingStatus(tc.timers, tc.c2, tc.postStart).Status); got != tc.want {
				t.Errorf("hasPendingStart() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestAllPresent(t *testing.T) {
	t.Parallel()

	states := map[string]string{"a": "RUNNING", "b": "BUILDING"}

	tests := []struct {
		name   string
		others map[string]bool
		want   bool
	}{
		{name: "waits on nothing", want: true},
		{name: "waits on VMs minimega holds", others: map[string]bool{"a": false, "b": true}, want: true},
		{name: "waits on a VM that is gone", others: map[string]bool{"a": false, "gone": false}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := allPresent(tc.others, states); got != tc.want {
				t.Errorf("allPresent(%v) = %t, want %t", tc.others, got, tc.want)
			}
		})
	}
}

// The lock of one experiment serializes its holders and leaves other experiments' locks free.
func TestLockStatus(t *testing.T) {
	t.Parallel()

	name := "resume-test-" + t.Name()

	var (
		wg    sync.WaitGroup
		count int // the race detector fails the test unless the lock serializes the writes
	)

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 100 {
				unlock := lockStatus(name)
				count++

				unlock()
			}
		}()
	}

	wg.Wait()

	if count != 800 {
		t.Errorf("count after 800 locked increments = %d, want 800", count)
	}

	unlock := lockStatus(name)
	defer unlock()

	polltest.Within(t, "locking another experiment's status", func() { lockStatus(name + "-other")() })
}

// resumeStored reads experiment exp's status back from the store.
func resumeStored(t *testing.T) ifaces.ExperimentStatus {
	t.Helper()

	return experimentNamed(t, "exp").Status
}

func TestUpdateStatus(t *testing.T) { //nolint:paralleltest // replaces the package store
	failed := errors.New("store is read-only")

	tests := []struct {
		name        string
		exp         string
		updateErr   error
		change      bool
		wantErr     error  // nil for none
		wantText    string // part of the error
		wantUpdates int64
		wantPending bool // whether the stored status still records the post-start apps
	}{
		{name: "writes a change", exp: "exp", change: true, wantUpdates: 1},
		{name: "writes nothing when nothing changed", exp: "exp", wantPending: true},
		{
			name: "an experiment not in the store", exp: "missing", change: true, wantErr: store.ErrNotExist,
			wantText: "getting experiment missing from store", wantPending: true,
		},
		{
			name: "a store that fails the write", exp: "exp", change: true, updateErr: failed, wantErr: failed,
			wantText: "updating experiment exp", wantUpdates: 1, wantPending: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			storeExperiment(t, "exp", map[string]any{"startTime": stopTestStart, "postStartPending": true})

			s.updateErr = tc.updateErr

			err := updateStatus(tc.exp, func(st ifaces.ExperimentStatus) bool {
				st.SetDelayedStarts(nil, nil, false)

				return tc.change
			})

			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Errorf("updateStatus(%s) error = %v, want %v", tc.exp, err, tc.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("updateStatus(%s) error = %q, want it to contain %q", tc.exp, err, tc.wantText)
			}

			if got := s.updates.Load(); got != tc.wantUpdates {
				t.Errorf("updateStatus(%s) store updates = %d, want %d", tc.exp, got, tc.wantUpdates)
			}

			if got := resumeStored(t).PostStartPending(); got != tc.wantPending {
				t.Errorf("stored post-start pending after updateStatus(%s) = %t, want %t", tc.exp, got, tc.wantPending)
			}
		})
	}
}

// An experiment whose spec does not decode is not written.
func TestUpdateStatusUndecodable(t *testing.T) { //nolint:paralleltest // replaces the package store
	s := useRecoverStore(t)

	err := store.Create(&store.Config{
		Version: "phenix.sandia.gov/v0", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "bad"},
		Spec: map[string]any{}, Status: map[string]any{},
	})
	if err != nil {
		t.Fatalf("store.Create(experiment bad) error = %v", err)
	}

	called := false

	err = updateStatus("bad", func(ifaces.ExperimentStatus) bool {
		called = true

		return true
	})
	if err == nil || !strings.Contains(err.Error(), "decoding experiment bad") {
		t.Errorf("updateStatus(bad) error = %v, want a decoding error", err)
	}

	if called || s.updates.Load() != 0 {
		t.Errorf("updateStatus(bad) called the change %t and updated %d times, want neither", called, s.updates.Load())
	}
}

func TestDelayedVMStarted(t *testing.T) { //nolint:paralleltest // replaces the package store
	const due = "2024-01-01T00:00:00Z"

	tests := []struct {
		name        string
		exp, host   string
		wantTimers  map[string]string
		wantC2      map[string]map[string]bool
		wantUpdates int64
	}{
		{
			name: "drops a timer-delayed VM", exp: "exp", host: "timed",
			wantTimers:  map[string]string{"other": due},
			wantC2:      map[string]map[string]bool{"client": {"server": true}},
			wantUpdates: 1,
		},
		{
			name: "drops a C2-delayed VM", exp: "exp", host: "client",
			wantTimers:  map[string]string{"timed": due, "other": due},
			wantUpdates: 1,
		},
		{
			name: "writes nothing for a VM not pending", exp: "exp", host: "server",
			wantTimers: map[string]string{"timed": due, "other": due},
			wantC2:     map[string]map[string]bool{"client": {"server": true}},
		},
		{
			name: "only logs for an experiment not in the store", exp: "missing", host: "timed",
			wantTimers: map[string]string{"timed": due, "other": due},
			wantC2:     map[string]map[string]bool{"client": {"server": true}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			storeExperiment(t, "exp", map[string]any{
				"startTime":        stopTestStart,
				"delayedTimers":    map[string]any{"timed": due, "other": due},
				"delayedC2":        map[string]any{"client": map[string]any{"server": true}},
				"postStartPending": true,
			})

			delayedVMStarted(tc.exp, tc.host)

			got := resumeStored(t)

			if diff := cmp.Diff(tc.wantTimers, got.DelayedTimers(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("stored timers mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantC2, got.DelayedC2(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("stored C2 waits mismatch (-want +got):\n%s", diff)
			}

			if !got.PostStartPending() {
				t.Error("stored post-start pending = false, want it kept")
			}

			if n := s.updates.Load(); n != tc.wantUpdates {
				t.Errorf("delayedVMStarted(%s, %s) store updates = %d, want %d", tc.exp, tc.host, n, tc.wantUpdates)
			}
		})
	}
}

// VMs that start at once each drop their own pending start, losing none of the others' updates.
func TestDelayedVMStartedConcurrently(t *testing.T) { //nolint:paralleltest // replaces the package store
	timers := make(map[string]string, 16)
	for i := range 16 {
		timers[fmt.Sprintf("vm%02d", i)] = "2024-01-01T00:00:00Z"
	}

	storePendingExperiment(t, timers, nil)

	var wg sync.WaitGroup

	for vm := range timers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			delayedVMStarted("exp", vm)
		}()
	}

	wg.Wait()

	got := resumeStored(t)
	if len(got.DelayedTimers()) != 0 || !got.PostStartPending() {
		t.Errorf("stored status after every VM started: timers %v, post-start %t; want no timers and post-start pending",
			got.DelayedTimers(), got.PostStartPending())
	}
}

func TestSetPending(t *testing.T) { //nolint:paralleltest // replaces the package store
	timers := map[string]string{"a": "2024-01-01T00:00:00Z"}
	c2 := map[string]map[string]bool{"b": {"a": false}}

	t.Run("records in memory and in the store", func(t *testing.T) {
		storePendingExperiment(t, nil, nil)

		exp := experimentNamed(t, "exp")
		setPending(exp, timers, c2, false)

		for where, st := range map[string]ifaces.ExperimentStatus{"in memory": exp.Status, "stored": resumeStored(t)} {
			if diff := cmp.Diff(timers, st.DelayedTimers()); diff != "" {
				t.Errorf("%s timers mismatch (-want +got):\n%s", where, diff)
			}

			if diff := cmp.Diff(c2, st.DelayedC2()); diff != "" {
				t.Errorf("%s C2 waits mismatch (-want +got):\n%s", where, diff)
			}

			if st.PostStartPending() {
				t.Errorf("%s post-start pending = true, want false", where)
			}
		}
	})

	t.Run("records in memory an experiment not in the store", func(t *testing.T) {
		storetest.Use(t)

		exp := pendingStatus(nil, nil, false)
		setPending(exp, timers, nil, true)

		if diff := cmp.Diff(timers, exp.Status.DelayedTimers()); diff != "" || !exp.Status.PostStartPending() {
			t.Errorf("in-memory status: timers diff %s, post-start %t; want the timers and post-start", diff,
				exp.Status.PostStartPending())
		}
	})
}

// finishStart closes the channel after the steps, and stops the experiment after a failed step unless its caller
// canceled the delayed VM starts, as the web UI does before it calls Stop itself.
func TestFinishStart(t *testing.T) { //nolint:paralleltest // replaces package globals
	expired, cancelExpired := context.WithTimeout(context.Background(), 0)
	defer cancelExpired()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	hour := map[string]time.Duration{"vm": time.Hour}

	tests := []struct {
		name        string
		ctx         context.Context //nolint:containedctx // each case's context
		dryrun      bool
		steps       pendingSteps
		readErr     error
		want        []string // the start of each error sent
		wantCleared []string
	}{
		{name: "nothing left to run", ctx: context.Background(), dryrun: true},
		{
			name: "a failed cc script stops the experiment", ctx: context.Background(),
			steps:   pendingSteps{ccScript: "exp-cc.mm"},
			readErr: errors.New("bad cc script"),
			want: []string{
				"reading minimega cc script: bad cc script",
				"stopping experiment: getting experiment exp from store",
			},
		},
		{
			name: "delayed VMs the caller canceled leave the stop to it", ctx: canceled,
			steps: pendingSteps{delays: hour},
			want:  []string{"handling delayed VMs: "},
		},
		{
			name: "delayed VMs out of time stop the experiment", ctx: expired,
			steps:       pendingSteps{delays: hour},
			want:        []string{"handling delayed VMs: ", "stopping experiment: getting experiment exp from store"},
			wantCleared: []string{"exp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t) // without exp, so a Stop fails at once
			fake := useStartMM(t, &startMM{readErr: tc.readErr})

			exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Spec.SetExperimentName("exp")
			exp.Spec.SetTopology(&v1.TopologySpec{
				NodesF: []*v1.Node{{GeneralF: &v1.General{HostnameF: "vm"}, CommandsF: []string{"cc exec ls"}}},
			})

			errChan := make(chan error, 4)
			finishStart(tc.ctx, exp, tc.dryrun, tc.steps, errChan, nil)

			errs := waitClosed(t, errChan)
			if len(errs) != len(tc.want) {
				t.Fatalf("finishStart() errors = %v, want %d starting with %q", errs, len(tc.want), tc.want)
			}

			for i, err := range errs {
				if !strings.HasPrefix(err.Error(), tc.want[i]) {
					t.Errorf("finishStart() error %d = %q, want it to start with %q", i, err, tc.want[i])
				}
			}

			if diff := cmp.Diff(tc.wantCleared, fake.cleared, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("finishStart() cleared namespaces mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ResumeStart closes the channel and resumes nothing when it cannot read or record the experiment, or when
// minimega cannot list the VMs.
func TestResumeStartFailures(t *testing.T) { //nolint:paralleltest // replaces package globals
	failed := errors.New("store is read-only")
	soon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)

	tests := []struct {
		name            string
		exp             string
		start           string
		statesErr       error
		updateErr       error
		wantErr         string // part of the error, "" for none
		wantInterrupted string // part of the stored reason, "" for none
	}{
		{name: "an experiment not in the store", exp: "missing", start: stopTestStart, wantErr: "getting experiment missing"},
		{name: "a dry run", exp: "exp", start: stopTestStart + "-DRYRUN"},
		{
			name: "minimega cannot list the VMs", exp: "exp", start: stopTestStart,
			statesErr:       errors.New("minimega unreachable"),
			wantInterrupted: "minimega could not list the VMs: minimega unreachable",
		},
		{
			name: "the plan cannot be recorded", exp: "exp", start: stopTestStart, updateErr: failed,
			wantErr: "updating experiment exp: " + failed.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			storeExperiment(t, "exp", map[string]any{
				"startTime": tc.start, "delayedTimers": map[string]any{"vm": soon}, "postStartPending": true,
			})

			received := useResumeMM(t, map[string]string{"vm": "BUILDING"}, tc.statesErr)
			s.updateErr = tc.updateErr

			errChan := make(chan error)

			resumed, err := ResumeStart(context.Background(), tc.exp, errChan)
			if resumed {
				t.Errorf("ResumeStart(%s) resumed, want not", tc.exp)
			}

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("ResumeStart(%s) error = %v, want nil", tc.exp, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("ResumeStart(%s) error = %v, want one containing %q", tc.exp, err, tc.wantErr)
			}

			if errs := waitClosed(t, errChan); len(errs) != 0 {
				t.Errorf("ResumeStart(%s) sent errors %v, want the channel closed empty", tc.exp, errs)
			}

			if got := received(); len(got) != 0 {
				t.Errorf("ResumeStart(%s) sent %q, want nothing", tc.exp, mmtest.Bases(got))
			}

			got := resumeStored(t).Interrupted()
			if (got == "") != (tc.wantInterrupted == "") || !strings.Contains(got, tc.wantInterrupted) {
				t.Errorf("stored interrupted reason = %q, want it to contain %q", got, tc.wantInterrupted)
			}
		})
	}
}

func TestPendingStartsReportsTheStore(t *testing.T) { //nolint:paralleltest // replaces the package store
	failed := errors.New("store is down")

	s := useRecoverStore(t)
	s.listErr = failed

	names, err := PendingStarts()
	if names != nil || !errors.Is(err, failed) || !strings.Contains(err.Error(), "listing running experiments") {
		t.Errorf("PendingStarts() = %q, %v; want nil and the listing error", names, err)
	}
}
