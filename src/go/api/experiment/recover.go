package experiment

import (
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/go-multierror"

	"phenix/types"
	"phenix/util/plog"
)

// ResetAppsRunning clears the app running flags of every running experiment, which phenix ui calls as it
// starts: flags still set then were left by a phenix that stopped mid-run and would keep the app from running.
func ResetAppsRunning() error {
	exps, err := types.Experiments(true)
	if err != nil {
		return fmt.Errorf("listing running experiments: %w", err)
	}

	var errs error

	for _, exp := range exps {
		running := exp.Status.AppRunning()

		var cleared []string

		for _, name := range slices.Sorted(maps.Keys(running)) {
			if running[name] {
				exp.Status.SetAppRunning(name, false)
				cleared = append(cleared, name)
			}
		}

		if len(cleared) == 0 {
			continue
		}

		if err := exp.WriteToStore(true); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("clearing app running flags of experiment %s: %w", exp.Metadata.Name, err))

			continue
		}

		plog.Warn(
			plog.TypeSystem,
			"cleared app running flags left by an earlier phenix",
			"exp", exp.Metadata.Name, "apps", cleared,
		)
	}

	return errs //nolint:wrapcheck // returning multierror
}
