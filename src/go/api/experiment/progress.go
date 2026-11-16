package experiment

import (
	"sync"

	ifaces "phenix/types/interfaces"
)

// The operations and phases that progress reports name. A start has the phases pre-start-apps, prepare, launch-vms,
// start-vms and post-start-apps, in that order. A stop has the phases cleanup-apps, stop-vms and cleanup.
const (
	OperationStart = "start"
	OperationStop  = "stop"

	PhasePreStartApps  = "pre-start-apps"
	PhasePrepare       = "prepare"
	PhaseLaunchVMs     = "launch-vms"
	PhaseStartVMs      = "start-vms"
	PhasePostStartApps = "post-start-apps"
	PhaseCleanupApps   = "cleanup-apps"
	PhaseStopVMs       = "stop-vms"
	PhaseCleanup       = "cleanup"

	// PhaseDone is the phase of a start whose post-start work is complete.
	PhaseDone = "done"
)

// The weight of each phase: its part of the progress of its operation when every phase has work.
const (
	weightPreStartApps  = 15
	weightPrepare       = 10
	weightLaunchVMs     = 55
	weightStartVMs      = 5
	weightPostStartApps = 15
	weightCleanupApps   = 40
	weightStopVMs       = 50
	weightCleanup       = 10
)

// ProgressFunc gets each change of the progress of a start or stop. Start and Stop call it from one goroutine at a
// time, in order.
type ProgressFunc func(ifaces.OperationProgress)

// phaseWeight is a phase and its part of the progress of its operation.
type phaseWeight struct {
	phase  string
	weight int
}

// startPhases are the phases of a start, in order, with their weights.
func startPhases() []phaseWeight {
	return []phaseWeight{
		{PhasePreStartApps, weightPreStartApps},
		{PhasePrepare, weightPrepare},
		{PhaseLaunchVMs, weightLaunchVMs},
		{PhaseStartVMs, weightStartVMs},
		{PhasePostStartApps, weightPostStartApps},
	}
}

// stopPhases are the phases of a stop, in order, with their weights.
func stopPhases() []phaseWeight {
	return []phaseWeight{
		{PhaseCleanupApps, weightCleanupApps}, {PhaseStopVMs, weightStopVMs}, {PhaseCleanup, weightCleanup},
	}
}

// progressPlan is the phases of one start or stop, fixed when the operation begins. A phase with no work has weight
// 0, so the weights of the other phases fill the whole progress.
type progressPlan struct {
	operation string
	phases    []phaseWeight
	sum       int
}

// newProgressPlan is the plan of operation over phases, where work counts the steps of each phase. A phase that
// work gives no steps gets weight 0.
func newProgressPlan(operation string, phases []phaseWeight, work map[string]int) progressPlan {
	plan := progressPlan{operation: operation, phases: make([]phaseWeight, 0, len(phases)), sum: 0}

	for _, p := range phases {
		if work[p.phase] <= 0 {
			p.weight = 0
		}

		plan.phases = append(plan.phases, p)
		plan.sum += p.weight
	}

	return plan
}

// percent is the part of the operation that is done, from 0 to 1, when phase has done of its total steps done. A
// phase with no steps counts as done. A phase that is not in the plan gives 0.
func (p progressPlan) percent(phase string, done, total int) float64 {
	if phase == PhaseDone {
		return 1
	}

	if p.sum == 0 {
		return 0
	}

	before := 0

	for _, ph := range p.phases {
		if ph.phase != phase {
			before += ph.weight

			continue
		}

		part := 1.0
		if total > 0 {
			part = float64(min(max(done, 0), total)) / float64(total)
		}

		return (float64(before) + float64(ph.weight)*part) / float64(p.sum)
	}

	return 0
}

// progressReporter reports the progress of one start or stop. It sends each change to send, and also writes each
// phase change and app boundary to mark, which records it in the store for other processes. Its methods hold a lock
// while they call send and mark, so the reports keep their order. A nil progressReporter reports nothing.
type progressReporter struct {
	mu      sync.Mutex
	plan    progressPlan
	current ifaces.OperationProgress
	send    ProgressFunc
	mark    ProgressFunc
}

// newProgressReporter is a reporter of plan that sends to send and marks with mark; either can be nil.
func newProgressReporter(plan progressPlan, send, mark ProgressFunc) *progressReporter {
	return &progressReporter{
		mu:   sync.Mutex{},
		plan: plan,
		current: ifaces.OperationProgress{
			Operation: plan.operation, Percent: 0, Phase: "", Done: 0, Total: 0, Step: "",
		},
		send: send,
		mark: mark,
	}
}

// begin starts phase, which has total steps, at the step named step, and marks it.
func (r *progressReporter) begin(phase string, total int, step string) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.report(phase, 0, total, step, true)
}

// step moves to the step named step of the current phase, with done steps done. With mark, it also marks it. It
// reports nothing when the step and the count do not change.
func (r *progressReporter) step(done int, step string, mark bool) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if step == r.current.Step && min(max(done, 0), r.current.Total) == r.current.Done {
		return
	}

	r.report(r.current.Phase, done, r.current.Total, step, mark)
}

// count sets the steps of phase that a poll counts as done. It reports only a higher count, and only while phase is
// the current phase. It does not mark.
func (r *progressReporter) count(phase string, done int) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.current.Phase != phase || min(done, r.current.Total) <= r.current.Done {
		return
	}

	r.report(phase, done, r.current.Total, r.current.Step, false)
}

// finish reports the operation complete, with the phase done. It does not mark.
func (r *progressReporter) finish() {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.report(PhaseDone, 0, 0, "", false)
}

// stopMarking stops the writes to mark, for when the store no longer holds the marker.
func (r *progressReporter) stopMarking() {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.mark = nil
}

// report sets the current progress and sends it, and marks it with mark. The caller holds r.mu.
func (r *progressReporter) report(phase string, done, total int, step string, mark bool) {
	done = min(max(done, 0), max(total, 0))

	r.current = ifaces.OperationProgress{
		Operation: r.plan.operation,
		Percent:   r.plan.percent(phase, done, total),
		Phase:     phase,
		Done:      done,
		Total:     total,
		Step:      step,
	}

	if r.send != nil {
		r.send(r.current)
	}

	if mark && r.mark != nil {
		r.mark(r.current)
	}
}
