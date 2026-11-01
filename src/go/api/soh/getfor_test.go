package soh

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

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
		Hosts: []string{}, HostFlows: [][]int{},
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
		Hosts: []string{}, HostFlows: [][]int{},
	}
	if diff := cmp.Diff(want, filtered); diff != "" {
		t.Errorf("GetFor(exp, notboot) mismatch (-want +got):\n%s", diff)
	}

	hosts, flows, err := GetFlowsFor(exp)
	if err != nil || hosts != nil || flows != nil {
		t.Errorf("GetFlowsFor(exp without SoH status) = %v, %v, %v; want nil, nil, nil", hosts, flows, err)
	}
}

// genGetForNodes draws a stopped experiment's VMs, on VLANs some of which the
// network leaves out or names with an alias.
func genGetForNodes() *rapid.Generator[[]*v1.Node] {
	node := rapid.Custom(func(t *rapid.T) *v1.Node {
		vlans := rapid.SliceOfN(rapid.SampledFrom([]string{"LAN", "WAN", "DMZ (12)", "MGMT", "mgmt", "MIRROR"}), 0, 3).Draw(t, "vlans")
		ifaces := make([]*v1.Interface, 0, len(vlans))

		for i, vlan := range vlans {
			ifaces = append(ifaces, &v1.Interface{NameF: "eth" + strconv.Itoa(i), VLANF: vlan})
		}

		dnb, external := rapid.Bool().Draw(t, "dnb"), rapid.Bool().Draw(t, "external")

		return &v1.Node{
			TypeF:     "VirtualMachine",
			GeneralF:  &v1.General{HostnameF: rapid.SampledFrom([]string{"a", "b", "c", "d", "e"}).Draw(t, "host"), DoNotBootF: &dnb},
			HardwareF: &v1.Hardware{},
			NetworkF:  &v1.Network{InterfacesF: ifaces},
			ExternalF: &external,
		}
	})

	return rapid.SliceOfNDistinct(node, 0, 5, func(n *v1.Node) string { return n.GeneralF.HostnameF })
}

// getForLinks returns the network's VM nodes, by ID, and each edge as "VM switch".
func getForLinks(t *rapid.T, network *Network) (map[int]Node, []string) {
	var (
		vms      = make(map[int]Node)
		switches = make(map[int]string)
		linked   = make(map[int]bool)
		links    = make([]string, 0, len(network.Edges))
	)

	for _, n := range network.Nodes {
		_, dup := vms[n.ID]
		if _, dupSwitch := switches[n.ID]; dup || dupSwitch {
			t.Fatalf("network nodes %+v share the ID %d", network.Nodes, n.ID)
		}

		if n.Status == "ignore" {
			if slices.Contains(slices.Collect(maps.Values(switches)), n.Label) || strings.EqualFold(n.Label, "MGMT") ||
				n.Label == "MIRROR" || strings.Contains(n.Label, "(") {
				t.Fatalf("network nodes %+v list switch %q", network.Nodes, n.Label)
			}

			switches[n.ID] = n.Label
		} else {
			vms[n.ID] = n
		}
	}

	for i, e := range network.Edges {
		vm, ok := vms[e.Source]
		if _, toSwitch := switches[e.Target]; !ok || !toSwitch || e.ID != i {
			t.Fatalf("network edge %+v of %+v is not edge %d from a VM to a switch", e, network.Nodes, i)
		}

		linked[e.Target] = true
		links = append(links, vm.Label+" "+switches[e.Target])
	}

	if len(linked) != len(switches) {
		t.Fatalf("network %+v lists a switch no VM is on", network)
	}

	slices.Sort(links)

	return vms, links
}

// A stopped experiment's network links each VM to a switch for each VLAN it
// is on, and filtering by status keeps the VMs in that status and their links.
func TestGetForProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		exp := &types.Experiment{
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: &v1.ExperimentSpec{
				ExperimentNameF: "exp",
				TopologyF:       &v1.TopologySpec{NodesF: genGetForNodes().Draw(t, "nodes")},
			},
			Status: &v1.ExperimentStatus{},
		}

		all, err := GetFor(context.Background(), exp, "")
		if err != nil {
			t.Fatalf("GetFor(exp, \"\") error = %v", err)
		}

		allVMs, allLinks := getForLinks(t, all)

		if len(allVMs) != len(exp.Spec.Topology().Nodes()) {
			t.Fatalf("GetFor(exp, \"\") lists VMs %+v, want all %d", allVMs, len(exp.Spec.Topology().Nodes()))
		}

		for _, status := range []string{"notboot", "notdeploy", "external"} {
			filtered, err := GetFor(context.Background(), exp, status)
			if err != nil {
				t.Fatalf("GetFor(exp, %s) error = %v", status, err)
			}

			vms, links := getForLinks(t, filtered)

			wantVMs := maps.Clone(allVMs)
			maps.DeleteFunc(wantVMs, func(_ int, n Node) bool { return n.Status != status })

			wantLinks := slices.DeleteFunc(slices.Clone(allLinks), func(link string) bool {
				vm, _, _ := strings.Cut(link, " ")

				return !slices.ContainsFunc(slices.Collect(maps.Values(wantVMs)), func(n Node) bool { return n.Label == vm })
			})

			if diff := cmp.Diff(wantVMs, vms, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("GetFor(exp, %s) VMs mismatch (-want +got):\n%s", status, diff)
			}

			if diff := cmp.Diff(wantLinks, links, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("GetFor(exp, %s) links mismatch (-want +got):\n%s", status, diff)
			}
		}
	})
}
