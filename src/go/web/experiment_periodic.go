package web

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
	"phenix/web/lifecycle"
)

// periodicCheckInterval is how often a periodic job checks that the start it runs for still runs, to end once another
// process stops or restarts the experiment.
const periodicCheckInterval = 5 * time.Second

// periodicJobs holds, for each experiment, the job that runs its apps on their schedule. commonMu guards it.
var periodicJobs = make(map[string]string) //nolint:gochecknoglobals // shared with the stop path

// startPeriodicJob starts, for owner, the job that runs the running stage of the apps of exp on the schedule of its
// scenario, as phenix experiment start --honor-run-periodically does, and returns it. Without a scheduled app it
// starts nothing and returns nil, unless always is set: then the job ends at once. The job ends when the experiment
// stops, when the job is canceled, when DELETE /experiments/{name}/trigger cancels it, or when the server stops. An
// experiment has one such job at a time: a new one ends the job of an earlier start.
func startPeriodicJob(owner string, exp *types.Experiment, always bool) *job.Job {
	name := exp.Metadata.Name
	apps := app.ScheduledApps(exp)

	if len(apps) == 0 && !always {
		return nil
	}

	cancelPeriodicJob(name)

	// the workers start, and are registered, before the job runs, so a stop at any time ends and waits for them
	work, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	app.PeriodicallyRunApps(work, &wg, exp)

	registered := addCanceler(name, cancel)

	commonMu.Lock()
	waiters[name] = &wg
	commonMu.Unlock()

	j, err := jobs.StartUnmetered(job.Spec{
		Kind:       job.KindExperimentsPeriodic,
		Owner:      owner,
		Experiment: name,
		Title:      "Run the running stage of the apps of experiment " + name + " on their schedule",
		Params:     map[string]string{"apps": strings.Join(apps, ",")},
		Items:      nil,
		BytesTotal: 0,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			defer forgetPeriodicJob(name, run.ID(), registered, &wg)

			if len(apps) > 0 {
				waitPeriodic(ctx, work, exp)
			}

			cancel()
			wg.Wait()

			return nil
		},
	})
	if err != nil {
		forgetPeriodicJob(name, "", registered, &wg)
		wg.Wait()
		plog.Error(plog.TypeSystem, "starting the scheduled runs of experiment apps", "exp", name, "err", err)

		return nil
	}

	commonMu.Lock()
	periodicJobs[name] = j.ID
	commonMu.Unlock()

	return &j
}

// waitPeriodic waits until the job ctx or the work of its workers ends, the server stops, or the start of exp no
// longer runs.
func waitPeriodic(ctx, work context.Context, exp *types.Experiment) {
	ticker := time.NewTicker(periodicCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-work.Done():
			return
		case <-lifecycle.Stopping():
			return
		case <-ticker.C:
			if !sameStart(exp) {
				return
			}
		}
	}
}

// sameStart reports whether the start of exp still runs: the store has the experiment running with the same start
// time. A store that cannot be read does not end it.
func sameStart(exp *types.Experiment) bool {
	stored, err := experiment.Get(exp.Metadata.Name)
	if errors.Is(err, experiment.ErrExperimentNotFound) || errors.Is(err, store.ErrNotExist) {
		return false
	}

	if err != nil {
		return true
	}

	return stored.Running() && stored.Status.StartTime() == exp.Status.StartTime()
}

// forgetPeriodicJob cancels the workers of the job id of experiment name and removes what startPeriodicJob
// registered for it, unless a newer job replaced it.
func forgetPeriodicJob(name, id string, registered *canceler, wg *sync.WaitGroup) {
	dropCanceler(name, registered)

	commonMu.Lock()
	defer commonMu.Unlock()

	if waiters[name] == wg {
		delete(waiters, name)
	}

	if periodicJobs[name] == id {
		delete(periodicJobs, name)
	}
}

// cancelPeriodicJob cancels the job that runs the apps of experiment name on their schedule, if one runs.
func cancelPeriodicJob(name string) {
	commonMu.Lock()
	id, ok := periodicJobs[name]
	commonMu.Unlock()

	if !ok {
		return
	}

	if _, err := jobs.Cancel(id); err != nil && !errors.Is(err, job.ErrFinished) && !errors.Is(err, job.ErrNotFound) {
		plog.Warn(plog.TypeSystem, "canceling the scheduled runs of experiment apps", "exp", name, "job", id, "err", err)
	}
}

// periodicJobOwner is the owner of the job that runs the apps of experiment name on their schedule, or nil when no
// such job runs.
func periodicJobOwner(name string) *string {
	commonMu.Lock()
	id, ok := periodicJobs[name]
	commonMu.Unlock()

	if !ok {
		return nil
	}

	j, ok := jobs.Get(id)
	if !ok || j.Status.Terminal() {
		return nil
	}

	return &j.Owner
}
