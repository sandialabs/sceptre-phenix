package scorchexe

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/go-multierror"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/tap"
)

func Execute(ctx context.Context, exp *types.Experiment, run int) error {
	var config ifaces.ScenarioApp

	for _, app := range exp.Apps() {
		if app.Name() == "scorch" {
			config = app

			break
		}
	}

	if config == nil {
		return fmt.Errorf("experiment %s doesn't include a Scorch configuration", exp.Metadata.Name)
	}

	if !exp.Running() {
		return fmt.Errorf("experiment %s is not running", exp.Metadata.Name)
	}

	scorch := app.GetApp("scorch")
	_ = scorch.Init()

	if running := exp.Status.AppRunning()["scorch"]; running {
		return errors.New("the Scorch app is currently already running")
	}

	exp.Status.SetAppRunning("scorch", true)
	exp.Status.SetAppStatus("scorch", scorchmd.ScorchStatus{RunID: run}) //nolint:exhaustruct // partial initialization

	err := exp.WriteToStore(true)
	if err != nil {
		return fmt.Errorf("error updating store with experiment %s: %w", exp.Metadata.Name, err)
	}

	var errors error

	ctx = SetRunID(ctx, run)

	err = scorch.Running(ctx, exp)
	if err != nil {
		errors = multierror.Append(
			errors,
			fmt.Errorf("running Scorch for experiment %s: %w", exp.Metadata.Name, err),
		)
	}

	_ = exp.Reload() // reload experiment from store in case status was updated during run

	var scorchStatus scorchmd.ScorchStatus
	if err := exp.Status.ParseAppStatus("scorch", &scorchStatus); err == nil {
		for name, tp := range scorchStatus.Taps {
			tp.Init(exp.Spec.DefaultBridge(), tap.Experiment(exp.Metadata.Name))

			if delErr := tp.Delete(mm.Headnode()); delErr != nil {
				errors = multierror.Append(
					errors,
					fmt.Errorf("cleaning up remaining scorch tap %s: %w", name, delErr),
				)
			}
		}
	}

	exp.Status.SetAppRunning("scorch", false)
	exp.Status.SetAppStatus("scorch", nil)

	err = exp.WriteToStore(true)
	if err != nil {
		errors = multierror.Append(
			errors,
			fmt.Errorf("error updating store with experiment %s: %w", exp.Metadata.Name, err),
		)
	}

	return errors
}
