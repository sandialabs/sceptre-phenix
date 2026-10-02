package soh

import (
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

func TestGetForStoppedExperimentShowsItsTopology(t *testing.T) {
	t.Parallel()

	dnb := true
	node := func(name string, dnb *bool, vlans ...string) *v1.Node {
		ifaces := make([]*v1.Interface, 0, len(vlans))
		for _, vlan := range vlans {
			ifaces = append(ifaces, &v1.Interface{NameF: vlan, VLANF: vlan})
		}

		return &v1.Node{
			TypeF:     "VirtualMachine",
			GeneralF:  &v1.General{HostnameF: name, DoNotBootF: dnb},
			HardwareF: &v1.Hardware{},
			NetworkF:  &v1.Network{InterfacesF: ifaces},
		}
	}

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			TopologyF: &v1.TopologySpec{
				NodesF: []*v1.Node{node("a", nil, "LAN", "MGMT"), node("b", &dnb, "LAN")},
			},
		},
		Status: &v1.ExperimentStatus{},
	}

	network, err := GetFor(exp, "")
	if err != nil {
		t.Fatal(err)
	}

	// two VMs plus the LAN switch (MGMT is ignored)
	if len(network.Nodes) != 3 || len(network.Edges) != 2 {
		t.Fatalf("got %d nodes, %d edges: %+v", len(network.Nodes), len(network.Edges), network)
	}

	if network.Nodes[0].Status != "notdeploy" || network.Nodes[1].Status != "ignore" || network.Nodes[2].Status != "notboot" {
		t.Errorf("unexpected states: %+v", network.Nodes)
	}

	filtered, err := GetFor(exp, "notboot")
	if err != nil {
		t.Fatal(err)
	}

	if len(filtered.Nodes) != 2 || filtered.Nodes[0].Label != "b" {
		t.Errorf("status filter not applied: %+v", filtered.Nodes)
	}

	hosts, flows, err := GetFlowsFor(exp)
	if err != nil || hosts != nil || flows != nil {
		t.Errorf("GetFlowsFor without SoH status = %v, %v, %v", hosts, flows, err)
	}
}
