package capture

import (
	"slices"
	"sync"

	"phenix/api/experiment"
	"phenix/util/mm"
)

// maxKept is how many stopped experiment captures one experiment keeps; the oldest stop goes first.
const maxKept = 100

// stateStopped is the state of a capture that Stop stopped.
const stateStopped = "stopped"

// keptCaptures holds the experiment captures that Stop stopped, by experiment. minimega forgets a capture when it
// stops it, so without this record a stopped capture leaves the list. The record is in memory only: a restart of
// phenix, or a stop from another phenix process, is not in it.
type keptCaptures struct {
	mu   sync.Mutex
	runs map[string]keptRun
}

// keptRun is the stopped captures of one run of an experiment, oldest stop first.
type keptRun struct {
	start    string // the experiment's start time, which tells one run from the next
	captures []mm.NamespaceCapture
}

var kept keptCaptures //nolint:gochecknoglobals // one record for the process

// add keeps the stopped capture c of the run of exp that started at start.
func (k *keptCaptures) add(exp, start string, c mm.NamespaceCapture) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.runs == nil {
		k.runs = make(map[string]keptRun)
	}

	run := k.runs[exp]
	if run.start != start {
		run = keptRun{start: start, captures: nil}
	}

	run.captures = slices.DeleteFunc(run.captures, func(o mm.NamespaceCapture) bool { return o.ID == c.ID })
	run.captures = append(run.captures, c)

	if over := len(run.captures) - maxKept; over > 0 {
		run.captures = slices.Delete(run.captures, 0, over)
	}

	k.runs[exp] = run
}

// merge returns the live captures of exp with the kept ones of its run that started at start, sorted by ID. It drops
// a kept capture whose file the head node no longer has, or whose ID minimega lists again, and every kept capture of
// an earlier run.
func (k *keptCaptures) merge(exp, start string, live []mm.NamespaceCapture) []mm.NamespaceCapture {
	k.mu.Lock()
	defer k.mu.Unlock()

	run, ok := k.runs[exp]
	if !ok {
		return live
	}

	if run.start != start {
		delete(k.runs, exp)

		return live
	}

	run.captures = slices.DeleteFunc(run.captures, func(c mm.NamespaceCapture) bool {
		file := File(exp, c)

		return file == "" || !experiment.LocalFileExists(exp, file) ||
			slices.ContainsFunc(live, func(l mm.NamespaceCapture) bool { return l.ID == c.ID })
	})

	if len(run.captures) == 0 {
		delete(k.runs, exp)

		return live
	}

	k.runs[exp] = run

	merged := append(slices.Clone(live), run.captures...)
	slices.SortStableFunc(merged, func(a, b mm.NamespaceCapture) int { return mm.CompareCaptureIDs(a.ID, b.ID) })

	return merged
}

// Forget drops the stopped experiment captures that Stop kept of exp. The experiment's stop calls it.
func Forget(exp string) {
	kept.mu.Lock()
	defer kept.mu.Unlock()

	delete(kept.runs, exp)
}
