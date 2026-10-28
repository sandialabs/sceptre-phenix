package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/app"
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
	// the error metadata of a stop that left the experiment's VMs running.
	stopRetryKey   = "retry"
	stopRetryForce = "force"

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

//nolint:funlen // complex logic
func startExperiment(name string) error {
	err := cache.LockExperimentForStarting(name)
	if err != nil {
		err := weberror.NewWebError(err, "unable to lock experiment %s for starting", name)

		return err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(name)

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/start", "update", name),
		bt.NewResource("experiment", name, "starting"),
		nil,
	)

	type result struct {
		exp *types.Experiment
		err error
	}

	status := make(chan result)

	go func() {
		// We don't want to use the HTTP request's context here.
		ctx, cancel := context.WithCancel(context.Background())
		registered := addCanceler(name, cancel)

		ctx = notes.Context(ctx, false)

		ch := make(chan error)

		if err := experiment.Start(
			ctx,
			experiment.StartWithName(name),
			experiment.StartWithErrorChannel(ch),
		); err != nil {
			dropCanceler(name, registered)

			status <- result{nil, err}
		} else {
			followStart(ctx, name, ch)
		}

		exp, err := experiment.Get(name)

		status <- result{exp, err}
	}()

	var progress float64

	// the experiment list reports the latest progress to pages opened
	// mid-start
	defer startProgress.Delete(name)

	count, _ := vm.Count(name)

	for {
		select {
		case s := <-status:
			if s.err != nil {
				broker.Broadcast(
					bt.NewRequestPolicy("experiments/start", "update", name),
					bt.NewResource("experiment", name, "errorStarting"),
					nil,
				)

				err := weberror.NewWebError(s.err, "unable to start experiment %s", name)

				return err.SetStatus(http.StatusBadRequest)
			}

			// Clear previous SCORCH pipeline runtime state after successful experiment start.
			scorch.DeletePipeline(name, -1, -1, false)

			runPeriodicApps(name, s.exp)

			// Like GET /experiments?vms=false: counts come from the topology and
			// no VM list is asked of minimega. The UI's start handlers replace the
			// experiment row (Experiments.vue) or reload (Scorch, SoH, runs) and
			// never read `vms` from this body.
			body, err := marshaler.Marshal(util.ExperimentSummaryToProtobuf(*s.exp, ""))
			if err != nil {
				err := weberror.NewWebError(err, "unable to start experiment %s", name)

				return err.SetStatus(http.StatusInternalServerError)
			}

			broker.Broadcast(
				bt.NewRequestPolicy("experiments/start", "update", name),
				bt.NewResource("experiment", name, "start"),
				body,
			)

			return nil
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

// runPeriodicApps schedules the periodic `running` stage of experiment name's
// apps, cancelable as a start is.
func runPeriodicApps(name string, exp *types.Experiment) {
	// We don't want to use the HTTP request's context here.
	ctx, cancel := context.WithCancel(context.Background())
	commonMu.Lock()
	cancelers[name] = append(cancelers[name], &canceler{cancel: cancel})

	var wg sync.WaitGroup

	waiters[name] = &wg
	commonMu.Unlock()

	app.PeriodicallyRunApps(ctx, &wg, exp)
}

// rearmAfterFailedStop restarts what stopExperiment canceled before a stop
// that left experiment name running: its periodic apps and pending starts.
func rearmAfterFailedStop(name string) {
	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "re-arming experiment after a failed stop", "exp", name, "err", err)

		return
	}

	runPeriodicApps(name, exp)
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
// it, and how far along its start is while it is starting.
func experimentStatus(exp types.Experiment) (cache.Status, float64) {
	status := cache.ExperimentStatus(exp.Metadata.Name, exp.Running())
	if status != cache.StatusStarting {
		return status, 0
	}

	progress, _ := startProgress.Load(exp.Metadata.Name)
	percent, _ := progress.(float64)

	return status, percent
}

// stopExperiment stops the experiment; force marks it stopped even when minimega cannot kill its VMs,
// which otherwise answers 502 with the metadata retry=force.
func stopExperiment(name string, force bool) ([]byte, error) {
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

	commonMu.Lock()
	cancels := cancelers[name]
	wg := waiters[name]
	delete(cancelers, name)
	delete(waiters, name)
	commonMu.Unlock()

	for _, c := range cancels {
		c.cancel()
	}

	if wg != nil {
		wg.Wait()
	}

	if err := experiment.Stop(name, experiment.StopWithForce(force)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("experiments/stop", "update", name),
			bt.NewResource("experiment", name, "errorStopping"),
			nil,
		)

		werr := weberror.NewWebError(err, "unable to stop experiment %s", name)

		if errors.Is(err, experiment.ErrTeardownFailed) {
			rearmAfterFailedStop(name)

			return nil, werr.WithMetadata(stopRetryKey, stopRetryForce, true).SetStatus(http.StatusBadGateway)
		}

		return nil, werr.SetStatus(http.StatusBadRequest)
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
