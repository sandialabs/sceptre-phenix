package experiment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
)

// mmStateBuilding is the state of a VM minimega launched but has not started.
const mmStateBuilding = "BUILDING"

// pendingSteps are the steps of Start left once the VMs launch.
type pendingSteps struct {
	ccScript  string // the cc script to read first, or "" for none
	delays    map[string]time.Duration
	c2s       map[string]map[string]bool
	postStart bool // whether to run the post-start apps
	persist   bool // whether the experiment's status records the delayed starts
}

// delayed reports whether any VM waits to start.
func (s pendingSteps) delayed() bool {
	return len(s.delays) > 0 || len(s.c2s) > 0
}

// dueTimes is when each timer-delayed VM is due to start, delays after now.
func dueTimes(now time.Time, delays map[string]time.Duration) map[string]string {
	due := make(map[string]string, len(delays))

	for vm, delay := range delays {
		due[vm] = now.Add(delay).UTC().Format(time.RFC3339Nano)
	}

	return due
}

// statusLocks holds a mutex for each experiment whose stored status is
// changed in place by updateStatus or Stop.
var statusLocks sync.Map //nolint:gochecknoglobals // per-experiment locks

func lockStatus(name string) func() {
	mu, _ := statusLocks.LoadOrStore(name, new(sync.Mutex))

	m, _ := mu.(*sync.Mutex)
	m.Lock()

	return m.Unlock
}

// updateStatus applies change to the stored status of experiment name, and
// writes it back when change reports that it changed it.
func updateStatus(name string, change func(ifaces.ExperimentStatus) bool) error {
	defer lockStatus(name)()

	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment %s: %w", name, err)
	}

	if !change(exp.Status) {
		return nil
	}

	c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("updating experiment %s: %w", name, err)
	}

	return nil
}

// delayedVMStarted drops the delayed VM host from the starts experiment name
// still records as pending.
func delayedVMStarted(name, host string) {
	err := updateStatus(name, func(s ifaces.ExperimentStatus) bool {
		var (
			timers, c2 = s.DelayedTimers(), s.DelayedC2()
			_, timed   = timers[host]
			_, waited  = c2[host]
		)

		if !timed && !waited {
			return false
		}

		delete(timers, host)
		delete(c2, host)
		s.SetDelayedStarts(timers, c2, s.PostStartPending())

		return true
	})
	if err != nil {
		plog.Warn(plog.TypeSystem, "recording delayed VM start", "exp", name, "vm", host, "err", err)
	}
}

// setPending records on exp, and in its stored status, the delayed starts
// still pending and whether the post-start apps still have to run.
func setPending(exp *types.Experiment, timers map[string]string, c2 map[string]map[string]bool, postStart bool) {
	exp.Status.SetDelayedStarts(timers, c2, postStart)

	err := updateStatus(exp.Metadata.Name, func(s ifaces.ExperimentStatus) bool {
		s.SetDelayedStarts(timers, c2, postStart)

		return true
	})
	if err != nil {
		plog.Warn(plog.TypeSystem, "recording pending experiment starts", "exp", exp.Metadata.Name, "err", err)
	}
}

// finishStart runs steps once the experiment is stored as started, sending errChan each error and closing it.
// A failed step stops the experiment, unless ctx was canceled, as the web UI does before it calls Stop itself.
func finishStart(ctx context.Context, exp *types.Experiment, dryrun bool, steps pendingSteps, errChan chan error) {
	defer close(errChan)

	step, err := afterLaunch(ctx, exp, dryrun, steps)
	if err == nil {
		return
	}

	errChan <- err

	if step == stepDelayedVMs && errors.Is(ctx.Err(), context.Canceled) {
		return
	}

	if err := Stop(exp.Spec.ExperimentName()); err != nil {
		errChan <- fmt.Errorf("stopping experiment: %w", err)
	}
}

// resumePlan is what ResumeStart re-arms, from the pending starts a status
// records and the VM states minimega reports.
type resumePlan struct {
	delays      map[string]time.Duration
	timers      map[string]string // the due times of the VMs in delays
	c2s         map[string]map[string]bool
	postStart   bool
	interrupted string // why some pending start cannot resume, or ""
}

