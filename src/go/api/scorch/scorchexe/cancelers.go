package scorchexe

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/types"
)

var (
	executionsMu      sync.Mutex               //nolint:gochecknoglobals // process-local execution handles
	executions        = make(map[string]*Task) //nolint:gochecknoglobals // process-local execution handles
	ErrAlreadyRunning = errors.New("scorch run is already active or requires recovery")
	ErrStopping       = errors.New("experiment is stopping")
)

func RequestCancel(name string, run int, id string) error {
	targetID := ""
	err := scorchmd.Mutate(name, func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
		e := s.Executions[strconv.Itoa(run)]
		if e == nil {
			return nil
		}
		if id != "" && e.ID != id {
			return scorchmd.ErrStaleExecution
		}
		targetID = e.ID
		if e.Active() {
			e.CancelRequested = true
			e.State = scorchmd.StateCanceling
			e.Revision++
		}
		return nil
	})
	if err != nil {
		return err
	}
	executionsMu.Lock()
	task := executions[fmt.Sprintf("%s/%d", name, run)]
	if task != nil && task.ID == targetID {
		task.cancel()
	}
	executionsMu.Unlock()
	return nil
}

// Drain prevents new admission and waits for every owner to finish before VMs
// are destroyed. Persisted cancellation also reaches standalone CLI processes.
func Drain(ctx context.Context, name string) error {
	if err := Reconcile(name); err != nil {
		return err
	}
	err := scorchmd.Mutate(name, func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
		for _, e := range s.Executions {
			if e.State == scorchmd.StateInterrupted {
				return fmt.Errorf("run %d requires manual recovery before stopping", e.Run)
			}
		}
		s.Stopping = true
		for _, e := range s.Executions {
			if e.Active() {
				e.CancelRequested = true
				e.State = scorchmd.StateCanceling
				e.Revision++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(controlInterval)
	defer ticker.Stop()
	for {
		c, err := store.NewConfig("experiment/" + name)
		if err != nil {
			return err
		}
		if err := store.Get(c); err != nil {
			return err
		}
		exp, err := types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return err
		}
		s, err := scorchmd.Status(exp)
		if err != nil {
			return err
		}
		for _, e := range s.Executions {
			if e.State == scorchmd.StateInterrupted {
				return fmt.Errorf("run %d requires manual recovery before stopping", e.Run)
			}
		}
		if len(s.ActiveRuns()) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("draining Scorch executions: %w", ctx.Err())
		case <-ticker.C:
			if err := Reconcile(name); err != nil {
				return err
			}
			for _, e := range s.Executions {
				if e.State == scorchmd.StateInterrupted {
					return fmt.Errorf("run %d requires manual recovery before stopping", e.Run)
				}
			}
		}
	}
}
