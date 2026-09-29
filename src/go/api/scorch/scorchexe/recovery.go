package scorchexe

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"phenix/api/scorch/scorchmd"
	"phenix/types"
)

const ownerLease = 30 * time.Second

func ownerGone(e *scorchmd.Execution, host string) bool {
	if e.Owner != host {
		updated, err := time.Parse(time.RFC3339Nano, e.Updated)
		return err == nil && time.Since(updated) > ownerLease
	}
	return e.PID > 0 && errors.Is(syscall.Kill(e.PID, 0), syscall.ESRCH)
}

// Reconcile fences abandoned owners without releasing their resource claims.
// A live local controller is never displaced merely because a heartbeat lagged.
func Reconcile(name string) error {
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	return scorchmd.Mutate(name, func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
		for _, e := range s.Executions {
			if e.Active() && ownerGone(e, host) {
				e.State = scorchmd.StateInterrupted
				e.Error = "execution owner exited or lost its lease; inspect and clean up resources before recovery"
				e.Wait = ""
				e.Revision++
			}
		}
		return nil
	})
}

// Recover is an explicit acknowledgement after manual cleanup, never a replay
// of setup or a guessed cleanup sequence. Verify the owner on its own host.
func Recover(name string, run int, id string, cleanupComplete bool) error {
	if id == "" || !cleanupComplete {
		return errors.New("execution ID and cleanup acknowledgement are required")
	}
	if err := Reconcile(name); err != nil {
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	return scorchmd.Mutate(name, func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
		e := s.Executions[strconv.Itoa(run)]
		if e == nil || e.ID != id {
			return scorchmd.ErrStaleExecution
		}
		exited := ownerGone(e, host)
		if e.Owner == host && e.PID == os.Getpid() {
			executionsMu.Lock()
			exited = executions[fmt.Sprintf("%s/%d", name, run)] == nil
			executionsMu.Unlock()
		}
		if e.State != scorchmd.StateInterrupted || e.Owner != host || !exited {
			return fmt.Errorf("recovery requires an exited owner on controller host %s", e.Owner)
		}
		e.State = scorchmd.StateFailed
		e.Error = "interrupted execution manually recovered after resource cleanup"
		e.Resources = nil
		e.Taps = nil
		e.Revision++
		e.Updated = time.Now().UTC().Format(time.RFC3339Nano)
		return nil
	})
}
