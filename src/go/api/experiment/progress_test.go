package experiment

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm/mmtest"
	"phenix/util/notes"
	"phenix/util/polltest"
)

// approx compares percents that the phase weights give as sums of divisions.
var approx = cmpopts.EquateApprox(0, 1e-9) //nolint:gochecknoglobals // a cmp option

// The percent of a phase is the weights of the phases before it, plus its own weight times the part of its steps
// done, over the weights of the phases with work. A phase with no work drops out, so the other phases fill the
// progress, and a phase with no steps counts as done.
func TestProgressPlanPercent(t *testing.T) {
	t.Parallel()

	everything := map[string]int{
		PhasePreStartApps: 4, PhasePrepare: 4, PhaseLaunchVMs: 10, PhaseStartVMs: 3, PhasePostStartApps: 2,
	}
	dryRun := map[string]int{PhasePreStartApps: 4, PhasePrepare: 2, PhasePostStartApps: 2}

	tests := []struct {
		name        string
		operation   string
		work        map[string]int
		phase       string
		done, total int
		want        float64
	}{
		{"the first phase begins at 0", OperationStart, everything, PhasePreStartApps, 0, 4, 0},
		{"half of the first phase", OperationStart, everything, PhasePreStartApps, 2, 4, 0.075},
		{"the launch begins after the apps and the preparation", OperationStart, everything, PhaseLaunchVMs, 0, 10, 0.25},
		{"three of ten VMs launched", OperationStart, everything, PhaseLaunchVMs, 3, 10, 0.25 + 0.55*0.3},
		{"the post-start apps begin at 85%", OperationStart, everything, PhasePostStartApps, 0, 2, 0.85},
		{"a phase with no steps is done", OperationStart, everything, PhaseStartVMs, 0, 0, 0.85},
		{"a count above the total is the total", OperationStart, everything, PhaseLaunchVMs, 12, 10, 0.8},
		{"a dry run has no launch", OperationStart, dryRun, PhasePostStartApps, 0, 2, 25.0 / 40},
		{"a dry run gives a launch no weight", OperationStart, dryRun, PhaseLaunchVMs, 5, 10, 25.0 / 40},
		{"done is complete", OperationStart, dryRun, PhaseDone, 0, 0, 1},
		{"a phase of another operation gives 0", OperationStart, everything, PhaseStopVMs, 1, 1, 0},
		{"a stop without apps begins with its VMs", OperationStop, map[string]int{PhaseStopVMs: 1, PhaseCleanup: 3}, PhaseStopVMs, 0, 1, 0},
		{
			"a stop without apps cleans up at 50 of 60",
			OperationStop,
			map[string]int{PhaseStopVMs: 1, PhaseCleanup: 3},
			PhaseCleanup,
			0,
			3,
			50.0 / 60,
		},
		{
			"a stop cleans up its apps first",
			OperationStop,
			map[string]int{PhaseCleanupApps: 2, PhaseStopVMs: 1, PhaseCleanup: 3},
			PhaseCleanupApps,
			1,
			2,
			0.2,
		},
		{"no work gives 0", OperationStop, map[string]int{}, PhaseCleanup, 1, 1, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			phases := startPhases()
			if tc.operation == OperationStop {
				phases = stopPhases()
			}

			plan := newProgressPlan(tc.operation, phases, tc.work)

			if got := plan.percent(tc.phase, tc.done, tc.total); !cmp.Equal(got, tc.want, approx) {
				t.Errorf("percent(%s, %d, %d) = %v, want %v", tc.phase, tc.done, tc.total, got, tc.want)
			}
		})
	}
}

// genProgressWork draws the steps of each phase of an operation, from none to a few.
func genProgressWork(phases []phaseWeight) *rapid.Generator[map[string]int] {
	return rapid.Custom(func(t *rapid.T) map[string]int {
		work := make(map[string]int, len(phases))

		for _, p := range phases {
			work[p.phase] = rapid.IntRange(0, 5).Draw(t, p.phase)
		}

		return work
	})
}

