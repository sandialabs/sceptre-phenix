package builder_test

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
	"phenix/types/builder"
	"phenix/util/common"
)

// ignoreDigest leaves out the digest of a source, which a test cannot know.
var ignoreDigest = cmpopts.IgnoreFields(builder.Source{}, "Digest") //nolint:gochecknoglobals // test option

// vlanAlias returns a pointer to alias, as a network holds its VLAN alias.
func vlanAlias(alias int) *int {
	return &alias
}

func TestFromTopologyConfig(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, warnings := documentFromConfig(t, config)

	if len(warnings) != 0 {
		t.Errorf("FromConfig(builder-fixture) warnings = %q, want none", warnings)
	}

	type version struct {
		Schema   string
		Revision int
	}

	gotVersion := version{Schema: doc.Schema, Revision: doc.Revision}
	if diff := cmp.Diff(version{Schema: builder.SchemaURI, Revision: builder.SchemaRevision}, gotVersion); diff != "" {
		t.Errorf("FromConfig(builder-fixture) version mismatch (-want +got):\n%s", diff)
	}

	if got, want := doc.Metadata.ID, builder.DocumentID("builder-fixture"); got != want {
		t.Errorf("FromConfig(builder-fixture) document ID = %q, want %q", got, want)
	}

	wantSource := &builder.Source{
		Kind:       builder.SourceKindTopology,
		Name:       "builder-fixture",
		APIVersion: config.Version,
	}
	if diff := cmp.Diff(wantSource, doc.Source, ignoreDigest); diff != "" {
		t.Errorf("FromConfig(builder-fixture) source mismatch (-want +got):\n%s", diff)
	}

	// Devices keep the order of the topology's nodes.
	wantDevices := []string{"router", "host-a", "sensor", "standalone"}

	gotDevices := make([]string, 0, len(doc.DeviceNodes()))

	for _, node := range doc.DeviceNodes() {
		gotDevices = append(gotDevices, node.Device.Hostname)
	}

	if diff := cmp.Diff(wantDevices, gotDevices); diff != "" {
		t.Errorf("FromConfig(builder-fixture) device order mismatch (-want +got):\n%s", diff)
	}

	wantNetworks := []string{"EXP", "MGMT", "SENSOR"}

	gotNetworks := make([]string, 0, len(doc.Networks))

	for _, network := range doc.Networks {
		gotNetworks = append(gotNetworks, network.Name)
	}

	if diff := cmp.Diff(wantNetworks, gotNetworks); diff != "" {
		t.Errorf("FromConfig(builder-fixture) networks mismatch (-want +got):\n%s", diff)
	}

	// Every non-empty VLAN gets exactly one switch hub, including the
	// single-ended MGMT and SENSOR VLANs.
	if got := len(doc.SwitchNodes()); got != 3 {
		t.Errorf("FromConfig(builder-fixture) = %d switches, want 3", got)
	}

	for _, node := range doc.SwitchNodes() {
		if doc.NetworkByID(node.Switch.NetworkID) == nil {
			t.Errorf("FromConfig(builder-fixture) switch %q network = %q, want a known network", node.ID, node.Switch.NetworkID)
		}
	}

	if got := len(doc.Edges); got != 4 {
		t.Errorf("FromConfig(builder-fixture) = %d edges, want 4: %s", got, asJSON(t, doc.Edges))
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}
}

func TestFromTopologyConfigPreservesIncludedTopologies(t *testing.T) {
	config := loadConfig(t, "topology.json")
	config.Spec["includeTopologies"] = []any{"shared-services", "monitoring"}

	want := []string{"shared-services", "monitoring"}

	doc, warnings := documentFromConfig(t, config)
	if diff := cmp.Diff(want, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig() included topologies mismatch (-want +got):\n%s", diff)
	}

	// Without a loader none of them is resolved.
	if diff := cmp.Diff(want, doc.Source.UnresolvedIncludes); diff != "" {
		t.Errorf("FromConfig() unresolved includes mismatch (-want +got):\n%s", diff)
	}

	if len(warnings) != 1 {
		t.Errorf("FromConfig() warnings = %q, want one visualization warning", warnings)
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if diff := cmp.Diff(want, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
	}
}

func TestFromTopologyConfigPreservesNodeSemantics(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, _ := documentFromConfig(t, config)

	router := nodeByHostname(t, doc, "router")

	if got := builder.DeviceNodeID("router"); router.ID != got {
		t.Errorf("router node ID = %q, want %q", router.ID, got)
	}

	if general, ok := router.Device.Spec["general"].(map[string]any); !ok {
		t.Errorf("router spec = %s, want a general section", asJSON(t, router.Device.Spec))
	} else if general["description"] != "core router" {
		t.Errorf("router description = %v, want %q", general["description"], "core router")
	}

	if annotations, ok := router.Device.Spec["annotations"].(map[string]any); !ok {
		t.Errorf("router spec = %s, want its annotations", asJSON(t, router.Device.Spec))
	} else if extension, ok := annotations["vendor/extension"].(map[string]any); !ok || extension["nested"] != true {
		t.Errorf("router annotations = %s, want the unknown key vendor/extension kept", asJSON(t, annotations))
	}

	if network, ok := router.Device.Spec["network"].(map[string]any); !ok {
		t.Errorf("router spec = %s, want a network section", asJSON(t, router.Device.Spec))
	} else if routes, ok := network["routes"].([]any); !ok || len(routes) != 1 {
		t.Errorf("router network = %s, want its one route", asJSON(t, network))
	}

	wantHandles := []builder.InterfaceHandle{
		{ID: builder.InterfaceHandleID("router", "eth0", 0), Name: "eth0", Index: 0},
		{ID: builder.InterfaceHandleID("router", "eth1", 1), Name: "eth1", Index: 1},
		{ID: builder.InterfaceHandleID("router", "eth2", 2), Name: "eth2", Index: 2},
	}

	if diff := cmp.Diff(wantHandles, router.Device.Interfaces); diff != "" {
		t.Errorf("router interface handles mismatch (-want +got):\n%s", diff)
	}

	if router.Device.IconKey != "linux" {
		t.Errorf("router icon key = %q, want linux", router.Device.IconKey)
	}

	if !builder.IsIconKey(router.Device.IconKey) {
		t.Errorf("IsIconKey(%q) = false, want true", router.Device.IconKey)
	}
}

func TestFromTopologyConfigLeavesUnconnectedInterfaces(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, _ := documentFromConfig(t, config)

	standalone := nodeByHostname(t, doc, "standalone")

	if len(standalone.Device.Interfaces) != 1 {
		t.Errorf("standalone interface handles = %s, want one", asJSON(t, standalone.Device.Interfaces))
	}

	for _, edge := range doc.Edges {
		if edge.SourceNodeID == standalone.ID || edge.TargetNodeID == standalone.ID {
			t.Errorf("FromConfig() edge %s connects standalone, want it unconnected", asJSON(t, edge))
		}
	}

	// The interface itself must still be part of the node spec.
	network, _ := standalone.Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)

	if len(ifaces) != 1 {
		t.Errorf("standalone spec = %s, want its one interface", asJSON(t, standalone.Device.Spec))
	}
}

func TestFromConfigIsDeterministic(t *testing.T) {
	config := loadConfig(t, "topology.json")

	first, _ := documentFromConfig(t, config)
	second, _ := documentFromConfig(t, config)

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("FromConfig() twice mismatch (-first +second):\n%s", diff)
	}

	// Positions are laid out deterministically, devices first.
	devices := first.DeviceNodes()

	if got := devices[0].Position; got != (builder.Position{}) {
		t.Errorf("first device position = %+v, want the origin", got)
	}

	if devices[1].Position.X <= devices[0].Position.X {
		t.Errorf("device positions = %s, want them laid out left to right", asJSON(t, devices))
	}

	switches := first.SwitchNodes()

	if switches[0].Position.Y <= devices[0].Position.Y {
		t.Errorf("switch positions = %s, want them below the devices", asJSON(t, switches))
	}
}

