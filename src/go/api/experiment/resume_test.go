package experiment

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
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

// Whatever the VMs' states, a plan re-arms only pending VMs minimega holds unstarted, never with a negative delay,
// runs post-start apps only when nothing is interrupted, and is interrupted exactly when a pending VM is gone.
func TestPlanResumeProperties(t *testing.T) {
	t.Parallel()

	var (
		now   = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		vms   = rapid.SampledFrom([]string{"a", "b", "c", "d", "e"})
		state = rapid.SampledFrom([]string{"BUILDING", "RUNNING", "PAUSED", "QUIT", "ERROR"})
	)

	rapid.Check(t, func(t *rapid.T) {
		var (
			timers = rapid.MapOf(vms, rapid.Map(rapid.IntRange(-120, 120), func(s int) string {
				return now.Add(time.Duration(s) * time.Second).Format(time.RFC3339Nano)
			})).Draw(t, "timers")
			c2     = rapid.MapOf(vms, rapid.MapOfN(vms, rapid.Bool(), 1, 2)).Draw(t, "c2")
			states = rapid.MapOf(vms, state).Draw(t, "states")
		)

		// a VM has one kind of delay
		for vm := range timers {
			delete(c2, vm)
		}

		plan := planResume(pendingStatus(timers, c2, true).Status, states, nil, now)

		for vm, delay := range plan.delays {
			if _, ok := timers[vm]; !ok || states[vm] != mmStateBuilding || delay < 0 {
				t.Fatalf("plan re-arms %s after %v; pending %t, state %q", vm, delay, ok, states[vm])
			}
		}

		for vm := range plan.c2s {
			if _, ok := c2[vm]; !ok || states[vm] != mmStateBuilding {
				t.Fatalf("plan re-arms C2-delayed %s; pending %t, state %q", vm, ok, states[vm])
			}
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

	if _, err := afterLaunch(context.Background(), exp, false, steps); err != nil {
		t.Fatalf("afterLaunch() error = %v", err)
	}

	if got := experimentNamed(t, "exp"); hasPendingStart(got.Status) {
		t.Errorf("stored pending starts after afterLaunch() = %v, %t; want none", got.Status.DelayedTimers(), got.Status.PostStartPending())
	}
}