// As an operation goes through its phases in order and through the steps of each, its percent stays in [0, 1] and
// never goes down. A phase with no work adds nothing, and the operation reaches 1 once its last phase with work is
// done.
func TestProgressPlanProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		phases := startPhases()
		if rapid.Bool().Draw(t, "stop") {
			phases = stopPhases()
		}

		work := genProgressWork(phases).Draw(t, "work")
		plan := newProgressPlan("op", phases, work)

		last := 0.0

		for _, p := range phases {
			total := work[p.phase]

			for done := 0; done <= total; done++ {
				got := plan.percent(p.phase, done, total)

				if got < 0 || got > 1 || got < last {
					t.Fatalf("percent(%s, %d, %d) = %v after %v, want a value in [%v, 1]", p.phase, done, total, got, last, last)
				}

				if total == 0 && got != last {
					t.Fatalf("percent(%s) of a phase with no work = %v, want %v as before it", p.phase, got, last)
				}

				last = got
			}
		}

		if plan.sum > 0 && math.Abs(last-1) > 1e-9 {
			t.Fatalf("percent after the last phase = %v, want 1", last)
		}
	})
}

// progressLog records the progress a reporter sends and marks.
type progressLog struct {
	mu           sync.Mutex
	sent, marked []ifaces.OperationProgress
}

func (l *progressLog) send(p ifaces.OperationProgress) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sent = append(l.sent, p)
}

func (l *progressLog) sentCopy() []ifaces.OperationProgress {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.sent)
}

func (l *progressLog) mark(p ifaces.OperationProgress) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.marked = append(l.marked, p)
}

// steps is each progress as phase, done and total, and step, without the percent.
func steps(ps []ifaces.OperationProgress) []string {
	out := make([]string, 0, len(ps))

	for _, p := range ps {
		out = append(out, progressStep(p))
	}

	return out
}

func progressStep(p ifaces.OperationProgress) string {
	return fmt.Sprintf("%s %d/%d %s", p.Phase, p.Done, p.Total, p.Step)
}