func TestFromExperimentConfig(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	doc, warnings := documentFromConfig(t, config)

	wantSource := &builder.Source{
		Kind:        builder.SourceKindExperiment,
		Name:        "builder-experiment",
		APIVersion:  config.Version,
		Topology:    "builder-fixture",
		Annotations: config.Metadata.Annotations,
		Warnings:    warnings,
	}
	if diff := cmp.Diff(wantSource, doc.Source, ignoreDigest); diff != "" {
		t.Errorf("FromConfig(builder-experiment) source mismatch (-want +got):\n%s", diff)
	}

	wantEXP := &builder.Network{ID: builder.NetworkID("EXP"), Name: "EXP", Alias: vlanAlias(101)}
	if diff := cmp.Diff(wantEXP, doc.NetworkByName("EXP")); diff != "" {
		t.Errorf("FromConfig(builder-experiment) EXP network mismatch (-want +got):\n%s", diff)
	}

	// An alias of 0 means "unassigned" in phenix.
	wantSENSOR := &builder.Network{ID: builder.NetworkID("SENSOR"), Name: "SENSOR"}
	if diff := cmp.Diff(wantSENSOR, doc.NetworkByName("SENSOR")); diff != "" {
		t.Errorf("FromConfig(builder-experiment) SENSOR network mismatch (-want +got):\n%s", diff)
	}

	// A VLAN alias with no attached interface still becomes a canonical
	// network, but gets no switch hub.
	wantRESERVED := &builder.Network{ID: builder.NetworkID("RESERVED"), Name: "RESERVED", Alias: vlanAlias(250)}
	if diff := cmp.Diff(wantRESERVED, doc.NetworkByName("RESERVED")); diff != "" {
		t.Errorf("FromConfig(builder-experiment) RESERVED network mismatch (-want +got):\n%s", diff)
	}

	for _, node := range doc.SwitchNodes() {
		if node.Switch.NetworkID == wantRESERVED.ID {
			t.Errorf("FromConfig(builder-experiment) switch %q is on the empty VLAN RESERVED, want none", node.ID)
		}
	}

	if got := len(doc.SwitchNodes()); got != 2 {
		t.Errorf("FromConfig(builder-experiment) = %d switches, want 2 (EXP and SENSOR)", got)
	}

	// Devices keep the order of the experiment's nodes, the order its VMs launch in.
	gotDevices := make([]string, 0, len(doc.DeviceNodes()))
	for _, node := range doc.DeviceNodes() {
		gotDevices = append(gotDevices, node.Device.Hostname)
	}

	if diff := cmp.Diff([]string{"router", "host-a", "sensor"}, gotDevices); diff != "" {
		t.Errorf("FromConfig(builder-experiment) device order mismatch (-want +got):\n%s", diff)
	}

	// Without a resolver, nothing tells that the scenario is stored.
	if len(doc.Scenarios) != 0 {
		t.Errorf("FromConfig(builder-experiment) scenarios = %q, want none", doc.Scenarios)
	}

	for _, want := range []string{
		`scenario "builder-scenario" is not a stored Scenario config`,
		"the minimum (100) of the experiment VLAN range",
		"does not hold these experiment fields: baseDir, defaultBridge",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromConfig(builder-experiment) warnings = %q, want one containing %q", warnings, want)
		}
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}
}

// An experiment as phenix stores it (see storedExperiment), with its nulls, zero values and
// struct-mapped scenario, generates the same devices as its topology. An interface without a
// VLAN keeps the VLAN "" that phenix stores, because the schema requires a VLAN.
func TestFromStoredExperiment(t *testing.T) {
	doc, _ := documentFromConfig(t, storedExperiment(t))

	topology := loadConfig(t, "topology.json")
	for _, node := range topology.Spec["nodes"].([]any) {
		network, _ := node.(map[string]any)["network"].(map[string]any)
		for _, iface := range network["interfaces"].([]any) {
			if _, ok := iface.(map[string]any)["vlan"]; !ok {
				iface.(map[string]any)["vlan"] = ""
			}
		}
	}

	fromTopology, _ := documentFromConfig(t, topology)

	for _, node := range doc.DeviceNodes() {
		want := nodeByHostname(t, fromTopology, node.Device.Hostname).Device
		if diff := cmp.Diff(want.Spec, node.Device.Spec); diff != "" {
			t.Errorf("device %q spec mismatch, want the topology's node (-want +got):\n%s", node.Device.Hostname, diff)
		}

		if node.Device.IconKey != want.IconKey {
			t.Errorf("device %q icon key = %q, want %q as generated from its topology",
				node.Device.Hostname, node.Device.IconKey, want.IconKey)
		}
	}

	if got := len(doc.DeviceNodes()); got != 4 {
		t.Errorf("FromConfig(stored-experiment) = %d devices, want 4", got)
	}

	if exp := doc.NetworkByName("EXP"); exp == nil {
		t.Errorf("FromConfig(stored-experiment) networks = %s, want EXP", asJSON(t, doc.Networks))
	} else if diff := cmp.Diff(vlanAlias(101), exp.Alias); diff != "" {
		t.Errorf("FromConfig(stored-experiment) EXP alias mismatch (-want +got):\n%s", diff)
	}

	listed, _, err := builder.FromConfig(storedExperiment(t), builder.WithScenarioResolver(
		func(name string) (bool, error) { return name == "builder-scenario", nil },
	))
	if err != nil {
		t.Errorf("FromConfig(stored-experiment, WithScenarioResolver) error = %v, want nil", err)
	} else if diff := cmp.Diff([]string{"builder-scenario"}, listed.Scenarios); diff != "" {
		t.Errorf("FromConfig(stored-experiment, WithScenarioResolver) scenarios mismatch (-want +got):\n%s", diff)
	}

	// The projected topology is one phenix can read back.
	config, _, err := doc.ToTopologyConfig("builder-fixture")
	if err != nil {
		t.Fatalf("ToTopologyConfig(builder-fixture): %v", err)
	}

	if _, err := types.DecodeTopologyFromConfig(*config); err != nil {
		t.Errorf("DecodeTopologyFromConfig() of the projection = %v, want nil", err)
	}

	if err := types.ValidateConfigSpec(*config); err != nil {
		t.Errorf("ValidateConfigSpec() of the projection = %v, want nil", err)
	}

	want := jsonValue(t, topology.Spec["nodes"])
	if diff := cmp.Diff(want, jsonValue(t, config.Spec["nodes"])); diff != "" {
		t.Errorf("ToTopologyConfig(builder-fixture) nodes mismatch, want the topology's (-want +got):\n%s", diff)
	}
}

