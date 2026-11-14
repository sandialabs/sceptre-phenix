package experiment

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/hashicorp/go-multierror"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/plog"
	"phenix/util/shell"
)

// ResetAppsRunning clears the app running flags of every running experiment, which phenix ui calls as it
// starts: flags still set then were left by a phenix that stopped mid-run and would keep the app from running.
func ResetAppsRunning() error {
	exps, err := recoverableExperiments(true, "app running flag reset")
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

// ClearStaleMarkers removes the start and stop markers whose operation cannot be in progress, which phenix ui calls
// as it starts. Stale markers are those of a process of this host that does not run, of this process, and of another
// host that are StartMarkerMaxAge old.
func ClearStaleMarkers() error {
	exps, err := recoverableExperiments(false, "start and stop marker cleanup")
	if err != nil {
		return fmt.Errorf("listing experiments: %w", err)
	}

	var (
		errs    error
		host, _ = os.Hostname()
		now     = time.Now()
		self    = os.Getpid()
	)

	// this process starts and stops no experiment before it recovers them
	stale := func(m ifaces.StartMarker) bool {
		return !m.IsZero() && (!StartMarkerLiveOn(m, host, now, shell.ProcessExists) || (m.Host == host && m.PID == self))
	}

	for _, exp := range exps {
		name := exp.Metadata.Name

		if m := exp.Status.Starting(); stale(m) {
			if err := dropStartMarker(name, m); err != nil {
				errs = multierror.Append(errs, fmt.Errorf("clearing the start marker of experiment %s: %w", name, err))
			} else {
				plog.Warn(plog.TypeSystem, "cleared a stale experiment start marker",
					"exp", name, "pid", m.PID, "host", m.Host, "since", m.Since)
			}
		}

		if m := exp.Status.Stopping(); stale(m) {
			if err := dropStopMarker(name, m); err != nil {
				errs = multierror.Append(errs, fmt.Errorf("clearing the stop marker of experiment %s: %w", name, err))
			} else {
				plog.Warn(plog.TypeSystem, "cleared a stale experiment stop marker",
					"exp", name, "pid", m.PID, "host", m.Host, "since", m.Since)
			}
		}
	}

	return errs //nolint:wrapcheck // returning multierror
}

// recoverableExperiments returns the stored experiments that decode, or only the running ones when running is true.
// It logs a warning that names each stored config that does not decode and the recovery pass that skips it, so that
// one bad config does not stop the pass for the other experiments.
func recoverableExperiments(running bool, pass string) ([]*types.Experiment, error) {
	exps, failed, err := types.DecodableExperiments(running)
	if err != nil {
		return nil, err //nolint:wrapcheck // the callers wrap it
	}

	for _, f := range failed {
		plog.Warn(
			plog.TypeSystem, pass+" skips an experiment config that does not decode",
			"exp", f.Name, "err", f.Err,
		)
	}

	return exps, nil
}