// A reporter sends each change, and marks only phase changes and the steps asked to. It drops a step that changes
// nothing, a poll count that is not higher or is of another phase, and marks nothing once marking stops. A nil
// reporter does nothing.
func TestProgressReporter(t *testing.T) {
	t.Parallel()

	log := new(progressLog)
	plan := newProgressPlan(OperationStart, startPhases(), map[string]int{PhasePreStartApps: 1, PhaseLaunchVMs: 4})
	rep := newProgressReporter(plan, log.send, log.mark)

	rep.begin(PhasePreStartApps, 1, "ntp")
	rep.step(0, "ntp", true)
	rep.begin(PhaseLaunchVMs, 4, "launch")
	rep.count(PhaseLaunchVMs, 2)
	rep.count(PhaseLaunchVMs, 1)
	rep.count(PhasePrepare, 3)
	rep.count(PhaseLaunchVMs, 9)
	rep.stopMarking()
	rep.begin(PhasePostStartApps, 1, "soh")
	rep.finish()

	wantSent := []string{
		"pre-start-apps 0/1 ntp", "launch-vms 0/4 launch", "launch-vms 2/4 launch", "launch-vms 4/4 launch",
		"post-start-apps 0/1 soh", "done 0/0 ",
	}

	if diff := cmp.Diff(wantSent, steps(log.sent)); diff != "" {
		t.Errorf("sent progress mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"pre-start-apps 0/1 ntp", "launch-vms 0/4 launch"}, steps(log.marked)); diff != "" {
		t.Errorf("marked progress mismatch (-want +got):\n%s", diff)
	}

	percents := make([]float64, 0, len(log.sent))
	for _, p := range log.sent {
		percents = append(percents, p.Percent)
	}

	if diff := cmp.Diff([]float64{0, 15.0 / 70, 15.0/70 + 55.0/70/2, 1, 1, 1}, percents, approx); diff != "" {
		t.Errorf("sent percents mismatch (-want +got):\n%s", diff)
	}

	var none *progressReporter

	none.begin(PhasePrepare, 1, "")
	none.step(1, "", true)
	none.count(PhasePrepare, 1)
	none.finish()
	none.stopMarking()
}

// planLaunch starts at once only the bootable VMs that are not external and not delayed by the user, by C2 or by a
// timer, and lists them all for minimega's `vm start all` when no VM waits.
func TestPlanLaunch(t *testing.T) { //nolint:paralleltest // replaces the package store
	image := "a.qc2"

	tests := []struct {
		name      string
		nodes     []map[string]any
		wantVMs   []string
		wantStart []string
	}{
		{
			name:    "every VM at once",
			nodes:   []map[string]any{launchNode("a", image, nil), launchNode("b", image, nil)},
			wantVMs: []string{"a", "b"},
		},
		{
			name: "leaves out delayed and external VMs",
			nodes: []map[string]any{
				launchNode("a", image, nil),
				launchNode("user", image, map[string]any{"delay": map[string]any{"user": true}}),
				launchNode("c2", image, map[string]any{"delay": map[string]any{"c2": []any{map[string]any{"hostname": "a"}}}}),
				launchNode("timer", image, map[string]any{"delay": map[string]any{"timer": "5m"}}),
				{"type": "VirtualMachine", "general": map[string]any{"hostname": "out"}, "external": true},
			},
			wantVMs:   []string{"a"},
			wantStart: []string{"a"},
		},
		{
			name:      "no VM at once",
			nodes:     []map[string]any{launchNode("user", image, map[string]any{"delay": map[string]any{"user": true}})},
			wantVMs:   []string{},
			wantStart: []string{},
		},
		{name: "no VMs", nodes: nil, wantVMs: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			storeLaunchExperiment(t, nil, tc.nodes...)

			plan := planLaunch(experimentNamed(t, "exp").Spec)

			if diff := cmp.Diff(tc.wantVMs, plan.vms); diff != "" {
				t.Errorf("planLaunch() VMs mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantStart, plan.start); diff != "" || (tc.wantStart == nil) != (plan.start == nil) {
				t.Errorf("planLaunch() start = %#v, want %#v", plan.start, tc.wantStart)
			}
		})
	}
}

// notMonotonic returns an error when the percent of a progress in ps is lower than the one before it, or out of
// [0, 1].
func notMonotonic(ps []ifaces.OperationProgress) error {
	last := 0.0

	for _, p := range ps {
		if p.Percent < last || p.Percent > 1 {
			return fmt.Errorf("progress %s at %v after %v, want a percent in [%v, 1]", progressStep(p), p.Percent, last, last)
		}

		last = p.Percent
	}

	return nil
}

// markerStore is a BoltDB that records the progress of the start and stop markers of each update of experiment exp,
// leaving out an update that changes neither.
type markerStore struct {
	*store.BoltDB

	mu       sync.Mutex
	starting []string
	stopping []string
}

// markerText is how markerStore records m: "none", "no progress", or the phase, count and step of its progress.
func markerText(m ifaces.StartMarker) string {
	switch {
	case m.IsZero():
		return "none"
	case m.Progress.Phase == "":
		return "no progress"
	default:
		return progressStep(m.Progress)
	}
}

func (s *markerStore) Update(c *store.Config) error {
	if exp, err := types.DecodeExperimentFromConfig(*c); err == nil && c.Metadata.Name == "exp" {
		s.mu.Lock()

		if text := markerText(exp.Status.Starting()); len(s.starting) == 0 || s.starting[len(s.starting)-1] != text {
			s.starting = append(s.starting, text)
		}

		if text := markerText(exp.Status.Stopping()); len(s.stopping) == 0 || s.stopping[len(s.stopping)-1] != text {
			s.stopping = append(s.stopping, text)
		}

		s.mu.Unlock()
	}

	return s.BoltDB.Update(c) //nolint:wrapcheck // the store's own error
}

// appStepTexts are the progress texts of the apps of a phase with total steps.
func appStepTexts(phase string, apps []string, total int) []string {
	out := make([]string, 0, len(apps))

	for i, name := range apps {
		out = append(out, fmt.Sprintf("%s %d/%d %s", phase, i, total, name))
	}

	return out
}

// A start reports each phase and step in order, with a percent that never goes down, and ends with done. It
// records each phase change and app boundary in its start marker, which the record of the start clears. A dry run
// has no launch-vms or start-vms phase, and a start with no VM at once has a launch-vms phase with no steps.
func TestStartReportsProgress(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name   string
		dryrun bool
		nodes  func(image string) []map[string]any
		vms    int // the VMs to launch at once
	}{
		{
			name: "a start",
			nodes: func(image string) []map[string]any {
				return []map[string]any{
					launchNode("a", image, nil),
					launchNode("b", image, map[string]any{"delay": map[string]any{"user": true}}),
				}
			},
			vms: 1,
		},
		{
			name: "a start of delayed VMs only",
			nodes: func(image string) []map[string]any {
				return []map[string]any{launchNode("b", image, map[string]any{"delay": map[string]any{"user": true}})}
			},
		},
		{
			name:   "a dry run",
			dryrun: true,
			nodes: func(image string) []map[string]any {
				return []map[string]any{launchNode("a", image, nil)}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			markers := &markerStore{BoltDB: storetest.NewBolt(t)}
			storetest.Use(t, markers)

			storeLaunchExperiment(t, nil, tc.nodes(launchImage(t))...)
			useDeletedFiles(t)
			useLaunchMM(t, defaultLaunchReplies())

			exp := experimentNamed(t, "exp")
			pre := app.PlannedApps(exp, app.Stage(app.ActionPreStart), app.DryRun(tc.dryrun))
			post := app.PlannedApps(exp, app.Stage(app.ActionPostStart), app.DryRun(tc.dryrun))

			log := new(progressLog)

			err := Start(notes.Context(t.Context(), false), StartWithName("exp"), StartWithDryRun(tc.dryrun),
				StartWithProgress(log.send))
			if err != nil {
				t.Fatalf("Start(exp) error = %v", err)
			}

			want := appStepTexts("pre-start-apps", pre, len(pre))
			marked := append([]string{"no progress"}, want...)

			if tc.dryrun {
				want = append(want, "prepare 0/2 disk-check", "prepare 1/2 scripts")
				marked = append(marked, "prepare 0/2 disk-check")
			} else {
				want = append(want, "prepare 0/4 disk-check", "prepare 1/4 scripts", "prepare 2/4 snapshot-cleanup",
					"prepare 3/4 minimega-script", fmt.Sprintf("launch-vms 0/%d launch", tc.vms),
					"start-vms 0/2 vm-start", "start-vms 1/2 vm-hosts-and-vlans")
				marked = append(marked, "prepare 0/4 disk-check", fmt.Sprintf("launch-vms 0/%d launch", tc.vms),
					"start-vms 0/2 vm-start")
			}

			postSteps := appStepTexts("post-start-apps", post, len(post))
			want = append(append(want, postSteps...), "done 0/0 ")
			marked = append(append(marked, postSteps...), "none")

			if diff := cmp.Diff(want, steps(log.sent)); diff != "" {
				t.Errorf("Start(exp) progress mismatch (-want +got):\n%s", diff)
			}

			if err := notMonotonic(log.sent); err != nil {
				t.Errorf("Start(exp) %v", err)
			}

			if diff := cmp.Diff(marked, markers.starting); diff != "" {
				t.Errorf("Start(exp) start markers stored mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A start whose post-start work runs in the background records the start in its start-vms phase, ends its marker
// there, and reports the post-start apps and done after Start returns.
func TestStartReportsBackgroundProgress(t *testing.T) { //nolint:paralleltest // replaces package globals
	markers := &markerStore{BoltDB: storetest.NewBolt(t)}
	storetest.Use(t, markers)

	storeLaunchExperiment(t, nil, launchNode("a", launchImage(t), nil))
	useDeletedFiles(t)
	useLaunchMM(t, defaultLaunchReplies())

	post := app.PlannedApps(experimentNamed(t, "exp"), app.Stage(app.ActionPostStart))

	var (
		log = new(progressLog)
		ch  = make(chan error)
	)

	err := Start(notes.Context(t.Context(), false), StartWithName("exp"), StartWithErrorChannel(ch),
		StartWithProgress(log.send))
	if err != nil {
		t.Fatalf("Start(exp) error = %v", err)
	}

	for err := range ch {
		t.Errorf("Start(exp) background error = %v", err)
	}

	got := steps(log.sent)

	i := slices.Index(got, "start-vms 0/3 vm-start")
	if i < 0 {
		t.Fatalf("Start(exp) progress = %q, want a start-vms phase of 3 steps", got)
	}

	want := append([]string{"start-vms 0/3 vm-start", "start-vms 1/3 vm-hosts-and-vlans", "start-vms 2/3 record"},
		appStepTexts("post-start-apps", post, len(post))...)
	want = append(want, "done 0/0 ")

	if diff := cmp.Diff(want, got[i:]); diff != "" {
		t.Errorf("Start(exp) progress from start-vms mismatch (-want +got):\n%s", diff)
	}

	if err := notMonotonic(log.sent); err != nil {
		t.Errorf("Start(exp) %v", err)
	}

	if last := markers.starting[len(markers.starting)-1]; last != "none" {
		t.Errorf("start marker after the start = %q, want none", last)
	}

	if slices.ContainsFunc(markers.starting, func(m string) bool { return len(m) > 4 && m[:4] == "post" }) {
		t.Errorf("start markers stored = %q, want none of the post-start apps after the record", markers.starting)
	}
}

// pollLaunch counts the VMs minimega launched into the launch-vms phase until it is stopped, and asks nothing when
// no one gets the progress.
func TestPollLaunch(t *testing.T) { //nolint:paralleltest // replaces package globals
	var (
		mu    sync.Mutex
		polls int
	)

	useMM(t, launchMM{})

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != "vm info" {
			return nil
		}

		mu.Lock()
		defer mu.Unlock()

		// the first poll finds nothing launched, the second one VM, and the later ones both
		rows := [][]string{{"a", "BUILDING", "0"}, {"b", "BUILDING", "0"}}

		switch {
		case polls > 1:
			rows = [][]string{{"a", "BUILDING", "11"}, {"b", "RUNNING", "12"}}
		case polls == 1:
			rows = [][]string{{"a", "BUILDING", "11"}, {"b", "BUILDING", "0"}}
		}

		polls++

		return []*minicli.Response{mmtest.Tabular("head", []string{"name", "state", "pid"}, rows...)}
	})

	log := new(progressLog)
	rep := newProgressReporter(newProgressPlan(OperationStart, startPhases(), map[string]int{PhaseLaunchVMs: 2}), log.send, log.mark)
	rep.begin(PhaseLaunchVMs, 2, "launch")

	stop := pollLaunch(context.Background(), "exp", []string{"a", "b"}, rep, time.Millisecond)

	polltest.Until(t, "the launch count reaching 2", func() bool { return len(log.sentCopy()) >= 3 })

	stop()

	if diff := cmp.Diff(
		[]string{"launch-vms 0/2 launch", "launch-vms 1/2 launch", "launch-vms 2/2 launch"},
		steps(log.sentCopy()),
	); diff != "" {
		t.Errorf("pollLaunch() progress mismatch (-want +got):\n%s", diff)
	}

	if len(log.marked) != 1 {
		t.Errorf("pollLaunch() marked %d times, want only the phase begin", len(log.marked))
	}

	silent := newProgressReporter(newProgressPlan(OperationStart, startPhases(), nil), nil, log.mark)
	pollLaunch(context.Background(), "exp", []string{"a"}, silent, time.Millisecond)()
}

// A stop reports its phases in order, records them in its stop marker and clears the marker at its end. A stop that
// keeps the snapshots has no snapshot step.
func TestStopReportsProgress(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name string
		keep bool
		want []string // after the cleanup apps
	}{
		{
			name: "deletes the snapshots",
			want: []string{"stop-vms 0/1 clear-namespace", "cleanup 0/3 snapshots", "cleanup 1/3 bridge", "cleanup 2/3 record"},
		},
		{
			name: "keeps the snapshots", keep: true,
			want: []string{"stop-vms 0/1 clear-namespace", "cleanup 0/2 bridge", "cleanup 1/2 record"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			markers := &markerStore{BoltDB: storetest.NewBolt(t)}
			storetest.Use(t, markers)
			storeRunningVMExperiment(t)

			useStartMM(t, &startMM{})
			useDeletedFiles(t)

			cleanup := app.PlannedApps(experimentNamed(t, "exp"), app.Stage(app.ActionCleanup))
			log := new(progressLog)

			if err := Stop("exp", StopWithKeepInjectionSnapshots(tc.keep), StopWithProgress(log.send)); err != nil {
				t.Fatalf("Stop(exp) error = %v", err)
			}

			apps := appStepTexts("cleanup-apps", cleanup, len(cleanup))

			if diff := cmp.Diff(append(apps, tc.want...), steps(log.sent)); diff != "" {
				t.Errorf("Stop(exp) progress mismatch (-want +got):\n%s", diff)
			}

			if err := notMonotonic(log.sent); err != nil {
				t.Errorf("Start(exp) %v", err)
			}

			if last := log.sent[len(log.sent)-1]; last.Operation != OperationStop {
				t.Errorf("Stop(exp) progress operation = %q, want %q", last.Operation, OperationStop)
			}

			marked := append(append([]string{"none"}, apps...), "stop-vms 0/1 clear-namespace", tc.want[1], "none")
			if diff := cmp.Diff(marked, markers.stopping); diff != "" {
				t.Errorf("Stop(exp) stop markers stored mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// storeRunningVMExperiment stores experiment exp, started at stopTestStart, with one VM.
func storeRunningVMExperiment(t *testing.T) {
	t.Helper()

	storeLaunchExperiment(t, nil, launchNode("a", "a.qc2", nil))

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment exp) error = %v", err)
	}

	c.Status = map[string]any{"startTime": stopTestStart}
	if err := store.Update(c); err != nil {
		t.Fatalf("store.Update(experiment exp) error = %v", err)
	}
}