// Importing a started experiment keeps only the injections of the topology itself, not those its
// apps added under the experiment's base directory.
func TestFromStartedExperimentDropsAppInjections(t *testing.T) {
	for _, tt := range []struct {
		name    string
		baseDir func(spec map[string]any) string
	}{
		{name: "stored base directory", baseDir: func(spec map[string]any) string {
			spec["baseDir"] = "/srv/phenix/experiments/stored-experiment"

			return "/srv/phenix/experiments/stored-experiment"
		}},
		{name: "default base directory", baseDir: func(spec map[string]any) string {
			delete(spec, "baseDir")

			return common.PhenixBase + "/experiments/stored-experiment"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := storedExperiment(t)
			dir := tt.baseDir(config.Spec)

			topology, ok := config.Spec["topology"].(map[string]any)
			if !ok {
				t.Fatalf("stored experiment spec = %s, want a topology", asJSON(t, config.Spec))
			}

			own := []any{
				map[string]any{"src": "configs/router.conf", "dst": "/etc/router.conf"},
				map[string]any{"src": "/phenix/images/banner.txt", "dst": "/etc/motd"},
				map[string]any{"src": dir + "-old/startup/router.sh", "dst": "/etc/old.sh"},
			}

			router := storedNode(t, topology, "router")
			router["injections"] = append(slices.Clone(own),
				map[string]any{"src": dir + "/startup/router-interfaces.sh", "dst": "/etc/phenix/startup/1.sh"},
				map[string]any{"src": dir + "/ntp/router_ntp", "dst": "/etc/ntp.conf"},
			)
			storedNode(t, topology, "host-a")["injections"] = []any{
				map[string]any{"src": dir + "/startup/host-a-timezone.ps1", "dst": "/phenix/startup/tz.ps1"},
			}

			doc, warnings := documentFromConfig(t, config)

			if diff := cmp.Diff(own, nodeByHostname(t, doc, "router").Device.Spec["injections"]); diff != "" {
				t.Errorf("router injections mismatch, want the topology's own (-want +got):\n%s", diff)
			}

			if got, ok := nodeByHostname(t, doc, "host-a").Device.Spec["injections"]; ok {
				t.Errorf("host-a injections = %s, want none", asJSON(t, got))
			}

			if containsSubstring(warnings, "inject") {
				t.Errorf("FromConfig() warnings = %q, want the app injections dropped silently", warnings)
			}

			if got, _ := router["injections"].([]any); len(got) != len(own)+2 {
				t.Errorf("FromConfig() changed the experiment config router injections to %s, want them unchanged",
					asJSON(t, router["injections"]))
			}
		})
	}
}

func TestFromExperimentSkipsBlankZeroVLANAlias(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	vlans, ok := config.Spec["vlans"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no VLAN map")
	}

	aliases, ok := vlans["aliases"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no VLAN aliases")
	}

	aliases[""] = 0

	doc, warnings := documentFromConfig(t, config)
	if network := doc.NetworkByName(""); network != nil {
		t.Errorf("NetworkByName(\"\") = %s, want nil", asJSON(t, network))
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}

	// phenix records the interfaces without a VLAN under "".
	if containsSubstring(warnings, `VLAN alias name ""`) {
		t.Errorf("FromConfig() warnings = %q, want none about the VLAN alias \"\"", warnings)
	}
}

// TestFromExperimentWithoutScenarioName checks that the scenario content of
// an experiment that names no scenario is not attached, with a warning.
func TestFromExperimentWithoutScenarioName(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	delete(config.Metadata.Annotations, "scenario")

	doc, warnings := documentFromConfig(t, config)

	if len(doc.Scenarios) != 0 {
		t.Errorf("FromConfig() scenarios = %q, want none", doc.Scenarios)
	}

	if want := "the experiment's scenario is not a stored Scenario config. " +
		"The Builder did not attach it"; !containsSubstring(warnings, want) {
		t.Errorf("FromConfig() warnings = %q, want one containing %q", warnings, want)
	}
}

func TestFromConfigWarnings(t *testing.T) {
	config := loadConfig(t, "topology.json")

	nodes, ok := config.Spec["nodes"].([]any)
	if !ok {
		t.Fatalf("topology.json has no nodes")
	}

	config.Spec["nodes"] = append(nodes,
		map[string]any{"general": map[string]any{"hostname": ""}},
		map[string]any{"general": map[string]any{"hostname": "ROUTER"}},
		"not-an-object",
		map[string]any{
			"general": map[string]any{"hostname": "unnamed-iface"},
			"network": map[string]any{
				"interfaces": []any{map[string]any{"vlan": "EXP"}},
			},
		},
	)

	doc, warnings := documentFromConfig(t, config)

	for _, want := range []string{
		"has no hostname",
		"duplicates the hostname",
		"is not an object",
		"has no name",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromConfig() warnings = %q, want one containing %q", warnings, want)
		}
	}

	if doc.FindDevice("ROUTER") == nil {
		t.Error("FindDevice(ROUTER) = nil, want the first node with the hostname kept")
	}

	if got := len(doc.DeviceNodes()); got != 5 {
		t.Errorf("FromConfig() = %d devices, want 5", got)
	}
}

func TestFromConfigCaseVariantVLANs(t *testing.T) {
	config := loadConfig(t, "topology.json")

	nodes, _ := config.Spec["nodes"].([]any)
	config.Spec["nodes"] = append(nodes, map[string]any{
		"general": map[string]any{"hostname": "lower-case-vlan"},
		"network": map[string]any{
			"interfaces": []any{map[string]any{"name": "eth0", "vlan": "exp"}},
		},
	})

	doc, warnings := documentFromConfig(t, config)

	if want := `VLAN "exp" differs only by case from VLAN "EXP"`; !containsSubstring(warnings, want) {
		t.Errorf("FromConfig() warnings = %q, want one containing %q", warnings, want)
	}

	// minimega VLAN names are case sensitive: EXP and exp are two isolated
	// segments, so they must stay two networks, each with its own switch.
	if got := len(doc.Networks); got != 4 {
		t.Errorf("FromConfig() = %d networks, want 4 (EXP and exp are different VLANs)", got)
	}

	upper, lower := doc.NetworkByName("EXP"), doc.NetworkByName("exp")
	if upper == nil || lower == nil || upper.ID == lower.ID {
		t.Errorf("FromConfig() networks = %s, want EXP and exp distinct", asJSON(t, doc.Networks))
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}

	// Publishing unchanged keeps each interface on its own VLAN.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if vlan := specInterface(t, specNode(t, topology, "lower-case-vlan"), "eth0")["vlan"]; vlan != "exp" {
		t.Errorf("ToTopology() lower-case-vlan eth0 vlan = %v, want exp", vlan)
	}

	if vlan := specInterface(t, specNode(t, topology, "host-a"), "eth0")["vlan"]; vlan != "EXP" {
		t.Errorf("ToTopology() host-a eth0 vlan = %v, want EXP", vlan)
	}
}

// TestFromConfigLeavesWhitespaceVLANsUnconnected checks that an interface whose
// VLAN holds white space stays unconnected with a warning, in a valid document.
func TestFromConfigLeavesWhitespaceVLANsUnconnected(t *testing.T) {
	for _, vlan := range []string{"my vlan", "my\rvlan", "my\u00a0vlan", "my\ufeffvlan"} {
		t.Run(strconv.Quote(vlan), func(t *testing.T) {
			config := loadConfig(t, "topology.json")

			nodes, _ := config.Spec["nodes"].([]any)
			config.Spec["nodes"] = append(nodes, map[string]any{
				"general": map[string]any{"hostname": "spaced"},
				"network": map[string]any{
					"interfaces": []any{map[string]any{"name": "eth0", "vlan": vlan}},
				},
			})

			doc, warnings := documentFromConfig(t, config)

			want := fmt.Sprintf("VLAN %q of interface \"eth0\" of node \"spaced\" contains whitespace", vlan)
			if !containsSubstring(warnings, want) {
				t.Errorf("FromConfig() warnings = %q, want one containing %q", warnings, want)
			}

			if network := doc.NetworkByName(vlan); network != nil {
				t.Errorf("FromConfig() network %q = %s, want none", vlan, asJSON(t, network))
			}

			device := doc.FindDevice("spaced")
			if device == nil || len(device.Device.Interfaces) != 1 {
				t.Fatalf("FromConfig() spaced = %s, want a device with one interface", asJSON(t, device))
			}

			for _, edge := range doc.Edges {
				if edge.SourceNodeID == device.ID || edge.TargetNodeID == device.ID {
					t.Errorf("FromConfig() edge %s, want the interface of spaced unconnected", asJSON(t, edge))
				}
			}

			if err := doc.Validate(); err != nil {
				t.Errorf("Validate() of the generated document = %v, want nil", err)
			}
		})
	}
}

