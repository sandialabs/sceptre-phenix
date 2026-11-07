package experiment

import (
	"context"
	"errors"
	"fmt"

	"phenix/app"
	"phenix/types"
)

// ErrAppRuntime is wrapped by the error of AppInput when it cannot read the
// experiment's hosts and VMs from minimega.
var ErrAppRuntime = errors.New("reading the runtime details of the experiment")

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
