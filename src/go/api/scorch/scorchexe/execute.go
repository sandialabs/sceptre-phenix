package scorchexe

import (
	"context"
	"fmt"

	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"

	"github.com/hashicorp/go-multierror"
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
	scorch.Init()

	if running := exp.Status.AppRunning()["scorch"]; running {
		return fmt.Errorf("the Scorch app is currently already running")
	}

	exp.Status.SetAppRunning("scorch", true)
	exp.Status.SetAppStatus("scorch", map[string]int{"runID": run})

	if err := exp.WriteToStore(true); err != nil {
		return fmt.Errorf("error updating store with experiment %s: %v", exp.Metadata.Name, err)
	}

	var errors error
	ctx = SetRunID(ctx, run)

	if err := scorch.Running(ctx, exp); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("running Scorch for experiment %s: %w", exp.Metadata.Name, err))
	}

	exp.Status.SetAppRunning("scorch", false)
	exp.Status.SetAppStatus("scorch", nil)

	if err := exp.WriteToStore(true); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("error updating store with experiment %s: %v", exp.Metadata.Name, err))
	}

	return errors
}