func TestFromExperimentKeepsCaseVariantVLANAliases(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	vlans, _ := config.Spec["vlans"].(map[string]any)
	aliases, _ := vlans["aliases"].(map[string]any)
	aliases["exp"] = 102

	doc, _ := documentFromConfig(t, config)

	for name, want := range map[string]int{"EXP": 101, "exp": 102} {
		if network := doc.NetworkByName(name); network == nil {
			t.Errorf("FromConfig() networks = %s, want %s", asJSON(t, doc.Networks), name)
		} else if diff := cmp.Diff(vlanAlias(want), network.Alias); diff != "" {
			t.Errorf("FromConfig() %s alias mismatch (-want +got):\n%s", name, diff)
		}
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	for name, want := range map[string]int{"EXP": 101, "exp": 102} {
		if got := topology.VLANAliases[name]; got != want {
			t.Errorf("ToTopology() alias of %s = %d, want %d (aliases %v)", name, got, want, topology.VLANAliases)
		}
	}
}

func TestFromConfigRejectsOlderVersions(t *testing.T) {
	config := loadConfig(t, "topology.json")
	config.Version = "phenix.sandia.gov/v0"

	_, _, err := builder.FromConfig(config)
	if !errors.Is(err, builder.ErrUnsupportedVersion) {
		t.Fatalf("FromConfig(v0 topology) error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestFromConfigChecksKind(t *testing.T) {
	for kind, want := range map[string]error{"topology": nil, "Scenario": builder.ErrUnsupportedKind} {
		t.Run(kind, func(t *testing.T) {
			config := loadConfig(t, "topology.json")
			config.Kind = kind

			if _, _, err := builder.FromConfig(config); !errors.Is(err, want) {
				t.Errorf("FromConfig(kind %q) error = %v, want %v", kind, err, want)
			}
		})
	}
}

func TestFromConfigEmptyTopology(t *testing.T) {
	config := store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "empty"},
		Spec:     map[string]any{},
	}

	doc, warnings, err := builder.FromConfig(config)
	if err != nil {
		t.Fatalf("FromConfig(empty): %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("FromConfig(empty) warnings = %q, want none", warnings)
	}

	type contents struct {
		Nodes    []builder.Node
		Networks []builder.Network
		Edges    []builder.Edge
	}

	got := contents{Nodes: doc.Nodes, Networks: doc.Networks, Edges: doc.Edges}
	if diff := cmp.Diff(contents{}, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("FromConfig(empty) mismatch (-want +got):\n%s", diff)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the empty document = %v, want nil", err)
	}
}

// includeFixture is a Topology config for include tests: nodes named by
// hostname, each with one eth0 interface on the given VLAN.
func includeFixture(name string, includes []string, nodes map[string]string) store.Config {
	specNodes := make([]any, 0, len(nodes))

	for _, hostname := range sortedKeys(nodes) {
		specNodes = append(specNodes, map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": hostname},
			"network": map[string]any{
				"interfaces": []any{map[string]any{"name": "eth0", "vlan": nodes[hostname]}},
			},
		})
	}

	spec := map[string]any{"nodes": specNodes}

	if len(includes) > 0 {
		spec["includeTopologies"] = toAnySlice(includes)
	}

	return store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     spec,
	}
}

// toAnySlice converts values to the []any a stored spec holds.
func toAnySlice(values []string) []any {
	converted := make([]any, len(values))
	for i, value := range values {
		converted[i] = value
	}

	return converted
}

// storeLoader resolves included topologies from configs, like the generate
// handler resolves them from the config store.
func storeLoader(configs ...store.Config) builder.TopologyLoader {
	byName := map[string]store.Config{}
	for _, config := range configs {
		byName[config.Metadata.Name] = config
	}

	return func(name string) (*store.Config, error) {
		config, ok := byName[name]
		if !ok {
			return nil, store.ErrNotExist
		}

		return &config, nil
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// connectedSwitch returns the name of the network a device's interface handle
// is connected to, or "" when it is not connected.
func connectedSwitch(doc *builder.Document, node *builder.Node, iface string) string {
	for _, handle := range node.Device.Interfaces {
		if handle.Name != iface {
			continue
		}

		for _, edge := range doc.Edges {
			if edge.SourceHandleID == handle.ID || edge.TargetHandleID == handle.ID {
				return doc.NetworkByID(edge.NetworkID).Name
			}
		}
	}

	return ""
}

func TestFromTopologyConfigAddsIncludedTopologies(t *testing.T) {
	root := loadConfig(t, "topology.json")
	root.Spec["includeTopologies"] = []any{"shared-services"}

	shared := includeFixture("shared-services", []string{"monitoring"},
		map[string]string{"dns": "EXP", "ntp": "SERVICES"})
	monitoring := includeFixture("monitoring", nil, map[string]string{"collector": "MGMT"})

	doc, warnings, err := builder.FromConfig(root, builder.WithTopologyLoader(storeLoader(shared, monitoring)))
	if err != nil {
		t.Fatalf("FromConfig(builder-fixture): %v", err)
	}

	const summary = "The Builder added 3 nodes from included topologies shared-services (2 nodes) and monitoring (1 node)."
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], summary) {
		t.Errorf("FromConfig(builder-fixture) warnings = %q, want one starting %q", warnings, summary)
	}

	// Only the direct include is the topology's own reference.
	if diff := cmp.Diff([]string{"shared-services"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(builder-fixture) included topologies mismatch (-want +got):\n%s", diff)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(builder-fixture) unresolved includes = %v, want none: every include was read",
			doc.Source.UnresolvedIncludes)
	}

	for hostname, want := range map[string]string{
		"router": "", "host-a": "", "dns": "shared-services", "ntp": "shared-services", "collector": "monitoring",
	} {
		if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
			t.Errorf("%s IncludedFrom = %q, want %q", hostname, got, want)
		}
	}

	// Included devices join the topology's own switches, and bring their own.
	for hostname, want := range map[string]string{"dns": "EXP", "ntp": "SERVICES", "collector": "MGMT"} {
		if got := connectedSwitch(doc, nodeByHostname(t, doc, hostname), "eth0"); got != want {
			t.Errorf("%s eth0 network = %q, want %q", hostname, got, want)
		}
	}

	if got := len(doc.SwitchNodes()); got != 4 {
		t.Errorf("FromConfig(builder-fixture) = %d switches, want 4 (EXP, MGMT, SENSOR and SERVICES)", got)
	}

	// The topology's own devices come first, then those of each included
	// topology in the order phenix merges them, each in a row of its own.
	devices := doc.DeviceNodes()
	gotOrder := make([]string, 0, len(devices))

	for _, node := range devices {
		gotOrder = append(gotOrder, node.Device.Hostname)
	}

	wantOrder := []string{"router", "host-a", "sensor", "standalone", "dns", "ntp", "collector"}
	if diff := cmp.Diff(wantOrder, gotOrder); diff != "" {
		t.Fatalf("FromConfig(builder-fixture) device order mismatch (-want +got):\n%s", diff)
	}

	if devices[4].Position.Y <= devices[3].Position.Y || devices[4].Position.X != 0 ||
		devices[5].Position.Y != devices[4].Position.Y ||
		devices[6].Position.Y <= devices[5].Position.Y || devices[6].Position.X != 0 {
		t.Errorf("FromConfig(builder-fixture) devices = %s, want each included topology in a row of its own",
			asJSON(t, devices))
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}

	// Publishing writes the reference, never the included devices, so phenix
	// does not find their hostnames twice when it merges the includes back.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 4 {
		t.Errorf("ToTopology() = %d nodes, want the topology's own 4: %s", len(nodes), asJSON(t, topology.Spec))
	}

	if diff := cmp.Diff([]string{"shared-services"}, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
	}
}

