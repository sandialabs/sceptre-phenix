package soh

import (
	"phenix/types"
)

func Configured(exp *types.Experiment) bool {
	for _, app := range exp.Spec.Scenario().Apps() {
		if app.Name() == "soh" {
			return true
		}
	}

	return false
}

func Initialized(exp *types.Experiment) bool {
	var status map[string]any
	exp.Status.ParseAppStatus("soh", &status)

	// the `_` will prevent a panic
	initialized, _ := status["initialized"].(bool)

	return initialized
}

func Running(exp *types.Experiment) bool {
	return exp.Status.AppRunning()["soh"]
}
