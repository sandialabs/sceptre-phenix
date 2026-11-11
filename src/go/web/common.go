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
	ifaces "phenix/types/interfaces"
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

	statusKeyPercent = "percent"
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
	defer cache.KeepExperimentStarting(name)()

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/start", "update", name),
		bt.NewResource("experiment", name, "starting"),
		nil,
	)

	// the post-start progress goes on after the start message, so it waits for it
	sink := newProgressSink(name, experiment.OperationStart, !opts.DryRun, broker.Broadcast)

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
			start  = append(
				opts.StartOptions(name), experiment.StartWithResult(&report), experiment.StartWithProgress(sink.report),
			)
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
			followStart(ctx, name, ch, sink)
		}

		exp, err := experiment.Get(name)

		status <- result{startOutcome{exp: exp, warnings: warnings, mmScript: report.MMScript, periodic: nil}, err}
	}()

	s := <-status
	if s.err != nil {
		sink.release()
		sink.fail(s.err)

		return startOutcome{}, startWebError(s.err, "unable to start experiment %s", name)
	}

	if opts.DryRun {
		sink.end()

		return s.outcome, broadcastDryRun(name, s.outcome.exp)
	}

	// sends the held post-start progress after the start message
	defer sink.release()

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
// background, and reports each error ch gets, until ch closes. The first error
// of a start that was not canceled goes to sink, when there is one.
func followStart(ctx context.Context, name string, ch chan error, sink *progressSink) {
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

			// a stop cancels ctx, and the errors it causes are not failures of the start
			if sink != nil && ctx.Err() == nil {
				sink.fail(err)
			}

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

	followStart(ctx, name, ch, nil)
}

// experimentStatus is the experiment's status as the experiment lists report it, and how far its start or stop is
// while one is in progress, or nil. A start or stop that another process runs gives the progress its marker records.
func experimentStatus(exp types.Experiment) (cache.Status, *ifaces.OperationProgress) {
	name := exp.Metadata.Name
	status := cache.ExperimentStatus(name, exp.Running())

	switch {
	case status == cache.StatusStopped && experiment.StartingElsewhere(exp.Status):
		status = cache.StatusStarting
	case status == cache.StatusStarted && experiment.StoppingElsewhere(exp.Status):
		status = cache.StatusStopping
	}

	if p, ok := liveProgress.get(name); ok {
		return status, &p
	}

	switch status { //nolint:exhaustive // only a start or stop has progress
	case cache.StatusStarting:
		return status, markerProgress(exp.Status.Starting())
	case cache.StatusStopping:
		return status, markerProgress(exp.Status.Stopping())
	default:
		return status, nil
	}
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
	defer cache.KeepExperimentStopping(name)()

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/stop", "update", name),
		bt.NewResource("experiment", name, "stopping"),
		nil,
	)

	sink := newProgressSink(name, experiment.OperationStop, false, broker.Broadcast)
	defer sink.end()

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
		experiment.StopWithProgress(sink.report),
	); err != nil {
		sink.fail(err)

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
