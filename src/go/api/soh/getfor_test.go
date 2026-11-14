package soh

import (
	"context"
	"errors"
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
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
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

// The network reports a soh app that the scenario disables, for a stopped
// experiment too.
func TestGetForReportsDisabledApp(t *testing.T) {
	t.Parallel()

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			TopologyF:       &v1.TopologySpec{},
			ScenarioF:       &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh", DisabledF: true}}},
		},
		Status: &v1.ExperimentStatus{},
	}

	network, err := GetFor(t.Context(), exp, "")
	if err != nil {
		t.Fatalf("GetFor(exp with soh disabled, \"\") error = %v", err)
	}

	if !network.SOHDisabled || network.SOHInitialized {
		t.Errorf("GetFor(exp with soh disabled, \"\") soh_disabled, soh_initialized = %t, %t; want true, false",
			network.SOHDisabled, network.SOHInitialized)
	}
}

// getForMM is a minimega that runs vms.
type getForMM struct {
	mm.MM

	vms mm.VMs
}

func (m getForMM) GetVMInfo(...mm.Option) mm.VMs { return m.vms }

// useGetForMM makes minimega run vms until the test ends.
func useGetForMM(tb testing.TB, vms ...mm.VM) {
	tb.Helper()

	originalMM := mm.DefaultMM

	mm.DefaultMM = getForMM{vms: vms} //nolint:reassign // install test double

	tb.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore default
}

// getForRunning is a started experiment with a router, two VMs and an external
// node, whose soh app status is soh (none when nil).
func getForRunning(soh any) *types.Experiment {
	router := sohNode("r", "linux", sohIface("eth0", "LAN", "static", "10.0.0.1", ""))
	router.TypeF = "Router"
	external := true

	status := &v1.ExperimentStatus{
		StartTimeF: "2026-11-06T00:00:00Z",
		RunningF:   map[string]bool{"soh": true},
	}
	if soh != nil {
		status.AppsF = map[string]any{"soh": soh}
	}

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{
				router,
				sohNode("web", "linux", sohIface("eth0", "LAN", "static", "10.0.0.2", "")),
				sohNode("db", "linux"),
				{TypeF: "VirtualMachine", GeneralF: &v1.General{HostnameF: "ext"}, HardwareF: &v1.Hardware{}, ExternalF: &external},
			}},
			ScenarioF: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh"}}},
		},
		Status: status,
	}
}

// For a started experiment, the network shows the state of each VM minimega
// runs and the soh results of each host, and marks the hosts with errors.
func TestGetForRunningExperiment(t *testing.T) { //nolint:paralleltest // replaces package globals
	useGetForMM(t,
		mm.VM{Name: "r", State: "RUNNING", Running: true, Networks: []string{"LAN (101)"}},
		mm.VM{Name: "web", State: "RUNNING", Running: true, Networks: []string{"LAN (101)"}},
		mm.VM{Name: "db", State: "PAUSED"},
	)

	exp := getForRunning(map[string]any{
		"initialized": true,
		"hosts": []any{
			map[string]any{"hostname": "web", "networking": []any{map[string]any{"error": "no address"}}},
			map[string]any{"hostname": "r", "networking": []any{map[string]any{"success": "10.0.0.1"}}},
		},
	})

	network, err := GetFor(t.Context(), exp, "")
	if err != nil {
		t.Fatalf("GetFor(running exp, \"\") error = %v", err)
	}

	want := &Network{
		ExpStarted: true, SOHInitialized: true, SOHRunning: true,
		Nodes: []Node{
			{ID: 0, Label: "r", Image: "Router", Status: "running", SOH: &HostState{
				Hostname: "r", Networking: []State{{Success: "10.0.0.1"}},
			}},
			{ID: 5, Label: "LAN", Image: "switch", Status: "ignore"},
			{ID: 1, Label: "web", Image: "linux", Status: "running", SOH: &HostState{
				Hostname: "web", Networking: []State{{Error: "no address"}}, Errors: true,
			}},
			{ID: 2, Label: "db", Image: "linux", Status: "notrunning"},
			{ID: 3, Label: "ext", Status: "external"},
		},
		Edges: []Edge{
			{ID: 0, Source: 0, Target: 5, Length: defaultEdgeLength},
			{ID: 1, Source: 1, Target: 5, Length: defaultEdgeLength},
		},
		Hosts: []string{}, HostFlows: [][]int{},
	}
	if diff := cmp.Diff(want, network, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("GetFor(running exp, \"\") mismatch (-want +got):\n%s", diff)
	}
}

// GetFor fails for soh results it cannot decode, and once its context is done.
func TestGetForErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	useGetForMM(t)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	for name, tc := range map[string]struct {
		ctx     context.Context
		soh     any
		wantErr error
	}{
		"status not a map": {ctx: t.Context(), soh: "done"},
		"hosts not a list": {ctx: t.Context(), soh: map[string]any{"hosts": 5}},
		"host not a map":   {ctx: t.Context(), soh: map[string]any{"hosts": []any{"web"}}},
		"context done":     {ctx: canceled, wantErr: context.Canceled},
	} {
		t.Run(name, func(t *testing.T) {
			network, err := GetFor(tc.ctx, getForRunning(tc.soh), "")
			if network != nil || err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("GetFor(%s) = %v, %v; want nil and an error that wraps %v", name, network, err, tc.wantErr)
			}

			if strings.Contains(err.Error(), "%!") {
				t.Errorf("GetFor(%s) error %q has a formatting verb with no value", name, err)
			}
		})
	}
}

// GetFlowsFor returns the packet capture hosts and flows that the soh app
// recorded, nothing when it recorded none, and an error for a status it cannot
// decode.
func TestGetFlowsFor(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		status    *v1.ExperimentStatus
		wantHosts []string
		wantFlows [][]int
		wantErr   bool
	}{
		"no status":        {},
		"no soh status":    {status: &v1.ExperimentStatus{AppsF: map[string]any{"scorch": map[string]any{}}}},
		"no capture":       {status: &v1.ExperimentStatus{AppsF: map[string]any{"soh": map[string]any{"hosts": []any{}}}}},
		"status not a map": {status: &v1.ExperimentStatus{AppsF: map[string]any{"soh": "done"}}, wantErr: true},
		"capture not a map": {
			status:  &v1.ExperimentStatus{AppsF: map[string]any{"soh": map[string]any{"packetCapture": "on"}}},
			wantErr: true,
		},
		"flows": {
			status: &v1.ExperimentStatus{AppsF: map[string]any{"soh": map[string]any{"packetCapture": map[string]any{
				"hosts": []any{"a", "b"}, "flows": []any{[]any{0, 3}, []any{4, 0}},
			}}}},
			wantHosts: []string{"a", "b"},
			wantFlows: [][]int{{0, 3}, {4, 0}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exp := &types.Experiment{Metadata: store.ConfigMetadata{Name: "exp"}}
			if tc.status != nil {
				exp.Status = tc.status
			}

			hosts, flows, err := GetFlowsFor(exp)
			if (err != nil) != tc.wantErr {
				t.Fatalf("GetFlowsFor(%s) error = %v, want error %t", name, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.wantHosts, hosts); diff != "" {
				t.Errorf("GetFlowsFor(%s) hosts mismatch (-want +got):\n%s", name, diff)
			}

			if diff := cmp.Diff(tc.wantFlows, flows); diff != "" {
				t.Errorf("GetFlowsFor(%s) flows mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}
