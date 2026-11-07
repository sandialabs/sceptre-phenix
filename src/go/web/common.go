package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/api/job"
	"phenix/api/vm"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/scorch"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	// the error metadata of a refusal that the request with force=true overrides:
	// a stop that left the VMs running, or a delete of a disk in use.
	retryKey   = "retry"
	retryForce = "force"

	statusKeyPercent             = "percent"
	percentMultiplier            = 100.0
	experimentStartCheckInterval = 2 * time.Second
)

var (
	// Track context cancelers and wait groups for periodically running apps.
	cancelers = make(map[string][]*canceler)     //nolint:gochecknoglobals // global state
	waiters   = make(map[string]*sync.WaitGroup) //nolint:gochecknoglobals // global state
	commonMu  sync.Mutex                         //nolint:gochecknoglobals // global lock
)

// canceler is one registered cancel func. Its address identifies it, so that
// its owner can remove it and keep the other cancelers of the same key.
type canceler struct {
	cancel context.CancelFunc
}

// addCanceler registers cancel under key and returns the handle that
// dropCanceler takes.
func addCanceler(key string, cancel context.CancelFunc) *canceler {
	c := &canceler{cancel: cancel}

	commonMu.Lock()
	cancelers[key] = append(cancelers[key], c)
	commonMu.Unlock()

	return c
}

// dropCanceler cancels c and removes it from key's cancelers. The other
// cancelers of key stay registered.
func dropCanceler(key string, c *canceler) {
	c.cancel()

	commonMu.Lock()
	defer commonMu.Unlock()

	var left []*canceler

	for _, other := range cancelers[key] {
		if other != c {
			left = append(left, other)
		}
	}

	if len(left) == 0 {
		delete(cancelers, key)
	} else {
		cancelers[key] = left
	}
}

// startOutcome is what a start that startExperiment made reports.
type startOutcome struct {
	exp *types.Experiment
	// the warnings of the start, as text; never nil
	warnings []string
	// the minimega script that the start wrote
	mmScript string
	// the job that runs the apps' running stage on their schedule, if one started
	periodic *job.Job
}

// startExperiment starts the experiment name with opts, as owner, and waits until the start ends. A dry run leaves
// the experiment stopped. After a start that is not a dry run, the apps' running stage runs on its schedule in a job
// of owner (see startPeriodicJob), unless opts turn it off; with HonorRunPeriodically set, the job starts even when
// no app has a schedule.
//
//nolint:funlen // complex logic
func startExperiment(name, owner string, opts startopts.Options) (startOutcome, error) {
	err := cache.LockExperimentForStarting(name)
	if err != nil {
		err := weberror.NewWebError(err, "unable to lock experiment %s for starting", name)

		return startOutcome{}, err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(name)

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/start", "update", name),
		bt.NewResource("experiment", name, "starting"),
		nil,
	)

	type result struct {
		outcome startOutcome
		err     error
	}

	status := make(chan result)

	go func() {
		// We don't want to use the HTTP request's context here.
		ctx, cancel := context.WithCancel(context.Background())
		registered := addCanceler(name, cancel)

		ctx = notes.Context(ctx, false)

		var (
			ch     = make(chan error)
			report experiment.StartResult
			start  = append(opts.StartOptions(name), experiment.StartWithResult(&report))
		)

		// a dry run does its post-start stage before Start returns, and records nothing
		if opts.DryRun {
			start = append(start, experiment.StartWithKeepStopped(true))
		} else {
			start = append(start, experiment.StartWithErrorChannel(ch))
		}

		err := experiment.Start(ctx, start...)
		warnings := noteTexts(notes.Warnings(ctx, true))

		if err != nil || opts.DryRun {
			dropCanceler(name, registered)
		}

		if err != nil {
			status <- result{startOutcome{}, err} //nolint:exhaustruct // nothing to report of a failed start

			return
		}

		if !opts.DryRun {
			followStart(ctx, name, ch)
		}

		exp, err := experiment.Get(name)

		status <- result{startOutcome{exp: exp, warnings: warnings, mmScript: report.MMScript, periodic: nil}, err}
	}()

	var progress float64

	// the experiment list reports the latest progress to pages opened
	// mid-start
	defer startProgress.Delete(name)

	// a dry run sends minimega nothing, so it waits without asking for progress
	var count int
	if !opts.DryRun {
		count, _ = vm.Count(name)
	}

	for {
		var (
			s        result
			finished bool
		)

		if opts.DryRun {
			s, finished = <-status, true
		} else {
			select {
			case s = <-status:
				finished = true
			default:
			}
		}

		switch {
		case finished:
			if s.err != nil {
				broker.Broadcast(
					bt.NewRequestPolicy("experiments/start", "update", name),
					bt.NewResource("experiment", name, "errorStarting"),
					nil,
				)

				return startOutcome{}, startWebError(s.err, "unable to start experiment %s", name)
			}

			if opts.DryRun {
				return s.outcome, broadcastDryRun(name, s.outcome.exp)
			}

			// Clear previous SCORCH pipeline runtime state after successful experiment start.
			scorch.DeletePipeline(name, -1, -1, false)

			if opts.Periodic(true) {
				s.outcome.periodic = startPeriodicJob(owner, s.outcome.exp, opts.HonorRunPeriodically != nil)
			}

			// Like GET /experiments?vms=false: counts come from the topology and
			// no VM list is asked of minimega. The UI's start handlers replace the
			// experiment row (Experiments.vue) or reload (Scorch, SoH, runs) and
			// never read `vms` from this body.
			body, err := marshaler.Marshal(util.ExperimentSummaryToProtobuf(*s.outcome.exp, ""))
			if err != nil {
				err := weberror.NewWebError(err, "unable to start experiment %s", name)

				return startOutcome{}, err.SetStatus(http.StatusInternalServerError)
			}

			broker.Broadcast(
				bt.NewRequestPolicy("experiments/start", "update", name),
				bt.NewResource("experiment", name, "start"),
				body,
			)

			return s.outcome, nil
		default:
			p, err := mm.GetLaunchProgress(name, count)
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"getting progress for experiment",
					"exp",
					name,
					"err",
					err,
				)

				continue
			}

			if p > progress {
				progress = p
			}

			startProgress.Store(name, progress)

			plog.Debug(plog.TypeSystem, "percent deployed", statusKeyPercent, progress*percentMultiplier, "exp", name)

			status := map[string]any{
				statusKeyPercent: progress,
			}

			marshalled, _ := json.Marshal(status)

			broker.Broadcast(
				bt.NewRequestPolicy("experiments/start", "update", name),
				bt.NewResource("experiment", name, "progress"),
				marshalled,
			)

			time.Sleep(experimentStartCheckInterval)
		}
	}
}