func TestFromTopologyConfigReportsIncludeProblems(t *testing.T) {
	root := loadConfig(t, "topology.json")
	root.Spec["includeTopologies"] = []any{"clash", "missing", "loop", "self"}

	clash := includeFixture("clash", nil, map[string]string{"ROUTER": "EXP", "shared-name": "EXP"})
	loop := includeFixture("loop", []string{"builder-fixture"}, map[string]string{"shared-name": "EXP", "looped": "EXP"})
	self := includeFixture("self", []string{"self"}, map[string]string{"self-node": "EXP"})

	doc, warnings, err := builder.FromConfig(root, builder.WithTopologyLoader(storeLoader(clash, loop, self)))
	if err != nil {
		t.Fatalf("FromConfig(builder-fixture): %v", err)
	}

	for _, want := range []string{
		// phenix compares hostnames exactly. The Builder folds case, so a
		// case variant is reported too rather than silently merged.
		`included topology "clash" defines node "ROUTER", which duplicates a hostname in the topology itself`,
		`included topology "loop" defines node "shared-name", which duplicates a hostname in included topology "clash"`,
		`the Builder could not read included topology "missing": ` + store.ErrNotExist.Error() + ". The diagram does not show its nodes",
		`included topology "builder-fixture" includes itself through a cycle`,
		`included topology "self" includes itself through a cycle`,
		"The Builder added 3 nodes from included topologies clash (1 node), loop (1 node) and self (1 node).",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromConfig(builder-fixture) warnings = %q, want one containing %q", warnings, want)
		}
	}

	// A cycle is not an include whose nodes are missing: only the topology
	// that could not be read is.
	if diff := cmp.Diff([]string{"missing"}, doc.Source.UnresolvedIncludes); diff != "" {
		t.Errorf("FromConfig(builder-fixture) unresolved includes mismatch (-want +got):\n%s", diff)
	}

	if router := nodeByHostname(t, doc, "router"); router.Device.IncludedFrom != "" {
		t.Errorf("router = %s, want the topology's own router, not an included one", asJSON(t, router))
	}

	for _, hostname := range []string{"shared-name", "looped", "self-node"} {
		if nodeByHostname(t, doc, hostname).Device.IncludedFrom == "" {
			t.Errorf("%s IncludedFrom = \"\", want the include it came from", hostname)
		}
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}
}

func TestFromExperimentConfigMarksIncludedNodes(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	// phenix merged the included topology's sensor into the experiment's
	// topology when it created the experiment, and kept the reference.
	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no topology")
	}

	topology["includeTopologies"] = []any{"sensors"}

	// The topology the experiment was created from, as it is now.
	root := includeFixture("builder-fixture", []string{"sensors"}, map[string]string{"router": "EXP", "host-a": "EXP"})

	// Since the experiment was created, the included topology gained a node,
	// and one named like a node of the root topology.
	sensors := includeFixture("sensors", nil,
		map[string]string{"sensor": "SENSOR", "added-later": "SENSOR", "router": "EXP"})

	doc, warnings := documentFromConfigWith(t, config, builder.WithTopologyLoader(storeLoader(root, sensors)))

	for _, want := range []string{
		"The Builder marked 1 experiment node as included from included topology sensors (1 node).",
		`included topology "sensors" defines node "router", which duplicates a hostname in the topology itself. ` +
			"phenix rejects duplicate hostnames",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromConfig(builder-experiment) warnings = %q, want one containing %q", warnings, want)
		}
	}

	if got := nodeByHostname(t, doc, "sensor").Device.IncludedFrom; got != "sensors" {
		t.Errorf("sensor IncludedFrom = %q, want sensors", got)
	}

	// The root topology's own router is never taken for the included one, and the clash is reported.
	if got := nodeByHostname(t, doc, "router").Device.IncludedFrom; got != "" {
		t.Errorf("router IncludedFrom = %q, want none: it is the root topology's", got)
	}

	// The experiment is shown as it is: a node the included topology gained
	// since is not added, and nothing is duplicated.
	if doc.FindDevice("added-later") != nil || len(doc.DeviceNodes()) != 3 {
		t.Errorf("FromConfig(builder-experiment) devices = %s, want the experiment's 3 and not added-later",
			asJSON(t, doc.DeviceNodes()))
	}

	projection, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	nodes, _ := projection.Spec["nodes"].([]any)
	for _, entry := range nodes {
		if node, _ := entry.(map[string]any); specString(node, "general", "hostname") == "sensor" {
			t.Errorf("ToTopology() = %s, want the included sensor left out", asJSON(t, projection.Spec))
		}
	}

	if len(nodes) != 2 {
		t.Errorf("ToTopology() = %d nodes, want router and host-a: %s", len(nodes), asJSON(t, projection.Spec))
	}

	if diff := cmp.Diff([]string{"sensors"}, projection.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
	}

	// A node that neither the root nor a readable include defines is marked as from the unreadable
	// include, so publishing does not copy it in beside the include phenix merges again.
	t.Run("unreadable include", func(t *testing.T) {
		errFromFile := errors.New("the Builder does not read files")

		for _, tc := range []struct {
			name     string
			includes []string
			// merged are experiment nodes phenix merged in besides the sensor.
			merged   []string
			loader   builder.TopologyLoader
			want     map[string]string
			warnings []string
		}{
			{
				name:     "missing",
				includes: []string{"sensors"},
				loader: storeLoader(
					includeFixture("builder-fixture", []string{"sensors"}, map[string]string{"router": "EXP", "host-a": "EXP"}),
				),
				want: map[string]string{"router": "", "host-a": "", "sensor": "sensors"},
				warnings: []string{
					`the Builder could not read included topology "sensors": ` + store.ErrNotExist.Error() +
						`. The Builder marks the experiment's 1 node that neither topology "builder-fixture" nor a readable ` +
						"included topology defines as included from it. The diagram shows each such node read only, " +
						"and publishing does not copy it into the topology",
				},
			},
			{
				name:     "file path",
				includes: []string{"sensors", "../extra.yml"},
				merged:   []string{"from-file"},
				loader: func(name string) (*store.Config, error) {
					if strings.Contains(name, "/") {
						return nil, errFromFile
					}

					return storeLoader(
						includeFixture("builder-fixture", []string{"sensors", "../extra.yml"},
							map[string]string{"router": "EXP", "host-a": "EXP"}),
						includeFixture("sensors", nil, map[string]string{"sensor": "SENSOR"}),
					)(name)
				},
				want: map[string]string{"router": "", "host-a": "", "sensor": "sensors", "from-file": "../extra.yml"},
				warnings: []string{
					`the Builder could not read included topology "../extra.yml": ` + errFromFile.Error() +
						`. The Builder marks the experiment's 1 node that neither topology "builder-fixture" nor a readable ` +
						"included topology defines as included from it",
					"The Builder marked 1 experiment node as included from included topology sensors (1 node).",
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				config := loadConfig(t, "experiment.json")

				topology, _ := config.Spec["topology"].(map[string]any)
				topology["includeTopologies"] = toAnySlice(tc.includes)

				for _, hostname := range tc.merged {
					topology["nodes"] = append(topology["nodes"].([]any), map[string]any{
						"type":    "VirtualMachine",
						"general": map[string]any{"hostname": hostname},
					})
				}

				doc, warnings := documentFromConfigWith(t, config, builder.WithTopologyLoader(tc.loader))

				for _, want := range tc.warnings {
					if !containsSubstring(warnings, want) {
						t.Errorf("FromConfig() warnings = %q, want one containing %q", warnings, want)
					}
				}

				for hostname, want := range tc.want {
					if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
						t.Errorf("%s IncludedFrom = %q, want %q", hostname, got, want)
					}
				}

				projection, err := doc.ToTopology()
				if err != nil {
					t.Fatalf("ToTopology(): %v", err)
				}

				if nodes, _ := projection.Spec["nodes"].([]any); len(nodes) != 2 {
					t.Errorf("ToTopology() = %d nodes, want router and host-a: %s", len(nodes), asJSON(t, projection.Spec))
				}

				if diff := cmp.Diff(tc.includes, projection.Spec["includeTopologies"]); diff != "" {
					t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})
}

// The experiment's own nodes come first and the included ones after them, each in the order of
// the experiment's nodes, even when the experiment lists an included node first.
func TestFromExperimentConfigGroupsIncludedNodes(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no topology")
	}

	nodes, _ := topology["nodes"].([]any)
	topology["nodes"] = []any{nodes[2], nodes[0], nodes[1]}
	topology["includeTopologies"] = []any{"sensors"}

	root := includeFixture("builder-fixture", []string{"sensors"}, map[string]string{"router": "EXP", "host-a": "EXP"})
	sensors := includeFixture("sensors", nil, map[string]string{"sensor": "SENSOR"})

	doc, _ := documentFromConfigWith(t, config, builder.WithTopologyLoader(storeLoader(root, sensors)))

	got := make([]string, 0, len(doc.DeviceNodes()))
	for _, node := range doc.DeviceNodes() {
		got = append(got, node.Device.Hostname)
	}

	if diff := cmp.Diff([]string{"router", "host-a", "sensor"}, got); diff != "" {
		t.Errorf("FromConfig(builder-experiment) device order mismatch (-want +got):\n%s", diff)
	}
}

// TestFromExperimentConfigWithoutRootTopology matches nodes by hostname alone when the experiment's
// topology cannot be read, and warns that nodes of unreadable includes publish as its own.
func TestFromExperimentConfigWithoutRootTopology(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no topology")
	}

	topology["includeTopologies"] = []any{"sensors", "missing"}

	sensors := includeFixture("sensors", nil, map[string]string{"sensor": "SENSOR"})

	doc, warnings := documentFromConfigWith(t, config, builder.WithTopologyLoader(storeLoader(sensors)))

	for _, want := range []string{
		`the Builder could not read topology "builder-fixture", which the experiment was created from: ` +
			store.ErrNotExist.Error() + ". Thus the Builder matches the experiment nodes to included topologies only by hostname",
		`the Builder could not read included topology "missing": ` + store.ErrNotExist.Error() +
			". Without the topology that the experiment was created from, the Builder cannot distinguish " +
			"its nodes in the experiment from the topology's own nodes. Thus the diagram shows them " +
			"as the topology's own nodes, and publishing the topology copies them into it",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromConfig(builder-experiment) warnings = %q, want one containing %q", warnings, want)
		}
	}

	for hostname, want := range map[string]string{"sensor": "sensors", "router": "", "host-a": ""} {
		if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
			t.Errorf("%s IncludedFrom = %q, want %q", hostname, got, want)
		}
	}
}

