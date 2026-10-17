package soh

import (
	"testing"

	"github.com/google/go-cmp/cmp"

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

	lan := Node{ID: 3, Label: "LAN", Image: "switch", Status: "ignore"}

	network, err := GetFor(t.Context(), exp, "")
	if err != nil {
		t.Fatalf("GetFor(exp, \"\") error = %v", err)
	}

	// two VMs plus the LAN switch (MGMT is ignored)
	want := &Network{
		Nodes: []Node{{ID: 0, Label: "a", Status: "notdeploy"}, lan, {ID: 1, Label: "b", Status: "notboot"}},
		Edges: []Edge{{ID: 0, Source: 0, Target: 3, Length: defaultEdgeLength}, {ID: 1, Source: 1, Target: 3, Length: defaultEdgeLength}},
	}
	if diff := cmp.Diff(want, network); diff != "" {
		t.Errorf("GetFor(exp, \"\") mismatch (-want +got):\n%s", diff)
	}

	filtered, err := GetFor(t.Context(), exp, "notboot")
	if err != nil {
		t.Fatalf("GetFor(exp, notboot) error = %v", err)
	}

	want = &Network{
		Nodes: []Node{{ID: 1, Label: "b", Status: "notboot"}, lan},
		Edges: []Edge{{ID: 0, Source: 1, Target: 3, Length: defaultEdgeLength}},
	}
	if diff := cmp.Diff(want, filtered); diff != "" {
		t.Errorf("GetFor(exp, notboot) mismatch (-want +got):\n%s", diff)
	}

	hosts, flows, err := GetFlowsFor(exp)
	if err != nil || hosts != nil || flows != nil {
		t.Errorf("GetFlowsFor(exp without SoH status) = %v, %v, %v; want nil, nil, nil", hosts, flows, err)
	}
}