// planResume works out which pending starts in status can resume at now: those of VMs minimega holds unstarted.
// A VM it no longer holds, or a C2-delayed VM waiting on one, cannot start, so the start is interrupted.
func planResume(status ifaces.ExperimentStatus, states map[string]string, statesErr error, now time.Time) resumePlan {
	const prefix = "VMs were waiting to start when phenix stopped tracking them"

	plan := resumePlan{
		delays:      make(map[string]time.Duration),
		timers:      make(map[string]string),
		c2s:         make(map[string]map[string]bool),
		postStart:   false,
		interrupted: "",
	}

	if statesErr != nil {
		plan.interrupted = fmt.Sprintf("%s, and minimega could not list the VMs: %v", prefix, statesErr)

		return plan
	}

	var gone []string

	for vm, due := range status.DelayedTimers() {
		state, ok := states[vm]

		switch {
		case !ok:
			gone = append(gone, vm)
		case state == mmStateBuilding:
			at, err := time.Parse(time.RFC3339Nano, due)
			if err != nil {
				at = now
			}

			plan.delays[vm] = max(at.Sub(now), 0)
			plan.timers[vm] = due
		}
	}

	for vm, others := range status.DelayedC2() {
		state, ok := states[vm]

		switch {
		case !ok:
			gone = append(gone, vm)
		case state != mmStateBuilding:
		case !allPresent(others, states):
			gone = append(gone, vm)
		default:
			plan.c2s[vm] = others
		}
	}

	if len(gone) > 0 {
		slices.Sort(gone)
		plan.interrupted = fmt.Sprintf(
			"%s, and these VMs, or VMs they wait on, are no longer in minimega: %s",
			prefix, strings.Join(gone, ", "),
		)

		return plan
	}

	plan.postStart = status.PostStartPending()

	return plan
}

// allPresent reports whether minimega holds every VM a C2-delayed VM waits on.
func allPresent(others map[string]bool, states map[string]string) bool {
	for other := range others {
		if _, ok := states[other]; !ok {
			return false
		}
	}

	return true
}

// ResumeStart re-arms in the background the delayed VM starts and post-start apps the running experiment still
// records as pending, sending errChan each error and closing it, or records the start as interrupted.
func ResumeStart(ctx context.Context, name string, errChan chan error) (bool, error) {
	exp, err := Get(name)
	if err != nil {
		close(errChan)

		return false, err
	}

	if !exp.Running() || exp.DryRun() || !hasPendingStart(exp.Status) {
		close(errChan)

		return false, nil
	}

	states, err := mm.GetVMStates(mm.NS(exp.Spec.ExperimentName()))
	plan := planResume(exp.Status, states, err, time.Now())

	exp.Status.SetDelayedStarts(plan.timers, plan.c2s, plan.postStart)
	exp.Status.SetInterrupted(plan.interrupted)

	err = updateStatus(name, func(s ifaces.ExperimentStatus) bool {
		s.SetDelayedStarts(plan.timers, plan.c2s, plan.postStart)
		s.SetInterrupted(plan.interrupted)

		return true
	})
	if err != nil {
		close(errChan)

		return false, err
	}

	if plan.interrupted != "" {
		plog.Warn(plog.TypeSystem, "experiment start interrupted", "exp", name, "reason", plan.interrupted)
	}

	steps := pendingSteps{ccScript: "", delays: plan.delays, c2s: plan.c2s, postStart: plan.postStart, persist: true}
	if !steps.delayed() && !steps.postStart {
		close(errChan)

		return false, nil
	}

	plog.Info(
		plog.TypeSystem, "resuming experiment start",
		"exp", name, "delayed_vms", len(plan.delays)+len(plan.c2s), "post_start", plan.postStart,
	)

	go finishStart(ctx, exp, false, steps, errChan)

	return true, nil
}

// PendingStarts lists the running experiments whose status records delayed VM starts or post-start apps still
// pending, which ResumeStart can re-arm.
func PendingStarts() ([]string, error) {
	exps, err := types.Experiments(true)
	if err != nil {
		return nil, fmt.Errorf("listing running experiments: %w", err)
	}

	var names []string

	for _, exp := range exps {
		if hasPendingStart(exp.Status) {
			names = append(names, exp.Metadata.Name)
		}
	}

	return names, nil
}

// hasPendingStart reports whether status records delayed VM starts or
// post-start apps still pending.
func hasPendingStart(s ifaces.ExperimentStatus) bool {
	return len(s.DelayedTimers()) > 0 || len(s.DelayedC2()) > 0 || s.PostStartPending()
}