// TestFromTopologyConfigIncludedTwice resolves a topology that two includes both include once, with
// one warning naming both paths, not as a clash with itself.
func TestFromTopologyConfigIncludedTwice(t *testing.T) {
	root := loadConfig(t, "topology.json")
	root.Spec["includeTopologies"] = []any{"left", "right"}

	left := includeFixture("left", []string{"shared"}, map[string]string{"left-node": "EXP"})
	right := includeFixture("right", []string{"shared", "shared"}, map[string]string{"right-node": "EXP"})
	shared := includeFixture("shared", nil, map[string]string{"shared-node": "EXP"})

	doc, warnings, err := builder.FromConfig(root, builder.WithTopologyLoader(storeLoader(left, right, shared)))
	if err != nil {
		t.Fatalf("FromConfig(builder-fixture): %v", err)
	}

	want := []string{
		`included topology "shared" occurs more than once (through left and right twice). ` +
			"phenix rejects the duplicate hostnames that this causes. Thus the diagram shows its nodes only once",
		"The Builder added 3 nodes from included topologies left (1 node), shared (1 node) and right (1 node).",
	}

	// Each warning goes on past the text the test knows, so only its start is compared.
	if len(warnings) != len(want) {
		t.Errorf("FromConfig(builder-fixture) warnings = %q, want %d starting %q", warnings, len(want), want)
	} else {
		for i := range want {
			if !strings.HasPrefix(warnings[i], want[i]) {
				t.Errorf("FromConfig(builder-fixture) warning = %q, want one starting %q", warnings[i], want[i])
			}
		}
	}

	if got := nodeByHostname(t, doc, "shared-node").Device.IncludedFrom; got != "shared" {
		t.Errorf("shared-node IncludedFrom = %q, want shared", got)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(builder-fixture) unresolved includes = %v, want none", doc.Source.UnresolvedIncludes)
	}
}

// TestCheckIncludes reports what would stop phenix from merging a
// topology's includes as they are now.
func TestCheckIncludes(t *testing.T) {
	spec := map[string]any{
		"nodes": []any{
			map[string]any{"general": map[string]any{"hostname": "web"}},
			map[string]any{"general": map[string]any{"hostname": "db"}},
		},
		// As a projection writes them.
		"includeTopologies": []string{"services", "missing"},
	}

	services := includeFixture("services", []string{"storage"}, map[string]string{"dns": "EXP", "DB": "EXP", "x": "EXP"})
	storage := includeFixture("storage", []string{"root"}, map[string]string{"web": "EXP", "nas": "EXP", "007": "EXP"})

	report, err := builder.CheckIncludes("root", spec, storeLoader(services, storage))
	if err != nil {
		t.Fatalf("CheckIncludes(root): %v", err)
	}

	wantClashes := []builder.HostnameClash{
		{Include: "services", Hostname: "DB"},
		{Include: "storage", Hostname: "web"},
	}
	if diff := cmp.Diff(wantClashes, report.Clashes); diff != "" {
		t.Errorf("CheckIncludes(root) clashes mismatch (-want +got):\n%s", diff)
	}

	if len(report.Unreadable) != 1 || report.Unreadable[0].Name != "missing" ||
		!errors.Is(report.Unreadable[0].Err, store.ErrNotExist) {
		t.Errorf("CheckIncludes(root) unreadable = %+v, want the missing include with %v",
			report.Unreadable, store.ErrNotExist)
	}

	// phenix's schema refuses a single character, which an older phenix
	// stored, and phenix refuses an all-digit hostname in an experiment.
	// Each reason goes on with phenix's own words, so only its start is compared.
	wantRefused := []builder.RefusedHostname{
		{Include: "services", Reason: "hostname 'x' is 1 character long: "},
		{Include: "storage", Reason: "hostname '007' is all digits: "},
	}
	if len(report.Refused) != len(wantRefused) {
		t.Errorf("CheckIncludes(root) refused = %+v, want services' x and storage's 007", report.Refused)
	} else {
		for i, want := range wantRefused {
			if got := report.Refused[i]; got.Include != want.Include || !strings.HasPrefix(got.Reason, want.Reason) {
				t.Errorf("CheckIncludes(root) refused = %+v, want one starting %+v", got, want)
			}
		}
	}

	clean, err := builder.CheckIncludes("root", map[string]any{"nodes": []any{}}, storeLoader())
	if err != nil {
		t.Fatalf("CheckIncludes(root without includes): %v", err)
	}

	if diff := cmp.Diff(builder.IncludeReport{}, clean, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("CheckIncludes(root without includes) mismatch (-want +got):\n%s", diff)
	}
}

