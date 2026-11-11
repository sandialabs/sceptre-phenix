package experiment

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
)

// launchPollInterval is how often a start asks minimega how many VMs it launched, while it launches them.
const launchPollInterval = 2 * time.Second

// The steps of the prepare phase after the disk check, which is step 0, in order. A dry run does only the disk
// check and the scripts.
const (
	prepareScripts = iota + 1
	prepareSnapshotCleanup
	prepareMinimegaScript
	prepareSteps
	prepareDryRunSteps = prepareSnapshotCleanup
)

// startVMFixedSteps is the number of steps that every start-vms phase has: the VM start, and the VM hosts and VLANs.
const startVMFixedSteps = 2

// launchPlan is how a start launches the bootable VMs of an experiment.
type launchPlan struct {
	// the VMs to start at once, in topology order
	vms []string
	// the VMs to start at once, or nil when they are every bootable VM, as mm.StartLaunchedVMs takes them
	start []string
	// each timer-delayed VM's delay, and each C2-delayed VM's awaited VMs
	delays map[string]time.Duration
	c2s    map[string]map[string]bool
	// the delayed VMs as the start's notes list them
	userDelayed, c2Delayed, timerDelayed []string
}

// planLaunch sorts the bootable VMs of spec that are not external into those to start at once and the delayed ones.
func planLaunch(spec ifaces.ExperimentSpec) launchPlan {
	plan := launchPlan{
		vms:          make([]string, 0),
		start:        nil,
		delays:       make(map[string]time.Duration),
		c2s:          make(map[string]map[string]bool),
		userDelayed:  nil,
		c2Delayed:    nil,
		timerDelayed: nil,
	}

	bootable := spec.Topology().BootableNodes()

	for _, node := range bootable {
		if node.External() {
			continue
		}

		hostname := node.General().Hostname()

		if node.Delay().User() {
			plan.userDelayed = append(plan.userDelayed, hostname)

			continue
		}

		if others := node.Delay().C2(); len(others) > 0 {
			var (
				c2    = make(map[string]bool)
				hosts = make([]string, 0, len(others))
			)

			for _, other := range others {
				if other.Hostname() == "" {
					continue
				}

				c2[other.Hostname()] = other.UseUUID()
				hosts = append(hosts, other.Hostname())
			}

			if len(c2) > 0 {
				plan.c2s[hostname] = c2
				plan.c2Delayed = append(plan.c2Delayed, fmt.Sprintf("%s (after %s)", hostname, strings.Join(hosts, ", ")))

				continue
			}
		}

		if d := node.Delay().Timer(); d != 0 {
			plan.delays[hostname] = d
			plan.timerDelayed = append(plan.timerDelayed, fmt.Sprintf("%s (after %v)", hostname, d))

			continue
		}

		plan.vms = append(plan.vms, hostname)
	}

	// A nil list has mm.StartLaunchedVMs send `vm start all`, which saves calls to minimega; an empty list starts
	// nothing.
	if len(plan.vms) != len(bootable) {
		plan.start = plan.vms
	}

	return plan
}

// startVMSteps counts the steps of the start-vms phase of exp: the VM start, the GRE bridge, the VM hosts and VLANs,
// and, when the post-start work runs in the background, the record of the start.
func startVMSteps(exp *types.Experiment, background bool) int {
	steps := startVMFixedSteps

	if exp.Spec.UseGREMesh() {
		steps++
	}

	if background {
		steps++
	}

	return steps
}

// postStartSteps gives the steps of the post-start-apps phase of exp: 1 for the C2 script, when c2Script and the
// start is not a dry run and exp has one, else 0, then the post-start apps when postStart.
func postStartSteps(exp *types.Experiment, dryrun, c2Script, postStart bool) (int, []string) {
	c2 := 0
	if c2Script && !dryrun && exp.Spec.Topology().HasCommands() {
		c2 = 1
	}

	if !postStart {
		return c2, nil
	}

	return c2, app.PlannedApps(exp, app.Stage(app.ActionPostStart), app.DryRun(dryrun))
}

