package store

// preserveScorch protects controller-owned state from older whole-experiment
// snapshots written by generic or periodic apps. Only Mutate may change it.
func preserveScorch(current, incoming *Config) {
	if current.Kind != "Experiment" || current.Status == nil {
		return
	}
	apps, _ := current.Status["apps"].(map[string]any)
	scorch, _ := apps["scorch"].(map[string]any)
	if _, managed := scorch["executions"]; !managed {
		return
	}
	if incoming.Status == nil {
		incoming.Status = make(map[string]any)
	}
	nextApps, _ := incoming.Status["apps"].(map[string]any)
	if nextApps == nil {
		nextApps = make(map[string]any)
		incoming.Status["apps"] = nextApps
	}
	nextApps["scorch"] = scorch
	running, _ := current.Status["appRunningStageStatus"].(map[string]any)
	nextRunning, _ := incoming.Status["appRunningStageStatus"].(map[string]any)
	if nextRunning == nil {
		nextRunning = make(map[string]any)
		incoming.Status["appRunningStageStatus"] = nextRunning
	}
	nextRunning["scorch"] = running["scorch"]
}