func TestFromConfigWithoutLoaderKeepsIncludesUnresolved(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("experiment.json has no topology")
	}

	topology["includeTopologies"] = []any{"sensors"}

	doc, warnings := documentFromConfig(t, config)

	if want := "includes 1 other topologies that the Builder did not resolve"; !containsSubstring(warnings, want) {
		t.Errorf("FromConfig(builder-experiment) warnings = %q, want one containing %q", warnings, want)
	}

	for _, node := range doc.DeviceNodes() {
		if node.Device.IncludedFrom != "" {
			t.Errorf("%s IncludedFrom = %q without a loader, want none", node.Device.Hostname, node.Device.IncludedFrom)
		}
	}

	// An experiment holds every node, so it never lists an include as
	// unresolved.
	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(builder-experiment) unresolved includes = %v, want none for an experiment",
			doc.Source.UnresolvedIncludes)
	}
}

// documentFromConfigWith imports a fixture config with options, failing the
// test on error.
func documentFromConfigWith(
	t *testing.T, config store.Config, options ...builder.GenerateOption,
) (*builder.Document, []string) {
	t.Helper()

	doc, warnings, err := builder.FromConfig(config, options...)
	if err != nil {
		t.Fatalf("FromConfig(%s/%s): %v", config.Kind, config.Metadata.Name, err)
	}

	return doc, warnings
}

// specString reads a string at a path of nested maps.
func specString(spec map[string]any, path ...string) string {
	var value any = spec

	for _, key := range path {
		object, _ := value.(map[string]any)
		value = object[key]
	}

	text, _ := value.(string)

	return text
}

// The two warnings a combined import adds, as the Import dialog shows them.
const (
	combinedSummary = "The Builder copied 3 nodes from included topologies corp-services (2 nodes) and lab-dns (1 node). " +
		"They are ordinary nodes of this diagram now: changes here do not reach those topologies, " +
		"and later changes there do not reach this diagram."
	combinedKept = "The Builder did not combine included topologies site-b and /srv/extra.yml. They stay in " +
		"includeTopologies, and publishing keeps the references."
)

// combineFixture is a topology "site" with one node that includes corp-services (which includes
// lab-dns), the unreadable site-b, and a file the loader does not read.
func combineFixture() (store.Config, builder.TopologyLoader) {
	root := includeFixture("site", []string{"corp-services", "site-b", "/srv/extra.yml"},
		map[string]string{"gateway": "EXP"})
	services := includeFixture("corp-services", []string{"lab-dns"},
		map[string]string{"mail": "EXP", "web": "SERVICES"})
	dns := includeFixture("lab-dns", nil, map[string]string{"dns": "MGMT"})

	return root, storeLoader(services, dns)
}

// TestFromTopologyConfigCombinesIncludes copies the nodes of readable includes into the document as
// its own, and keeps only the other includes.
func TestFromTopologyConfigCombinesIncludes(t *testing.T) {
	root, load := combineFixture()

	doc, warnings, err := builder.FromConfig(root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())
	if err != nil {
		t.Fatalf("FromConfig(site): %v", err)
	}

	hostnames := make([]string, 0, len(doc.DeviceNodes()))

	for _, node := range doc.DeviceNodes() {
		if node.Device.IncludedFrom != "" {
			t.Errorf("%s IncludedFrom = %q, want none", node.Device.Hostname, node.Device.IncludedFrom)
		}

		hostnames = append(hostnames, node.Device.Hostname)
	}

	// The nodes keep the order of a plain import: the topology's own, then
	// each included topology's.
	if diff := cmp.Diff([]string{"gateway", "mail", "web", "dns"}, hostnames); diff != "" {
		t.Errorf("FromConfig(site) devices mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"site-b", "/srv/extra.yml"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(site) included topologies mismatch (-want +got):\n%s", diff)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(site) unresolved includes = %v, want none: they are the document's includes now",
			doc.Source.UnresolvedIncludes)
	}

	// The problems are reported as a plain import reports them. The summary
	// says the nodes were copied, and one more warning names what was not.
	want := []string{
		`the Builder could not read included topology "site-b": ` + store.ErrNotExist.Error() + ". The diagram does not show its nodes",
		`the Builder could not read included topology "/srv/extra.yml": ` + store.ErrNotExist.Error() + ". The diagram does not show its nodes",
		combinedSummary,
		combinedKept,
	}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("FromConfig(site) warnings mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, doc.Source.Warnings); diff != "" {
		t.Errorf("FromConfig(site) source warnings mismatch (-want +got):\n%s", diff)
	}

	if got := connectedSwitch(doc, nodeByHostname(t, doc, "dns"), "eth0"); got != "MGMT" {
		t.Errorf("dns eth0 network = %q, want MGMT", got)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the combined document = %v, want nil", err)
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 4 {
		t.Errorf("ToTopology() = %d nodes, want all 4: %s", len(nodes), asJSON(t, topology.Spec))
	}

	if diff := cmp.Diff([]string{"site-b", "/srv/extra.yml"}, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
	}
}

