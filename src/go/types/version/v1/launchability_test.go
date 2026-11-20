package v1

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	ifaces "phenix/types/interfaces"
)

func TestNodeLaunchabilitySemantics(t *testing.T) {
	trueValue := true

	tests := []struct {
		name            string
		node            Node
		managedByPhenix bool
		launchable      bool
		schedulable     bool
	}{
		{
			name: "internal node is managed launchable and schedulable",
			node: Node{
				GeneralF: &General{HostnameF: "vm"},
			},
			managedByPhenix: true,
			launchable:      true,
			schedulable:     true,
		},
		{
			name: "external node is not managed launchable or schedulable",
			node: Node{
				GeneralF:  &General{HostnameF: "external"},
				ExternalF: &trueValue,
			},
			managedByPhenix: false,
			launchable:      false,
			schedulable:     false,
		},
		{
			name: "do not boot node is managed and schedulable but not launchable",
			node: Node{
				GeneralF: &General{
					HostnameF:  "held",
					DoNotBootF: &trueValue,
				},
			},
			managedByPhenix: true,
			launchable:      false,
			schedulable:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.node.ManagedByPhenix(); got != test.managedByPhenix {
				t.Errorf("Node.ManagedByPhenix() = %t, want %t", got, test.managedByPhenix)
			}

			if got := test.node.Launchable(); got != test.launchable {
				t.Errorf("Node.Launchable() = %t, want %t", got, test.launchable)
			}

			if got := test.node.Schedulable(); got != test.schedulable {
				t.Errorf("Node.Schedulable() = %t, want %t", got, test.schedulable)
			}
		})
	}
}

func TestTopologyLaunchabilityHelpers(t *testing.T) {
	trueValue := true
	topology := &TopologySpec{
		NodesF: []*Node{
			{GeneralF: &General{HostnameF: "vm"}},
			{
				GeneralF:  &General{HostnameF: "external"},
				ExternalF: &trueValue,
			},
			{
				GeneralF: &General{
					HostnameF:  "held",
					DoNotBootF: &trueValue,
				},
			},
		},
	}

	if diff := cmp.Diff([]string{"vm"}, nodeNames(topology.LaunchableNodes())); diff != "" {
		t.Errorf("TopologySpec.LaunchableNodes() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"vm"}, topology.LaunchableVMNames()); diff != "" {
		t.Errorf("TopologySpec.LaunchableVMNames() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"vm", "held"}, nodeNames(topology.SchedulableNodes(""))); diff != "" {
		t.Errorf("TopologySpec.SchedulableNodes(\"\") mismatch (-want +got):\n%s", diff)
	}
}

func nodeNames(nodes []ifaces.NodeSpec) []string {
	names := make([]string, len(nodes))

	for i, node := range nodes {
		names[i] = node.General().Hostname()
	}

	return names
}
