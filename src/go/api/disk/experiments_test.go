package disk

import (
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

func testExperiment(t *testing.T, name, startTime string, images ...string) types.Experiment {
	t.Helper()

	drives := make([]*v1.Drive, 0, len(images))
	for _, image := range images {
		drives = append(drives, &v1.Drive{ImageF: image})
	}

	node := &v1.Node{
		TypeF:     "VirtualMachine",
		GeneralF:  &v1.General{HostnameF: "host"},
		HardwareF: &v1.Hardware{DrivesF: drives},
	}

	spec := &v1.ExperimentSpec{
		ExperimentNameF: name,
		TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{node}},
	}

	return types.Experiment{
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     spec,
		Status:   &v1.ExperimentStatus{StartTimeF: startTime},
	}
}
