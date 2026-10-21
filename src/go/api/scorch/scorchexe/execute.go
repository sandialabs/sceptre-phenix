package scorchexe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/gofrs/uuid/v5"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
	"phenix/util/plog"
)

const controlInterval = time.Second

var ErrResourceConflict = errors.New("scorch resource conflict")

// Task owns the reservation until execution and finalization have both ended.
type Task struct {
	ID     string
	ctx    context.Context
	cancel context.CancelFunc
	exp    *types.Experiment
	run    int
	done   chan struct{}
	name   string
}

func Prepare(ctx context.Context, exp *types.Experiment, run int) (*Task, error) {
	preflight, err := scorchmd.DecodeMetadata(exp)
	if err != nil {
		return nil, err
	}
	if run < 0 || run >= len(preflight.Runs) {
		return nil, fmt.Errorf("invalid scorch run ID %d", run)
	}
	if err := validateInteractive(ctx, preflight, run); err != nil {
		return nil, err
	}
	exclusive := legacyComponents(ctx, preflight, run)
	if err := Reconcile(exp.Metadata.Name); err != nil {
		return nil, err
	}
	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var reservedExp *types.Experiment
	err = scorchmd.Mutate(exp.Metadata.Name, func(current *types.Experiment, s *scorchmd.ScorchStatus) error {
		if !current.Running() {
			return fmt.Errorf("experiment %s is not running", exp.Metadata.Name)
		}
		if s.Stopping {
			return ErrStopping
		}
		md, err := scorchmd.DecodeMetadata(current)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(preflight, md) {
			return fmt.Errorf("%w: configuration changed during preflight; retry", ErrResourceConflict)
		}
		if run < 0 || run >= len(md.Runs) {
			return fmt.Errorf("invalid Scorch run ID %d", run)
		}
		for loop := md.Runs[run]; loop != nil; loop = loop.Loop {
			for _, stage := range [][]string{loop.Configure, loop.Start, loop.Stop, loop.Cleanup} {
				for _, name := range stage {
					if _, ok := md.ComponentSpecs()[name]; !ok {
						return fmt.Errorf("unknown Scorch component %q", name)
					}
				}
			}
		}
		reservedExp = current
		previous := s.Executions[strconv.Itoa(run)]
		if previous != nil && (previous.Active() || previous.State == scorchmd.StateInterrupted) {
			return ErrAlreadyRunning
		}
		// A legacy running flag has no execution owner; do not reinterpret an old worker.
		if len(s.Executions) == 0 && current.Status.AppRunning()["scorch"] {
			return ErrAlreadyRunning
		}
		claims := resources(md, run)
		if exclusive {
			claims = []string{"*"}
		}
		if err := checkResources(claims, s); err != nil {
			return err
		}
		if _, ok := app.GetApp("scorch").(app.ManagedRunning); !ok {
			return errors.New("external Scorch override does not support managed runs")
		}
		s.Executions[strconv.Itoa(run)] = &scorchmd.Execution{ //nolint:exhaustruct // initial execution state
			ID: id.String(), Run: run, Name: md.RunName(run), State: scorchmd.StateStarting,
			Owner: host, PID: os.Getpid(), Started: now, Updated: now, Revision: 1,
			Controller: "cli", Resources: claims,
		}
		if app.IsContextTriggerUI(ctx) {
			s.Executions[strconv.Itoa(run)].Controller = "web"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	ctx = SetRunID(ctx, run)
	ctx = context.WithValue(ctx, executionKey{}, id.String())
	task := &Task{ID: id.String(), ctx: ctx, cancel: cancel, exp: reservedExp, run: run, done: make(chan struct{}), name: exp.Metadata.Name}
	executionsMu.Lock()
	executions[fmt.Sprintf("%s/%d", exp.Metadata.Name, run)] = task
	executionsMu.Unlock()
	return task, nil
}

func Execute(ctx context.Context, exp *types.Experiment, run int) error {
	task, err := Prepare(ctx, exp, run)
	if err != nil {
		return err
	}
	return task.Run()
}

func (t *Task) Run() error {
	defer t.cancel()
	defer close(t.done)
	defer func() {
		executionsMu.Lock()
		key := fmt.Sprintf("%s/%d", t.name, t.run)
		if current := executions[key]; current != nil && current.ID == t.ID {
			delete(executions, key)
		}
		executionsMu.Unlock()
	}()
	monitorDone := make(chan struct{})
	monitorJoined := make(chan struct{})
	go func() { defer close(monitorJoined); t.monitor(monitorDone) }()
	stopMonitor := func() { close(monitorDone); <-monitorJoined }
	defer func() {
		if monitorDone != nil {
			stopMonitor()
		}
	}()
	if err := t.exp.Reload(); err != nil {
		return errors.Join(err, t.finish(err))
	}
	scorch := app.GetApp("scorch")
	if err := scorch.Init(); err != nil {
		return errors.Join(err, t.finish(err))
	}
	err := State(t.ctx, t.name, scorchmd.StateRunning, "", "", 0, 0, "", "")
	if err == nil {
		err = scorch.Running(t.ctx, t.exp)
	}
	stopMonitor()
	monitorDone = nil
	return errors.Join(err, t.finish(err))
}

func (t *Task) finish(result error) error {
	return scorchmd.UpdateExecution(t.name, t.run, t.ID, func(e *scorchmd.Execution) error {
		e.State = scorchmd.StateSucceeded
		if result != nil {
			e.State = scorchmd.StateFailed
			e.Error = result.Error()
		}
		if t.ctx.Err() != nil {
			e.State = scorchmd.StateCanceled
		}
		if len(e.Taps) > 0 {
			e.State = scorchmd.StateInterrupted
			e.Error = "tap cleanup failed; manual resource cleanup and recovery required"
		}
		e.Wait = ""
		e.Deadline = ""
		e.Updated = time.Now().UTC().Format(time.RFC3339Nano)
		e.Revision++
		return nil
	})
}

func (t *Task) monitor(done <-chan struct{}) {
	ticker := time.NewTicker(controlInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			var requested bool
			err := scorchmd.UpdateExecution(t.name, t.run, t.ID, func(e *scorchmd.Execution) error {
				if !e.Active() {
					return scorchmd.ErrStaleExecution
				}
				requested = e.CancelRequested
				e.Updated = time.Now().UTC().Format(time.RFC3339Nano)
				return nil
			})
			if err != nil {
				plog.Error(plog.TypeScorch, "Scorch execution lost store ownership", "err", err, "execution", t.ID)
				t.cancel()
				return
			}
			if requested {
				t.cancel()
			}
		}
	}
}

func State(ctx context.Context, name, state, stage, component string, loop, count int, wait, deadline string) error {
	id := ExecutionID(ctx)
	if id == "" {
		return nil
	}
	return scorchmd.UpdateExecution(name, MustRunID(ctx), id, func(e *scorchmd.Execution) error {
		if e.CancelRequested && state != scorchmd.StateFinalizing {
			return context.Canceled
		}
		e.State, e.Stage, e.Component = state, stage, component
		e.Loop, e.Count, e.Wait, e.Deadline = loop, count, wait, deadline
		e.Updated = time.Now().UTC().Format(time.RFC3339Nano)
		e.Revision++
		return nil
	})
}
