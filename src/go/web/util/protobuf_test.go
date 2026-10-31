package util

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"pgregory.net/rapid"

	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
)

const (
	testStartTime   = "2026-07-31T12:00:00Z"
	testInterrupted = "VMs were waiting to start when phenix stopped tracking them"
)

// testExperiment has a VM of each kind the VM counts treat differently. It is
// running when given a start time.
func testExperiment(t *testing.T, startTime string) types.Experiment {
	t.Helper()

	yes := true

	exp := types.NewExperiment(store.ConfigMetadata{
		Name:        "exp",
		Annotations: map[string]string{"topology": "topo", "scenario": "scn"},
	})
	exp.Spec.SetExperimentName("exp")
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: []*v1.Node{
		{GeneralF: &v1.General{HostnameF: "plain"}},
		{GeneralF: &v1.General{HostnameF: "dnb", DoNotBootF: &yes}},
		{GeneralF: &v1.General{HostnameF: "external"}, ExternalF: &yes},
		{GeneralF: &v1.General{HostnameF: "timer"}, DelayF: &v1.Delay{TimerF: "5m"}},
		{GeneralF: &v1.General{HostnameF: "user", DoNotBootF: &yes}, DelayF: &v1.Delay{UserF: true}},
		// a VM takes its delay and external flag from the first node with its
		// name, and do-not-boot from its own node
		{GeneralF: &v1.General{HostnameF: "plain"}, ExternalF: &yes, DelayF: &v1.Delay{TimerF: "9m"}},
		{GeneralF: &v1.General{HostnameF: "dnb"}},
	}})
	exp.Spec.SetScenario(&v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
		{NameF: "ntp"}, {NameF: "soh", FromScenarioF: "base"}, {NameF: "x", FromScenarioF: "scn"},
	}})

	if err := exp.Spec.SetVLANRange(100, 200, true); err != nil {
		t.Fatal(err)
	}

	if err := exp.Spec.SetVLANAlias("EXP", 101, true); err != nil {
		t.Fatal(err)
	}

	exp.Status.SetVLANs(map[string]int{"EXP": 105, "MGMT": 107})
	exp.Status.SetStartTime(startTime)
	// left from a start a restarted phenix could not resume; only a running one reports it
	exp.Status.SetInterrupted(testInterrupted)

	return *exp
}

type vmFields struct {
	Name, Experiment, Delay string
	External                bool
}

func vmFieldsOf(vms []*proto.VM) []vmFields {
	if vms == nil {
		return nil
	}

	out := make([]vmFields, len(vms))
	for i, v := range vms {
		out[i] = vmFields{v.GetName(), v.GetExperiment(), v.GetDelayedStart(), v.GetExternal()}
	}

	return out
}

// ExperimentToProtobuf counts the VMs it is given, and ExperimentSummaryToProtobuf
// the VMs the topology configures; both report the rest of the experiment alike.
func TestExperimentToProtobuf(t *testing.T) {
	type converted struct {
		Running                  bool
		VMCount, DelayedVMs      uint32
		VLANMin, VLANMax         uint32
		VLANs                    map[string]uint32
		VMs                      []vmFields
		Name, Topology, Scenario string
		StartTime, Status        string
		Apps, Scenarios          []string
		Interrupted              string
	}

	withVMs := func(exp types.Experiment) *proto.Experiment {
		return ExperimentToProtobuf(exp, cache.StatusStarted, vm.ListConfigured(exp))
	}

	// minimega runs every VM but timer, so vm.ListFor leaves it out
	withRunningVMs := func(exp types.Experiment) *proto.Experiment {
		vms := slices.DeleteFunc(vm.ListConfigured(exp), func(v mm.VM) bool { return v.Name == "timer" })

		return ExperimentToProtobuf(exp, cache.StatusStarted, vms)
	}

	summary := func(exp types.Experiment) *proto.Experiment {
		return ExperimentSummaryToProtobuf(exp, cache.StatusStarted)
	}

	configuredVMs := []vmFields{
		{"plain", "exp", "", false},
		{"dnb", "exp", "", false},
		{"external", "exp", "", true},
		{"timer", "exp", "timer:5m", false},
		{"user", "exp", "user", false},
		{"plain", "exp", "", false},
		{"dnb", "exp", "", false},
	}

	tests := []struct {
		name      string
		startTime string
		convert   func(types.Experiment) *proto.Experiment
		vmCount   uint32
		delayed   uint32
		vms       []vmFields
	}{
		{"stopped counts every VM", "", withVMs, 7, 2, configuredVMs},
		{"stopped summary counts every node", "", summary, 7, 2, nil},
		{"running counts the VMs that boot and are not external", testStartTime, withVMs, 4, 2, configuredVMs},
		{"running summary counts the nodes that boot and are not external", testStartTime, summary, 4, 2, nil},
		{
			"running counts only the VMs it is given",
			testStartTime, withRunningVMs, 3, 1,
			slices.DeleteFunc(slices.Clone(configuredVMs), func(v vmFields) bool { return v.Name == "timer" }),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			running := tc.startTime != ""

			want := converted{
				Running:    running,
				VMCount:    tc.vmCount,
				DelayedVMs: tc.delayed,
				// a stopped experiment reports its configured VLANs, a running
				// one those it was given
				VLANMin: 100,
				VLANMax: 200,
				VLANs:   map[string]uint32{"EXP": 101},
				VMs:     tc.vms,
				Name:    "exp", Topology: "topo", Scenario: "scn",
				StartTime: tc.startTime,
				Status:    string(cache.StatusStarted),
				Apps:      []string{"ntp", "soh", "x"},
				Scenarios: []string{"scn", "base"},
			}

			if running {
				want.VLANMin, want.VLANMax = 105, 107
				want.VLANs = map[string]uint32{"EXP": 105, "MGMT": 107}
				want.Interrupted = testInterrupted
			}

			pb := tc.convert(testExperiment(t, tc.startTime))

			vlans := make(map[string]uint32, len(pb.GetVlans()))
			for _, v := range pb.GetVlans() {
				vlans[v.GetAlias()] = v.GetVlan()
			}

			if n := int(pb.GetVlanCount()); n != len(vlans) {
				t.Errorf("converting experiment started at %q: vlan_count = %d, want %d", tc.startTime, n, len(vlans))
			}

			got := converted{
				Running:    pb.GetRunning(),
				VMCount:    pb.GetVmCount(),
				DelayedVMs: pb.GetDelayedVms(),
				VLANMin:    pb.GetVlanMin(),
				VLANMax:    pb.GetVlanMax(),
				VLANs:      vlans,
				VMs:        vmFieldsOf(pb.GetVms()),
				Name:       pb.GetName(),
				Topology:   pb.GetTopology(),
				Scenario:   pb.GetScenario(),
				StartTime:  pb.GetStartTime(),
				Status:     pb.GetStatus(),
				Apps:       pb.GetApps(),
				Scenarios:  pb.GetScenarios(),

				Interrupted: pb.GetInterrupted(),
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("converting experiment started at %q mismatch (-want +got):\n%s", tc.startTime, diff)
			}
		})
	}
}

