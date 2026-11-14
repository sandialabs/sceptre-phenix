package scorch

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

// statusPipeline builds, as getPipeline does, a pipeline whose stages have the
// given components, with a loop node when loop is set.
func statusPipeline(stages map[string][]string, loop bool) *pipeline {
	pl := newPipeline("exp", "run", 0, 0)

	next := pl.stop
	if loop {
		next = &node{Name: stageLoop, Status: statusUnknown}
		next.addEdge(pl.stop, 0)

		pl.addNode("", next)
		pl.loop = next
	}

	targets := map[string]*node{stageConfigure: pl.start, stageStart: next, stageStop: pl.cleanup, stageCleanup: pl.done}

	for _, stage := range []string{stageConfigure, stageStart, stageStop, stageCleanup} {
		if len(stages[stage]) == 0 {
			pl.addComponentToStage(stage, targets[stage])

			continue
		}

		for _, name := range stages[stage] {
			n := &node{Name: name, Status: statusUnknown}
			n.addEdge(targets[stage], 0)

			pl.addNode(stage, n)
			pl.addComponentToStage(stage, n)
		}
	}

	return pl
}

// statusComponents are two components in each stage, with names unique in
// the pipeline.
var statusComponents = map[string][]string{ //nolint:gochecknoglobals // read-only test data
	stageConfigure: {"c1", "c2"},
	stageStart:     {"s1", "s2"},
	stageStop:      {"p1", "p2"},
	stageCleanup:   {"u1", "u2"},
}

// statusOf returns the status of each node whose status is known.
func statusOf(pl *pipeline) map[string]string {
	got := map[string]string{}

	for _, n := range pl.Pipeline {
		if n.Status != statusUnknown {
			got[n.Name] = n.Status
		}
	}

	return got
}

// heavyEdges names, as from>to, each edge with a weight.
func heavyEdges(pl *pipeline) []string {
	var got []string

	for _, n := range pl.Pipeline {
		for _, e := range n.Next {
			if e.Weight > 0 {
				got = append(got, n.Name+">"+pl.Pipeline[e.Index].Name)
			}
		}
	}

	return got
}

type statusUpdate struct {
	stage, name, status string
}

