package web

import (
	"testing"
)

// useVMExperiment stores the experiment exp, running unless stopped, with VMs
// of the given names, and a minimega that knows of none of them.
func useVMExperiment(t *testing.T, running bool, names ...string) {
	t.Helper()

	nodes := make([]map[string]any, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, diskVM(name, "a.qc2"))
	}

	started := ""
	if running {
		started = "2024-01-01T00:00:00Z"
	}

	useTestStore(t, namedExperiment(t, "exp", started, nodes...))
	useTestMM(t, new(testMM))
}