// startWork counts the steps of each phase of a start of exp, as the start plans them before it begins.
func startWork(exp *types.Experiment, dryrun, background bool) map[string]int {
	work := map[string]int{
		PhasePreStartApps: len(app.PlannedApps(exp, app.Stage(app.ActionPreStart), app.DryRun(dryrun))),
		PhasePrepare:      prepareSteps,
	}

	if dryrun {
		work[PhasePrepare] = prepareDryRunSteps
	} else {
		work[PhaseLaunchVMs] = len(planLaunch(exp.Spec).vms)
		work[PhaseStartVMs] = startVMSteps(exp, background)
	}

	c2, apps := postStartSteps(exp, dryrun, true, true)
	work[PhasePostStartApps] = c2 + len(apps)

	return work
}

// stopCleanupSteps counts the steps of the cleanup phase of a stop: the snapshots unless keepSnapshots, the bridge,
// and the record of the stop. A dry run has only the record.
func stopCleanupSteps(dryrun, keepSnapshots bool) int {
	steps := 1

	if !dryrun {
		steps++

		if !keepSnapshots {
			steps++
		}
	}

	return steps
}

// stopWork counts the steps of each phase of a stop of exp.
func stopWork(exp *types.Experiment, dryrun, keepSnapshots bool) map[string]int {
	work := map[string]int{
		PhaseCleanupApps: len(app.PlannedApps(exp, app.Stage(app.ActionCleanup), app.DryRun(dryrun))),
		PhaseCleanup:     stopCleanupSteps(dryrun, keepSnapshots),
	}

	if !dryrun {
		work[PhaseStopVMs] = 1
	}

	return work
}

// firstApp is the first of the apps, or "" when there are none.
func firstApp(apps []string) string {
	if len(apps) == 0 {
		return ""
	}

	return apps[0]
}

// appSteps is the app.OnApp function that reports each app as a step of the current phase, after offset steps.
func (r *progressReporter) appSteps(offset int) app.Option {
	return app.OnApp(func(ran int, name string) { r.step(offset+ran, name, true) })
}

// markStart is the mark function of a start that marker records: it puts the progress in the start marker of exp
// and of the store, while the store still holds marker.
func markStart(exp *types.Experiment, marker ifaces.StartMarker) ProgressFunc {
	return func(p ifaces.OperationProgress) {
		m := marker
		m.Progress = p

		exp.Status.SetStarting(m)

		err := updateStatus(exp.Metadata.Name, func(s ifaces.ExperimentStatus) bool {
			if !s.Starting().Same(marker) {
				return false
			}

			s.SetStarting(m)

			return true
		})
		if err != nil {
			plog.Warn(plog.TypeSystem, "recording experiment start progress", "exp", exp.Metadata.Name, "err", err)
		}
	}
}

// markStop is the mark function of a stop that marker records: it puts marker, with the progress, in the stop
// marker of exp and of the store.
func markStop(exp *types.Experiment, marker ifaces.StopMarker) ProgressFunc {
	return func(p ifaces.OperationProgress) {
		m := marker
		m.Progress = p

		exp.Status.SetStopping(m)

		err := updateStatus(exp.Metadata.Name, func(s ifaces.ExperimentStatus) bool {
			s.SetStopping(m)

			return true
		})
		if err != nil {
			plog.Warn(plog.TypeSystem, "recording experiment stop progress", "exp", exp.Metadata.Name, "err", err)
		}
	}
}

// pollLaunch asks minimega at each interval how many of vms namespace ns launched, and counts them in the
// launch-vms phase of rep, until the returned function is called. It returns at once and does not ask when rep
// sends to no one or vms is empty.
func pollLaunch(ctx context.Context, ns string, vms []string, rep *progressReporter, interval time.Duration) func() {
	if rep == nil || rep.send == nil || len(vms) == 0 {
		return func() {}
	}

	var (
		ctx2, cancel = context.WithCancel(ctx)
		wg           sync.WaitGroup
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx2.Done():
				return
			case <-ticker.C:
			}

			launched, err := mm.GetLaunchProgress(ctx2, ns, vms)
			if err != nil {
				if ctx2.Err() == nil {
					plog.Debug(plog.TypeSystem, "counting launched VMs", "exp", ns, "err", err)
				}

				continue
			}

			rep.count(PhaseLaunchVMs, launched)
		}
	}()

	return func() {
		cancel()
		wg.Wait()
	}
}