// updateNodeStatus sets a component's status and its stage's status: a stage
// succeeds when each of its components succeeded, or, in configure and start,
// runs in the background; it fails when a component fails. A component the
// stage does not have changes nothing.
func TestUpdateNodeStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		loop      bool
		updates   []statusUpdate
		wantOK    bool
		wantState map[string]string
		wantEdges []string
	}{
		"configure: unknown component": {updates: []statusUpdate{{stageConfigure, "s1", statusRunning}}},
		"configure: running": {
			updates: []statusUpdate{{stageConfigure, "c1", statusRunning}}, wantOK: true,
			wantState: map[string]string{"c1": statusRunning, stageConfigure: statusRunning},
			wantEdges: []string{"configure>c1"},
		},
		"configure: unstable": {
			updates: []statusUpdate{{stageConfigure, "c1", statusUnstable}}, wantOK: true,
			wantState: map[string]string{"c1": statusUnstable, stageConfigure: statusRunning},
			wantEdges: []string{"configure>c1"},
		},
		"configure: background, other not done": {
			updates: []statusUpdate{{stageConfigure, "c1", statusBackground}}, wantOK: true,
			wantState: map[string]string{"c1": statusBackground, stageConfigure: statusRunning},
			wantEdges: []string{"configure>c1"},
		},
		"configure: all done": {
			updates: []statusUpdate{{stageConfigure, "c1", statusBackground}, {stageConfigure, "c2", statusSuccess}}, wantOK: true,
			wantState: map[string]string{"c1": statusBackground, "c2": statusSuccess, stageConfigure: statusSuccess},
			wantEdges: []string{"configure>c1", "c1>start", "c2>start"},
		},
		"configure: failure": {
			updates: []statusUpdate{{stageConfigure, "c1", statusFailure}}, wantOK: true,
			wantState: map[string]string{"c1": statusFailure, stageConfigure: statusFailure},
			wantEdges: []string{"configure>cleanup"},
		},
		"configure: other status": {
			updates: []statusUpdate{{stageConfigure, "c1", "paused"}}, wantOK: true,
			wantState: map[string]string{"c1": "paused"},
		},
		"start: unknown component": {updates: []statusUpdate{{stageStart, "c1", statusRunning}}},
		"start: running": {
			updates: []statusUpdate{{stageStart, "s1", statusRunning}}, wantOK: true,
			wantState: map[string]string{"s1": statusRunning, stageStart: statusRunning},
			wantEdges: []string{"start>s1"},
		},
		"start: background, other not done": {
			updates: []statusUpdate{{stageStart, "s1", statusBackground}}, wantOK: true,
			wantState: map[string]string{"s1": statusBackground, stageStart: statusRunning},
			wantEdges: []string{"start>s1"},
		},
		"start: all done": {
			updates: []statusUpdate{{stageStart, "s1", statusSuccess}, {stageStart, "s2", statusSuccess}}, wantOK: true,
			wantState: map[string]string{"s1": statusSuccess, "s2": statusSuccess, stageStart: statusSuccess},
			wantEdges: []string{"s1>stop", "s2>stop"},
		},
		"start: all done before a loop": {
			loop:    true,
			updates: []statusUpdate{{stageStart, "s1", statusSuccess}, {stageStart, "s2", statusBackground}}, wantOK: true,
			wantState: map[string]string{"s1": statusSuccess, "s2": statusBackground, stageStart: statusSuccess},
			wantEdges: []string{"start>s2", "s1>loop", "s2>loop"},
		},
		"start: failure": {
			updates: []statusUpdate{{stageStart, "s2", statusFailure}}, wantOK: true,
			wantState: map[string]string{"s2": statusFailure, stageStart: statusFailure},
			wantEdges: []string{"start>stop"},
		},
		"stop: unknown component": {updates: []statusUpdate{{stageStop, "u1", statusRunning}}},
		"stop: running": {
			updates: []statusUpdate{{stageStop, "p1", statusRunning}}, wantOK: true,
			wantState: map[string]string{"p1": statusRunning, stageStop: statusRunning},
			wantEdges: []string{"stop>p1"},
		},
		"stop: one done": {
			updates: []statusUpdate{{stageStop, "p1", statusSuccess}}, wantOK: true,
			wantState: map[string]string{"p1": statusSuccess},
		},
		"stop: one failed": {
			updates: []statusUpdate{{stageStop, "p1", statusSuccess}, {stageStop, "p2", statusFailure}}, wantOK: true,
			wantState: map[string]string{"p1": statusSuccess, "p2": statusFailure, stageStop: statusFailure},
			wantEdges: []string{"p1>cleanup", "p2>cleanup"},
		},
		"stop: all done": {
			updates: []statusUpdate{
				{stageStop, "p1", statusUnstable},
				{stageStop, "p1", statusSuccess},
				{stageStop, "p2", statusSuccess},
			},
			wantOK:    true,
			wantState: map[string]string{"p1": statusSuccess, "p2": statusSuccess, stageStop: statusSuccess},
			wantEdges: []string{"stop>p1", "p1>cleanup", "p2>cleanup"},
		},
		"cleanup: unknown component": {updates: []statusUpdate{{stageCleanup, "p1", statusRunning}}},
		"cleanup: running": {
			updates: []statusUpdate{{stageCleanup, "u2", statusUnstable}}, wantOK: true,
			wantState: map[string]string{"u2": statusUnstable, stageCleanup: statusRunning},
			wantEdges: []string{"cleanup>u2"},
		},
		"cleanup: one failed": {
			updates: []statusUpdate{{stageCleanup, "u1", statusFailure}, {stageCleanup, "u2", statusSuccess}}, wantOK: true,
			wantState: map[string]string{"u1": statusFailure, "u2": statusSuccess, stageCleanup: statusFailure},
			wantEdges: []string{"u1>done", "u2>done"},
		},
		"cleanup: all done": {
			updates: []statusUpdate{{stageCleanup, "u1", statusSuccess}, {stageCleanup, "u2", statusSuccess}}, wantOK: true,
			wantState: map[string]string{"u1": statusSuccess, "u2": statusSuccess, stageCleanup: statusSuccess},
			wantEdges: []string{"u1>done", "u2>done"},
		},
		"done stage":    {updates: []statusUpdate{{stageDone, "c1", statusSuccess}}},
		"loop stage":    {loop: true, updates: []statusUpdate{{stageLoop, "s1", statusSuccess}}},
		"unknown stage": {updates: []statusUpdate{{"later", "c1", statusSuccess}}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pl := statusPipeline(statusComponents, tc.loop)

			var ok bool
			for _, u := range tc.updates {
				ok = pl.updateNodeStatus(u.stage, u.name, u.status)
			}

			if ok != tc.wantOK {
				t.Errorf("updateNodeStatus(%+v) = %t, want %t", tc.updates[len(tc.updates)-1], ok, tc.wantOK)
			}

			if diff := cmp.Diff(tc.wantState, statusOf(pl), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("after updateNodeStatus(%+v), statuses mismatch (-want +got):\n%s", tc.updates, diff)
			}

			if diff := cmp.Diff(
				tc.wantEdges,
				heavyEdges(pl),
				cmpopts.EquateEmpty(),
				cmpopts.SortSlices(func(a, b string) bool { return a < b }),
			); diff != "" {
				t.Errorf("after updateNodeStatus(%+v), weighted edges mismatch (-want +got):\n%s", tc.updates, diff)
			}
		})
	}
}

