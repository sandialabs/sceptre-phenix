package scorch

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"phenix/types"

	"strconv"

	"golang.org/x/sys/unix"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/tap"
)

func cleanupOwnedTaps(ctx context.Context, exp *types.Experiment, run int) error {
	current, err := experiment.Get(exp.Metadata.Name)
	if err != nil {
		return err
	}
	status, err := scorchmd.Status(current)
	if err != nil {
		return err
	}
	execution := status.Executions[strconv.Itoa(run)]
	if execution == nil || execution.ID != scorchexe.ExecutionID(ctx) {
		return scorchmd.ErrStaleExecution
	}
	var result error
	for name := range execution.Taps {
		result = errors.Join(result, deleteOwnedTap(ctx, NewOptions(Experiment(*exp), RunID(run), Name(name))))
	}
	return result
}

// Hold the host lock through allocation AND persistence so other processes see
// the allocated subnet before attempting their own allocation.
func tapLock(fn func() error) error {
	dir := filepath.Join(common.PhenixBase, ".scorch-control")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "taps.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	return fn()
}

func createOwnedTap(ctx context.Context, o Options, tp *tap.Tap) error {
	return tapLock(func() error {
		pairs, err := discoverUsedPairs()
		if err != nil {
			return err
		}
		tp.Init(o.Exp.Spec.DefaultBridge(), tap.Experiment(o.Exp.Metadata.Name), tap.UsedPairs(pairs))
		if _, err := tp.Create(mm.Headnode()); err != nil {
			return err
		}
		err = scorchexeTap(ctx, o, func(e *scorchmd.Execution) error {
			if e.Taps == nil {
				e.Taps = make(map[string]*tap.Tap)
			}
			e.Taps[o.Name] = tp
			return nil
		})
		if err != nil {
			_ = tp.Delete(mm.Headnode())
		}
		return err
	})
}

func scorchexeTap(ctx context.Context, o Options, fn func(*scorchmd.Execution) error) error {
	id := scorchexe.ExecutionID(ctx)
	if id == "" {
		return errors.New("tap requires a managed Scorch execution")
	}
	return scorchmd.UpdateExecution(o.Exp.Metadata.Name, o.Run, id, fn)
}

func deleteOwnedTap(ctx context.Context, o Options) error {
	return tapLock(func() error {
		exp, err := experiment.Get(o.Exp.Metadata.Name)
		if err != nil {
			return err
		}
		status, err := scorchmd.Status(exp)
		if err != nil {
			return err
		}
		e := status.Executions[strconv.Itoa(o.Run)]
		if e == nil || e.ID != scorchexe.ExecutionID(ctx) {
			return scorchmd.ErrStaleExecution
		}
		tp := e.Taps[o.Name]
		if tp == nil {
			return nil
		}
		tp.Init(o.Exp.Spec.DefaultBridge(), tap.Experiment(o.Exp.Metadata.Name))
		if err := tp.Delete(mm.Headnode()); err != nil {
			return err
		}
		return scorchexeTap(ctx, o, func(current *scorchmd.Execution) error { delete(current.Taps, o.Name); return nil })
	})
}