// broadcastDryRun tells the clients that the dry run of experiment exp ended with it stopped, as a stop does, so a
// page that showed it starting shows it stopped again.
func broadcastDryRun(name string, exp *types.Experiment) error {
	body, err := marshaler.Marshal(util.ExperimentSummaryToProtobuf(*exp, ""))
	if err != nil {
		return weberror.NewWebError(err, "unable to start experiment %s", name).SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/start", "update", name),
		bt.NewResource("experiment", name, "stop"),
		body,
	)

	return nil
}

// noteTexts are the texts of errs, as an empty list when there are none.
func noteTexts(errs []error) []string {
	texts := make([]string, 0, len(errs))

	for _, err := range errs {
		texts = append(texts, err.Error())
	}

	return texts
}

// lifecycleStatus is the status of a start or stop that failed with err: 404 for a missing experiment, 409 for one
// that another process starts, already runs or does not run, else 400.
func lifecycleStatus(err error) int {
	switch {
	case errors.Is(err, experiment.ErrExperimentNotFound):
		return http.StatusNotFound
	case errors.Is(err, experiment.ErrExperimentStarting), errors.Is(err, experiment.ErrExperimentRunning),
		errors.Is(err, experiment.ErrStopNotRunning):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// startWebError is the error of a start that failed with err, with the status lifecycleStatus gives. For broken disk
// images it has the code broken_disks and lists each VM, disk and missing file in details.disks. For a pinned VLAN ID
// that minimega gives to another alias, it has the code vlan_in_use and the ID in details.vlan.
func startWebError(err error, format string, args ...any) *weberror.WebError {
	werr := weberror.NewWebError(err, format, args...).SetStatus(lifecycleStatus(err))

	var (
		broken *experiment.BrokenDisksError
		inUse  *experiment.VLANInUseError
	)

	switch {
	case errors.As(err, &broken):
		werr = werr.WithCode(weberror.CodeBrokenDisks).WithDetail("disks", broken.Problems)
	case errors.As(err, &inUse):
		werr = werr.WithCode(weberror.CodeVLANInUse).WithDetail("vlan", inUse.ID)
	}

	return werr
}

// followStart logs the notes of the experiment's start as it goes on in the
// background, and reports each error ch gets, until ch closes.
func followStart(ctx context.Context, name string, ch chan error) {
	for _, note := range notes.Info(ctx, false) {
		plog.Info(plog.TypeSystem, note)
	}

	done := make(chan struct{})

	go func() {
		for {
			for _, note := range notes.Info(ctx, false) {
				plog.Info(plog.TypeSystem, note)
			}

			select {
			case <-done:
				return
			default:
				time.Sleep(1 * time.Second)
			}
		}
	}()

	go func() {
		for err := range ch {
			plog.Warn(plog.TypeSystem, "delayed error starting experiment", "exp", name, "err", err)

			var delayErr experiment.DelayedVMError

			if errors.As(err, &delayErr) {
				broker.Broadcast(
					bt.NewRequestPolicy("experiments/start", "update", name),
					bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", name, delayErr.VM), "error"),
					json.RawMessage(fmt.Sprintf(`{"error": "unable to start delayed VM %s"}`, delayErr.VM)),
				)
			}
		}

		// Stop periodically printing out logs via previous Goroutine.
		close(done)
	}()
}

// rearmAfterFailedStop restarts what stopExperiment canceled before a stop
// that left experiment name running: its pending starts and, when periodic
// names the owner of the job that ran them, its apps' scheduled runs.
func rearmAfterFailedStop(name string, periodic *string) {
	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "re-arming experiment after a failed stop", "exp", name, "err", err)

		return
	}

	if periodic != nil {
		startPeriodicJob(*periodic, exp, false)
	}

	resumeStart(name)
}