// setStageStatus sets a stage's status, and weights the edge to the next
// stage when the stage succeeds or fails. A loop stage needs a loop node.
func TestSetStageStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		loop          bool
		stage, status string
		wantOK        bool
		wantEdges     []string
	}{
		"configure running":      {stage: stageConfigure, status: statusRunning, wantOK: true},
		"configure success":      {stage: stageConfigure, status: statusSuccess, wantOK: true, wantEdges: []string{"configure>start"}},
		"start failure":          {stage: stageStart, status: statusFailure, wantOK: true, wantEdges: []string{"start>stop"}},
		"start success, a loop":  {loop: true, stage: stageStart, status: statusSuccess, wantOK: true, wantEdges: []string{"start>loop"}},
		"start running":          {stage: stageStart, status: statusRunning, wantOK: true},
		"stop success":           {stage: stageStop, status: statusSuccess, wantOK: true, wantEdges: []string{"stop>cleanup"}},
		"stop running":           {stage: stageStop, status: statusRunning, wantOK: true},
		"cleanup failure":        {stage: stageCleanup, status: statusFailure, wantOK: true, wantEdges: []string{"cleanup>done"}},
		"cleanup running":        {stage: stageCleanup, status: statusRunning, wantOK: true},
		"done":                   {stage: stageDone, status: statusSuccess, wantOK: true},
		"loop success":           {loop: true, stage: stageLoop, status: statusSuccess, wantOK: true, wantEdges: []string{"loop>stop"}},
		"loop unstable":          {loop: true, stage: stageLoop, status: statusUnstable, wantOK: true},
		"loop without loop node": {stage: stageLoop, status: statusSuccess},
		"unknown stage":          {stage: "later", status: statusSuccess},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pl := statusPipeline(nil, tc.loop)

			if got := pl.setStageStatus(tc.stage, tc.status); got != tc.wantOK {
				t.Errorf("setStageStatus(%s, %s) = %t, want %t", tc.stage, tc.status, got, tc.wantOK)
			}

			want := map[string]string{}
			if tc.wantOK {
				want[tc.stage] = tc.status
			}

			if diff := cmp.Diff(want, statusOf(pl)); diff != "" {
				t.Errorf("setStageStatus(%s, %s) statuses mismatch (-want +got):\n%s", tc.stage, tc.status, diff)
			}

			if diff := cmp.Diff(tc.wantEdges, heavyEdges(pl), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("setStageStatus(%s, %s) weighted edges mismatch (-want +got):\n%s", tc.stage, tc.status, diff)
			}
		})
	}
}

