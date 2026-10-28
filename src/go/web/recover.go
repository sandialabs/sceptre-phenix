package web

import (
	"phenix/api/experiment"
	"phenix/util/plog"
)

// recoverExperiments repairs the app state an earlier phenix ui left when it stopped mid-run, then resumes in
// the background the delayed VM starts and post-start apps it left pending.
func recoverExperiments() {
	if err := experiment.ResetAppsRunning(); err != nil {
		plog.Error(plog.TypeSystem, "clearing stale app running flags", "err", err)
	}

	names, err := experiment.PendingStarts()
	if err != nil {
		plog.Error(plog.TypeSystem, "listing experiments with pending starts", "err", err)

		return
	}

	for _, name := range names {
		go resumeStart(name)
	}
}