// resumeStart re-arms what experiment name's start still waited on when phenix
// stopped waiting (see experiment.ResumeStart), cancelable as a start is.
func resumeStart(name string) {
	ctx, cancel := context.WithCancel(context.Background())
	registered := addCanceler(name, cancel)

	ctx = notes.Context(ctx, false)

	ch := make(chan error)

	resumed, err := experiment.ResumeStart(ctx, name, ch)
	if err != nil {
		plog.Error(plog.TypeSystem, "resuming experiment start", "exp", name, "err", err)
	}

	if !resumed {
		dropCanceler(name, registered)

		return
	}

	followStart(ctx, name, ch)
}

// startProgress holds each starting experiment's launch progress, from 0 to 1.
var startProgress sync.Map //nolint:gochecknoglobals // shared with the list handler

// experimentStatus is the experiment's status as the experiment lists report
// it, and how far along its start is while it is starting. A start that
// another process runs is starting, with an unknown progress of 0.
func experimentStatus(exp types.Experiment) (cache.Status, float64) {
	status := cache.ExperimentStatus(exp.Metadata.Name, exp.Running())
	if status == cache.StatusStopped && experiment.StartingElsewhere(exp.Status) {
		return cache.StatusStarting, 0
	}

	if status != cache.StatusStarting {
		return status, 0
	}

	progress, _ := startProgress.Load(exp.Metadata.Name)
	percent, _ := progress.(float64)

	return status, percent
}

// takeCancelers removes and returns the cancelers and the wait group of
// experiment name. The cancelers include those of the app stages triggered
// from the UI, which register under "<name>/<app>".
func takeCancelers(name string) ([]*canceler, *sync.WaitGroup) {
	commonMu.Lock()
	defer commonMu.Unlock()

	cancels := cancelers[name]
	wg := waiters[name]

	delete(cancelers, name)
	delete(waiters, name)

	for key, triggered := range cancelers {
		if strings.HasPrefix(key, name+"/") {
			cancels = append(cancels, triggered...)
			delete(cancelers, key)
		}
	}

	return cancels, wg
}

// stopExperiment stops the experiment; force marks it stopped even when minimega cannot kill its VMs,
// which otherwise answers 502 with the metadata retry=force, and keep keeps the VMs' injection snapshots.
func stopExperiment(name string, force, keep bool) ([]byte, error) {
	if err := cache.LockExperimentForStopping(name); err != nil {
		err := weberror.NewWebError(err, "unable to lock experiment %s for stopping", name)

		return nil, err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(name)

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/stop", "update", name),
		bt.NewResource("experiment", name, "stopping"),
		nil,
	)

	periodic := periodicJobOwner(name)
	cancels, wg := takeCancelers(name)

	for _, c := range cancels {
		c.cancel()
	}

	if wg != nil {
		wg.Wait()
	}

	if err := experiment.Stop(
		name, experiment.StopWithForce(force), experiment.StopWithKeepInjectionSnapshots(keep),
	); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("experiments/stop", "update", name),
			bt.NewResource("experiment", name, "errorStopping"),
			nil,
		)

		werr := weberror.NewWebError(err, "unable to stop experiment %s", name)

		if errors.Is(err, experiment.ErrTeardownFailed) {
			rearmAfterFailedStop(name, periodic)

			return nil, werr.WithMetadata(retryKey, retryForce, true).SetStatus(http.StatusBadGateway)
		}

		return nil, werr.SetStatus(lifecycleStatus(err))
	}

	exp, err := experiment.Get(name)
	if err != nil {
		err := weberror.NewWebError(err, "unable to get stopped experiment %s", name)

		return nil, err.SetStatus(http.StatusInternalServerError)
	}

	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vm.ListFor(context.Background(), exp)))
	if err != nil {
		err := weberror.NewWebError(err, "unable to stop experiment %s", name)

		return nil, err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/stop", "update", name),
		bt.NewResource("experiment", name, "stop"),
		body,
	)

	return body, nil
}