func TestExperimentScenarios(t *testing.T) {
	apps := func(from ...string) []ifaces.ScenarioApp {
		out := make([]ifaces.ScenarioApp, len(from))
		for i, f := range from {
			out[i] = &v2.ScenarioApp{NameF: "app", FromScenarioF: f}
		}

		return out
	}

	tests := []struct {
		name     string
		scenario string
		apps     []ifaces.ScenarioApp
		want     []string
	}{
		{"no scenario", "", nil, nil},
		{"one scenario", "scn", apps("", ""), []string{"scn"}},
		{"its own scenario named again", "scn", apps("scn"), []string{"scn"}},
		{"others once each, in app order", "scn", apps("b", "", "a", "b"), []string{"scn", "b", "a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, experimentScenarios(tc.scenario, tc.apps)); diff != "" {
				t.Errorf("experimentScenarios(%q, ...) mismatch (-want +got):\n%s", tc.scenario, diff)
			}
		})
	}
}

// The scenarios start with the experiment's own, name each scenario the apps
// take configuration from, and name none twice.
func TestExperimentScenariosListsEachOnce(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom([]string{"", "a", "b", "c"})
		scenario := name.Draw(t, "scenario")
		from := rapid.SliceOf(name).Draw(t, "from")

		apps := make([]ifaces.ScenarioApp, len(from))
		for i, f := range from {
			apps[i] = &v2.ScenarioApp{NameF: "app", FromScenarioF: f}
		}

		got := experimentScenarios(scenario, apps)

		if scenario != "" && (len(got) == 0 || got[0] != scenario) {
			t.Fatalf("experimentScenarios(%q, %v) = %v, want %q first", scenario, from, got, scenario)
		}

		want := map[string]bool{}
		if scenario != "" {
			want[scenario] = true
		}

		for _, f := range from {
			if f != "" {
				want[f] = true
			}
		}

		seen := map[string]bool{}
		for _, g := range got {
			if seen[g] || !want[g] {
				t.Fatalf("experimentScenarios(%q, %v) = %v: %q is repeated or unexpected", scenario, from, got, g)
			}

			seen[g] = true
		}

		if len(seen) != len(want) {
			t.Fatalf("experimentScenarios(%q, %v) = %v, want each of %v", scenario, from, got, want)
		}
	})
}

func TestVMToProtobuf(t *testing.T) {
	exp := testExperiment(t, "")
	topo := exp.Spec.Topology()

	tests := []struct {
		name     string
		vm       string
		topo     ifaces.TopologySpec
		delay    string
		external bool
	}{
		{"takes its node's delay", "timer", topo, "timer:5m", false},
		{"takes its node's external flag", "external", topo, "", true},
		{"takes the first node with its name", "plain", topo, "", false},
		{"has no node fields without a node", "missing", topo, "", false},
		{"has no node fields without a topology", "timer", nil, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := &proto.VM{
				Name:         tc.vm,
				Host:         "host",
				DoNotBoot:    true,
				Experiment:   "exp",
				DelayedStart: tc.delay,
				External:     tc.external,
				OsType:       "windows",
			}

			got := VMToProtobuf("exp", mm.VM{Name: tc.vm, Host: "host", DoNotBoot: true, OSType: "windows"}, tc.topo)

			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("VMToProtobuf(exp, %q) mismatch (-want +got):\n%s", tc.vm, diff)
			}
		})
	}
}
