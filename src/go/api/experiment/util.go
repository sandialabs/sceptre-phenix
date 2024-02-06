package experiment

func ClusterNodes(exp string) ([]string, error) {
	nodeMap := make(map[string]struct{})

	spec, err := Get(exp)
	if err != nil {
		return nil, ErrExperimentNotFound
	}

	if !spec.Running() {
		return nil, ErrExperimentNotRunning
	}

	for _, node := range spec.Status.Schedules() {
		nodeMap[node] = struct{}{}
	}

	var nodes []string

	for node := range nodeMap {
		nodes = append(nodes, node)
	}

	return nodes, nil
}
