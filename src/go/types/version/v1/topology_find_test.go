package v1

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	ifaces "phenix/types/interfaces"
)

func hostnames(nodes []ifaces.NodeSpec) []string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.General().Hostname())
	}

	return names
}

func findTopology() *TopologySpec {
	dnb, boot, external := true, false, true

	return &TopologySpec{
		IncludeTopologiesF: []string{"base"},
		NodesF: []*Node{
			{
				GeneralF: &General{HostnameF: "web", DoNotBootF: &boot},
				LabelsF:  map[string]string{"role": "web"},
				NetworkF: &Network{InterfacesF: []*Interface{{VLANF: "EXP"}}},
			},
			{
				GeneralF:  &General{HostnameF: "db", DoNotBootF: &dnb},
				LabelsF:   map[string]string{"role": "db", "tier": "back"},
				NetworkF:  &Network{InterfacesF: []*Interface{{VLANF: "MGMT"}, {VLANF: "EXP"}}},
				CommandsF: []string{"echo"},
			},
			{
				GeneralF:  &General{HostnameF: "ext"},
				ExternalF: &external,
				DelayF:    &Delay{UserF: true},
				NetworkF:  &Network{},
			},
		},
	}
}

func TestTopologyNilReceiver(t *testing.T) {
	t.Parallel()

	var topo *TopologySpec

	if topo.IncludedTopologies() != nil || topo.Nodes() != nil || topo.BootableNodes() != nil || topo.SchedulableNodes("") != nil {
		t.Error("accessors of a nil *TopologySpec return values, want nil")
	}
}

func TestTopologyFindNodes(t *testing.T) {
	t.Parallel()

	topo := findTopology()

	if diff := cmp.Diff([]string{"base"}, topo.IncludedTopologies()); diff != "" {
		t.Errorf("IncludedTopologies() (-want +got):\n%s", diff)
	}

	tests := []struct {
		name string
		got  []ifaces.NodeSpec
		want []string
	}{
		{name: "Nodes", got: topo.Nodes(), want: []string{"web", "db", "ext"}},
		{name: "BootableNodes", got: topo.BootableNodes(), want: []string{"web", "ext"}},
		{name: "SchedulableNodes", got: topo.SchedulableNodes("minimega"), want: []string{"web", "db"}},
		{name: "FindNodesWithLabels role", got: topo.FindNodesWithLabels("role"), want: []string{"web", "db"}},
		{name: "FindNodesWithLabels any of two", got: topo.FindNodesWithLabels("tier", "role"), want: []string{"web", "db"}},
		{name: "FindNodesWithLabels none", got: topo.FindNodesWithLabels("missing"), want: []string{}},
		{name: "FindDelayedNodes", got: topo.FindDelayedNodes(), want: []string{"ext"}},
		{name: "FindNodesWithVLAN EXP", got: topo.FindNodesWithVLAN("EXP"), want: []string{"web", "db"}},
		{name: "FindNodesWithVLAN MGMT", got: topo.FindNodesWithVLAN("MGMT"), want: []string{"db"}},
	}

	for _, tt := range tests {
		if diff := cmp.Diff(tt.want, hostnames(tt.got)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
	}

	if got := topo.FindNodeByName("db"); got == nil || got.General().Hostname() != "db" {
		t.Errorf("FindNodeByName(db) = %v, want the db node", got)
	}

	if got := topo.FindNodeByName("missing"); got != nil {
		t.Errorf("FindNodeByName(missing) = %v, want nil", got)
	}

	if !topo.HasCommands() {
		t.Error("HasCommands() = false, want true")
	}

	if (&TopologySpec{NodesF: topo.NodesF[:1]}).HasCommands() {
		t.Error("HasCommands() without commands = true, want false")
	}
}

func TestTopologyAddRemoveNode(t *testing.T) {
	t.Parallel()

	topo := &TopologySpec{}

	added := topo.AddNode("VirtualMachine", "a")
	topo.AddNode("Router", "b")
	topo.AddNode("Switch", "c")

	if added.Type() != "VirtualMachine" || added.General().Hostname() != "a" {
		t.Errorf("AddNode() = %+v, want a VirtualMachine named a", added)
	}

	topo.RemoveNode("b")
	topo.RemoveNode("missing")

	if diff := cmp.Diff([]string{"a", "c"}, hostnames(topo.Nodes())); diff != "" {
		t.Errorf("nodes after RemoveNode(b) and RemoveNode(missing) (-want +got):\n%s", diff)
	}
}