// After any component updates, a stage is a success only when each of its
// components is a success, or, in configure and start, runs in the
// background; updateNodeStatus accepts only the stage's own components.
func TestUpdateNodeStatusStageSuccess(t *testing.T) {
	t.Parallel()

	stages := []string{stageConfigure, stageStart, stageStop, stageCleanup}
	statuses := map[string][]string{
		stageConfigure: {statusRunning, statusUnstable, statusBackground, statusSuccess, statusFailure},
		stageStart:     {statusRunning, statusUnstable, statusBackground, statusSuccess, statusFailure},
		stageStop:      {statusRunning, statusUnstable, statusSuccess, statusFailure},
		stageCleanup:   {statusRunning, statusUnstable, statusSuccess, statusFailure},
	}

	names := make([]string, 0, 2*len(stages))
	for _, stage := range stages {
		names = append(names, statusComponents[stage]...)
	}

	rapid.Check(t, func(t *rapid.T) {
		pl := statusPipeline(statusComponents, rapid.Bool().Draw(t, "loop"))
		stageNodes := map[string]*node{stageConfigure: pl.config, stageStart: pl.start, stageStop: pl.stop, stageCleanup: pl.cleanup}

		for range rapid.IntRange(1, 30).Draw(t, "updates") {
			stage := rapid.SampledFrom(stages).Draw(t, "stage")
			name := rapid.SampledFrom(names).Draw(t, "name")
			status := rapid.SampledFrom(statuses[stage]).Draw(t, "status")

			if got, want := pl.updateNodeStatus(stage, name, status), slices.Contains(statusComponents[stage], name); got != want {
				t.Fatalf("updateNodeStatus(%s, %s, %s) = %t, want %t", stage, name, status, got, want)
			}

			for _, s := range stages {
				if stageNodes[s].Status != statusSuccess {
					continue
				}

				for _, c := range statusComponents[s] {
					n := pl.Pipeline[slices.IndexFunc(pl.Pipeline, func(n *node) bool { return n.Name == c })]

					done := n.Status == statusSuccess ||
						(n.Status == statusBackground && (s == stageConfigure || s == stageStart))
					if !done {
						t.Fatalf("stage %s is a success with component %s %s", s, c, n.Status)
					}
				}
			}
		}
	})
}

// UpdatePipeline builds the pipeline of a run with components in each stage,
// and sets the status of a stage or a component in it. It fails for an
// experiment without a SCORCH app, and ignores a stage it does not know.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestUpdatePipelineStages(t *testing.T) {
	useScorch(t, map[string]any{
		"name": "full", "configure": []string{"c"}, "start": []string{"s"}, "stop": []string{"p"}, "cleanup": []string{"u"},
	})
	handlersStore(t, "plain", map[string]any{}, map[string]any{})

	updates := []ComponentUpdate{
		{Exp: "exp", Stage: stageConfigure, Status: statusRunning},
		{Exp: "exp", Stage: stageStop, CmpName: "p", Status: statusSuccess},
		{Exp: "exp", Stage: "later", Status: statusRunning},
	}

	for _, u := range updates {
		if err := UpdatePipeline(u); err != nil {
			t.Fatalf("UpdatePipeline(%+v) error = %v", u, err)
		}
	}

	pl, err := RequestPipeline("exp", 0, 0)
	if err != nil {
		t.Fatalf("RequestPipeline(exp, 0, 0) error = %v", err)
	}

	want := map[string]string{stageConfigure: statusRunning, "p": statusSuccess, stageStop: statusSuccess}
	if diff := cmp.Diff(want, statusOf(pl)); diff != "" {
		t.Errorf("pipeline statuses mismatch (-want +got):\n%s", diff)
	}

	wantStages := map[string]string{"c": stageConfigure, "s": stageStart, "p": stageStop, "u": stageCleanup}
	for _, n := range pl.Pipeline {
		if stage, ok := wantStages[n.Name]; ok && n.Stage != stage {
			t.Errorf("pipeline node %s stage = %q, want %q", n.Name, n.Stage, stage)
		}
	}

	if err := UpdatePipeline(ComponentUpdate{Exp: "plain", Stage: stageStart, Status: statusRunning}); err == nil {
		t.Errorf("UpdatePipeline(plain) = nil, want an error for an experiment without SCORCH")
	}

	if _, err := RequestPipeline("plain", 0, 0); err == nil {
		t.Errorf("RequestPipeline(plain, 0, 0) = nil error, want one")
	}

	DeletePipeline("plain", 0, 0, true)
}
