package experiment

import (
	"context"
	"errors"
	"fmt"

	"phenix/app"
	"phenix/scheduler"
	"phenix/types"
)

// ErrAppRuntime is wrapped by the error of AppInput when it cannot read the
// experiment's hosts and VMs from minimega.
var ErrAppRuntime = errors.New("reading the runtime details of the experiment")

// ErrSchedulerHosts is wrapped by the error of SchedulerInput when it cannot
// read the cluster hosts from minimega.
var ErrSchedulerHosts = errors.New("reading the cluster hosts for a user scheduler")

// AppInput returns the experiment name as phenix gives it to a user app on
// stdin: the stored experiment, with the cluster's schedulable hosts and the
// experiment's VMs as minimega reports them. Its minimega commands give up
// once ctx is done. A read from minimega that fails wraps ErrAppRuntime.
func AppInput(ctx context.Context, name string) (*types.Experiment, error) {
	exp, err := Get(name)
	if err != nil {
		return nil, err
	}

	if err := app.PopulateObservedRuntime(ctx, exp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAppRuntime, err)
	}

	return exp, nil
}

// SchedulerInput returns the JSON that a user scheduler gets on stdin when
// phenix schedules the stored experiment name (see scheduler.UserInput). It
// changes the experiment only in memory and stores nothing. Its read of the
// cluster hosts gives up once ctx is done or after scheduler.UserTimeout, as
// in a user scheduler run. A host read that fails wraps ErrSchedulerHosts,
// and also scheduler.ErrTimeout when the time limit ends it.
func SchedulerInput(ctx context.Context, name string) ([]byte, error) {
	exp, err := Get(name)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, scheduler.UserTimeout)
	defer cancel()

	data, err := scheduler.UserInput(ctx, exp.Spec)

	switch {
	case err == nil:
		return data, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("%w: %w: not done within %v: %w", ErrSchedulerHosts, scheduler.ErrTimeout, scheduler.UserTimeout, err)
	}

	return nil, fmt.Errorf("%w: %w", ErrSchedulerHosts, err)
}