// TestFromTopologyConfigCombinesEveryInclude checks that a document whose includes were all read
// includes nothing, and words one topology in the singular.
func TestFromTopologyConfigCombinesEveryInclude(t *testing.T) {
	root := includeFixture("site", []string{"lab-dns"}, map[string]string{"gateway": "EXP"})
	dns := includeFixture("lab-dns", nil, map[string]string{"dns": "MGMT"})

	doc, warnings, err := builder.FromConfig(root,
		builder.WithTopologyLoader(storeLoader(dns)), builder.WithCombinedIncludes())
	if err != nil {
		t.Fatalf("FromConfig(site): %v", err)
	}

	want := []string{
		"The Builder copied 1 node from included topology lab-dns (1 node). They are ordinary nodes of this diagram now: " +
			"changes here do not reach that topology, and later changes there do not reach this diagram.",
	}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("FromConfig(site) warnings mismatch (-want +got):\n%s", diff)
	}

	if doc.Source.IncludeTopologies != nil {
		t.Errorf("FromConfig(site) included topologies = %v, want none", doc.Source.IncludeTopologies)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(site) unresolved includes = %v, want none", doc.Source.UnresolvedIncludes)
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if included, ok := topology.Spec["includeTopologies"]; ok {
		t.Errorf("ToTopology() includeTopologies = %v, want none", included)
	}

	// One include that cannot be read is named in the singular.
	root = includeFixture("site", []string{"site-b"}, map[string]string{"gateway": "EXP"})

	doc, warnings = documentFromConfigWith(t, root,
		builder.WithTopologyLoader(storeLoader()), builder.WithCombinedIncludes())

	kept := "The Builder did not combine included topology site-b. It stays in includeTopologies, and publishing keeps the reference."
	if len(warnings) != 2 || warnings[1] != kept {
		t.Errorf("FromConfig(site including site-b) warnings = %q, want the unreadable include and %q", warnings, kept)
	}

	if diff := cmp.Diff([]string{"site-b"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(site including site-b) included topologies mismatch (-want +got):\n%s", diff)
	}
}

// TestFromTopologyConfigCombineWithoutLoader asserts that combining copies
// nothing it cannot read: without a loader every include is kept.
func TestFromTopologyConfigCombineWithoutLoader(t *testing.T) {
	root, _ := combineFixture()

	doc, warnings := documentFromConfigWith(t, root, builder.WithCombinedIncludes())

	if got := len(doc.DeviceNodes()); got != 1 {
		t.Errorf("FromConfig(site) = %d devices, want only the topology's own", got)
	}

	// Each include once, in the order the topology lists them.
	if diff := cmp.Diff([]string{"corp-services", "site-b", "/srv/extra.yml"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(site) included topologies mismatch (-want +got):\n%s", diff)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromConfig(site) unresolved includes = %v, want none", doc.Source.UnresolvedIncludes)
	}

	if want := "The Builder did not combine included topologies " +
		"corp-services, site-b and /srv/extra.yml"; !containsSubstring(warnings, want) {
		t.Errorf("FromConfig(site) warnings = %q, want one containing %q", warnings, want)
	}
}

// TestFromTopologyConfigRecordsUnresolvedIncludes checks that a plain import lists the includes it
// does not hold, at any depth, once each in the order met, and keeps the topology's own includes.
func TestFromTopologyConfigRecordsUnresolvedIncludes(t *testing.T) {
	root := includeFixture("site", []string{"corp-services", "site-b", "nested-gone", "site"},
		map[string]string{"gateway": "EXP"})
	services := includeFixture("corp-services", []string{"lab-dns", "nested-gone", "site-b"},
		map[string]string{"mail": "EXP"})
	dns := includeFixture("lab-dns", []string{"deep-gone", "corp-services"}, map[string]string{"dns": "MGMT"})

	doc, warnings := documentFromConfigWith(t, root, builder.WithTopologyLoader(storeLoader(services, dns)))

	if diff := cmp.Diff([]string{"deep-gone", "nested-gone", "site-b"}, doc.Source.UnresolvedIncludes); diff != "" {
		t.Errorf("FromConfig(site) unresolved includes mismatch (-want +got):\n%s", diff)
	}

	want := []string{"corp-services", "site-b", "nested-gone", "site"}
	if diff := cmp.Diff(want, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(site) included topologies mismatch (-want +got):\n%s", diff)
	}

	// A plain import says nothing new: the marks and the summary are as
	// they were.
	if !containsSubstring(warnings, "The Builder added 2 nodes from included topologies corp-services (1 node) and lab-dns (1 node).") ||
		containsSubstring(warnings, "combined") || containsSubstring(warnings, "Copied") {
		t.Errorf("FromConfig(site) warnings = %q, want the summary of a plain import", warnings)
	}

	if got := nodeByHostname(t, doc, "dns").Device.IncludedFrom; got != "lab-dns" {
		t.Errorf("dns IncludedFrom = %q, want lab-dns", got)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the generated document = %v, want nil", err)
	}
}

// TestFromTopologyConfigListsIncludesPastTheLimit checks that includes left unread at the
// resolution limit are listed too, and that a combined document keeps them all.
func TestFromTopologyConfigListsIncludesPastTheLimit(t *testing.T) {
	const (
		limit = 100
		extra = 20
	)

	var (
		names   = make([]string, 0, limit+extra)
		configs = make([]store.Config, 0, limit+extra)
	)

	for i := range limit + extra {
		name := "part-" + strconv.Itoa(1000+i)
		names = append(names, name)
		configs = append(configs, includeFixture(name, nil, map[string]string{"node-" + strconv.Itoa(1000+i): "EXP"}))
	}

	root := includeFixture("site", names, map[string]string{"gateway": "EXP"})
	load := storeLoader(configs...)

	kept, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(load))
	if diff := cmp.Diff(names[limit:], kept.Source.UnresolvedIncludes); diff != "" {
		t.Errorf("FromConfig(site) unresolved includes mismatch, want the %d past the limit (-want +got):\n%s", extra, diff)
	}

	combined, warnings := documentFromConfigWith(t, root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())
	if diff := cmp.Diff(names[limit:], combined.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromConfig(site, combined) included topologies mismatch, want the %d past the limit (-want +got):\n%s",
			extra, diff)
	}

	if got := len(combined.DeviceNodes()); got != limit+1 {
		t.Errorf("FromConfig(site, combined) = %d devices, want the topology's own and the %d that were read", got, limit)
	}

	// A long list is cut: eight names, then how many more.
	want := "The Builder did not combine included topologies " + strings.Join(names[limit:limit+7], ", ") + ", " +
		names[limit+7] + " and 12 more. They stay in includeTopologies, and publishing keeps the references."
	if !slices.Contains(warnings, want) {
		t.Errorf("FromConfig(site, combined) warnings = %q, want one of %q", warnings, want)
	}
}

// TestFromExperimentConfigRefusesCombine asserts an experiment, which already
// holds the nodes phenix merged into it, is not combined.
func TestFromExperimentConfigRefusesCombine(t *testing.T) {
	_, _, err := builder.FromConfig(loadConfig(t, "experiment.json"), builder.WithCombinedIncludes())
	if !errors.Is(err, builder.ErrCombineExperiment) {
		t.Errorf("FromConfig(builder-experiment, WithCombinedIncludes) error = %v, want %v", err, builder.ErrCombineExperiment)
	}
}

func TestIncludeCount(t *testing.T) {
	topology := includeFixture("site", nil, map[string]string{"gateway": "EXP"})

	if got := builder.IncludeCount(topology); got != 0 {
		t.Errorf("IncludeCount(topology without includes) = %d, want 0", got)
	}

	// Only the entries an import would use count. Each entry counts, also
	// one that repeats another.
	topology.Spec["includeTopologies"] = []any{"corp-services", "", "has space", 7, "lab-dns", "corp-services"}

	if got := builder.IncludeCount(topology); got != 3 {
		t.Errorf("IncludeCount(topology) = %d, want 3", got)
	}

	lower := topology
	lower.Kind = "topology"

	if got := builder.IncludeCount(lower); got != 3 {
		t.Errorf("IncludeCount(kind in lower case) = %d, want 3", got)
	}

	// An import refuses an older apiVersion, so it counts no includes.
	older := includeFixture("older", []string{"corp-services", "lab-dns"}, nil)
	older.Version = "phenix.sandia.gov/v0"

	if got := builder.IncludeCount(older); got != 0 {
		t.Errorf("IncludeCount(v0 topology) = %d, want 0", got)
	}

	experiment := loadConfig(t, "experiment.json")
	if spec, ok := experiment.Spec["topology"].(map[string]any); ok {
		spec["includeTopologies"] = []any{"corp-services"}
	}

	scenario := topology
	scenario.Kind = "Scenario"

	for _, config := range []store.Config{experiment, scenario} {
		t.Run(config.Kind, func(t *testing.T) {
			if got := builder.IncludeCount(config); got != 0 {
				t.Errorf("IncludeCount(%s) = %d, want 0", config.Kind, got)
			}
		})
	}
}

// TestFromConfigReadsVLANAliasesOfEveryNumberType checks that the aliases of
// an experiment are read whatever number type its decoder gave them.
func TestFromConfigReadsVLANAliasesOfEveryNumberType(t *testing.T) {
	doc, _ := documentFromConfig(t, store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "numbers"},
		Spec: map[string]any{
			"topology": map[string]any{"nodes": []any{}},
			"vlans": map[string]any{"aliases": map[string]any{
				"A": int32(101), "B": int64(102), "C": float32(103), "D": 104.0, "E": 105,
			}},
		},
	})

	got := map[string]int{}

	for _, network := range doc.Networks {
		if network.Alias != nil {
			got[network.Name] = *network.Alias
		}
	}

	want := map[string]int{"A": 101, "B": 102, "C": 103, "D": 104, "E": 105}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FromConfig(numbers) VLAN aliases mismatch (-want +got):\n%s", diff)
	}
}
