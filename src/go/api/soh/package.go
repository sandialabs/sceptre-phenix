package soh

import (
	"phenix/types"
)

func Configured(exp *types.Experiment) bool {
	for _, app := range exp.Spec.Scenario().Apps() {
		if app.Name() == appName {
			return true
		}
	}

	return false
}

// Disabled reports whether the scenario includes the soh app with
// `disabled: true`, so that it does not run at post-start and runs only when
// a user starts it.
func Disabled(exp *types.Experiment) bool {
	for _, app := range exp.Spec.Scenario().Apps() {
		if app.Name() == appName {
			return app.Disabled()
		}
	}

	return false
}

func Initialized(exp *types.Experiment) bool {
	var status map[string]any

	_ = exp.Status.ParseAppStatus("soh", &status)

	// the `_` will prevent a panic
	initialized, _ := status["initialized"].(bool)

	return initialized
}

func Running(exp *types.Experiment) bool {
	return exp.Status.AppRunning()["soh"]
}
