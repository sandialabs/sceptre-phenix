package experiment

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/shell"
)

// StartMarkerMaxAge is how long the start or stop marker of another host stays live: phenix cannot check the process
// of another host, so it uses the age of the marker.
const StartMarkerMaxAge = 24 * time.Hour

// maxClaimAttempts is how many times Start reads the experiment again when it changes before the marker is stored.
const maxClaimAttempts = 3

// ErrExperimentStarting is the cause of a [StartingError].
var ErrExperimentStarting = errors.New("experiment already starting")

// StartingError is the error of a start that Start refuses, because another live process starts the experiment.
type StartingError struct {
	Name   string
	Marker ifaces.StartMarker
}

// Error names the experiment and the process that starts it.
func (e *StartingError) Error() string {
	host, _ := os.Hostname()
	if e.Marker.Host != host {
		return fmt.Sprintf("experiment %s is already starting (process %d on host %s since %s)",
			e.Name, e.Marker.PID, e.Marker.Host, e.Marker.Since)
	}

	return fmt.Sprintf("experiment %s is already starting (process %d since %s)", e.Name, e.Marker.PID, e.Marker.Since)
}

// Unwrap gives ErrExperimentStarting.
func (e *StartingError) Unwrap() error {
	return ErrExperimentStarting
}

// StartMarkerLive reports whether the start or stop that m records can still be in progress. A marker of this host is
// live while its process runs. A marker of another host is live until it is StartMarkerMaxAge old.
func StartMarkerLive(m ifaces.StartMarker) bool {
	host, _ := os.Hostname()

	return StartMarkerLiveOn(m, host, time.Now(), shell.ProcessExists)
}

// StartMarkerLiveOn is StartMarkerLive for the host named host at the time now. The function exists tells if a
// process of host runs.
func StartMarkerLiveOn(m ifaces.StartMarker, host string, now time.Time, exists func(pid int) bool) bool {
	if m.IsZero() {
		return false
	}

	if m.Host == host {
		return m.PID > 0 && exists(m.PID)
	}

	since, err := time.Parse(time.RFC3339, m.Since)

	return err == nil && now.Sub(since) < StartMarkerMaxAge
}

// StartingElsewhere reports whether status records a live start marker of a stopped experiment, such as when
// phenix experiment start runs in another process.
func StartingElsewhere(status ifaces.ExperimentStatus) bool {
	return status != nil && status.StartTime() == "" && StartMarkerLive(status.Starting())
}

// StoppingElsewhere reports whether status records a live stop marker of a running experiment, such as when
// phenix experiment stop runs in another process.
func StoppingElsewhere(status ifaces.ExperimentStatus) bool {
	return status != nil && status.StartTime() != "" && StartMarkerLive(status.Stopping())
}

// newStartMarker is the start or stop marker of this process for an operation that begins at now.
func newStartMarker(now time.Time) ifaces.StartMarker {
	host, _ := os.Hostname()

	return ifaces.StartMarker{
		Since:    now.UTC().Format(time.RFC3339),
		PID:      os.Getpid(),
		Host:     host,
		Progress: ifaces.OperationProgress{}, //nolint:exhaustruct // no progress
	}
}

// claimStart reads experiment name for Start and stores the start marker of this process in its status. It refuses an
// experiment that runs, or that another live process starts. It reads again when the experiment changes before the
// write.
func claimStart(name string) (*store.Config, *types.Experiment, ifaces.StartMarker, error) {
	for attempt := 1; ; attempt++ {
		c, _ := store.NewConfig("experiment/" + name)

		if err := store.Get(c); err != nil {
			return nil, nil, ifaces.StartMarker{}, fmt.Errorf("getting experiment %s from store: %w", name, notFound(err))
		}

		exp, err := types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return nil, nil, ifaces.StartMarker{}, fmt.Errorf("decoding experiment from config: %w", err)
		}

		if exp.Running() && !exp.DryRun() {
			return nil, nil, ifaces.StartMarker{}, fmt.Errorf("%w (started at: %s)", ErrExperimentRunning, exp.Status.StartTime())
		}

		if m := exp.Status.Starting(); StartMarkerLive(m) {
			return nil, nil, ifaces.StartMarker{}, &StartingError{Name: name, Marker: m}
		}

		marker := newStartMarker(time.Now())
		exp.Status.SetStarting(marker)

		c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)
		c.IfRevision = c.Metadata.Revision

		err = store.Update(c)
		c.IfRevision = 0

		switch {
		case errors.Is(err, store.ErrConflict) && attempt < maxClaimAttempts:
			continue
		case err != nil:
			return nil, nil, ifaces.StartMarker{}, fmt.Errorf("recording the start of experiment %s: %w", name, err)
		}

		return c, exp, marker, nil
	}
}

// dropStartMarker removes the start marker m from the stored status of experiment name, if the status still holds m.
// It changes no other part of the status.
func dropStartMarker(name string, m ifaces.StartMarker) error {
	return updateStatus(name, func(s ifaces.ExperimentStatus) bool {
		if !s.Starting().Same(m) {
			return false
		}

		var none ifaces.StartMarker

		s.SetStarting(none)

		return true
	})
}

// dropStopMarker removes the stop marker m from the stored status of experiment name, if the status still holds m.
// It changes no other part of the status.
func dropStopMarker(name string, m ifaces.StopMarker) error {
	return updateStatus(name, func(s ifaces.ExperimentStatus) bool {
		if !s.Stopping().Same(m) {
			return false
		}

		var none ifaces.StopMarker

		s.SetStopping(none)

		return true
	})
}
