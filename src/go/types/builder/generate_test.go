package builder_test

import (
	"errors"
	"reflect"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

func TestFromTopologyConfig(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, warnings := documentFromConfig(t, config)

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	if doc.Schema != builder.SchemaURI || doc.Revision != builder.SchemaRevision {
		t.Fatalf("document is not versioned: %s/%d", doc.Schema, doc.Revision)
	}

	if doc.ID != builder.DocumentID("builder-fixture") {
		t.Fatalf("document ID = %q, want deterministic ID", doc.ID)
	}

	if doc.Source == nil || doc.Source.Kind != builder.SourceKindTopology ||
		doc.Source.Name != "builder-fixture" || doc.Source.APIVersion != config.Version {
		t.Fatalf("unexpected source metadata: %s", asJSON(t, doc.Source))
	}

	wantDevices := []string{"host-a", "router", "sensor", "standalone"}

	gotDevices := make([]string, 0, len(doc.DeviceNodes()))

	for _, node := range doc.DeviceNodes() {
		gotDevices = append(gotDevices, node.Device.Hostname)
	}

	if !reflect.DeepEqual(gotDevices, wantDevices) {
		t.Fatalf("device order = %v, want %v", gotDevices, wantDevices)
	}

	wantNetworks := []string{"EXP", "MGMT", "SENSOR"}

	gotNetworks := make([]string, 0, len(doc.Networks))

	for _, network := range doc.Networks {
		gotNetworks = append(gotNetworks, network.Name)
	}

	if !reflect.DeepEqual(gotNetworks, wantNetworks) {
		t.Fatalf("networks = %v, want %v", gotNetworks, wantNetworks)
	}

	// Every non-empty VLAN gets exactly one switch hub, including the
	// single-ended MGMT and SENSOR VLANs.
	if got := len(doc.SwitchNodes()); got != 3 {
		t.Fatalf("switch count = %d, want 3", got)
	}

	for _, node := range doc.SwitchNodes() {
		if doc.NetworkByID(node.Switch.NetworkID) == nil {
			t.Fatalf("switch %q references an unknown network", node.ID)
		}
	}

	if got := len(doc.Edges); got != 4 {
		t.Fatalf("edge count = %d, want 4: %s", got, asJSON(t, doc.Edges))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}
}

func TestFromTopologyConfigPreservesIncludedTopologies(t *testing.T) {
	config := loadConfig(t, "topology.json")
	config.Spec["includeTopologies"] = []any{"shared-services", "monitoring"}

	doc, warnings := documentFromConfig(t, config)
	if !reflect.DeepEqual(doc.Source.IncludeTopologies, []string{"shared-services", "monitoring"}) {
		t.Fatalf("included topologies = %v", doc.Source.IncludeTopologies)
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one visualization warning", warnings)
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if !reflect.DeepEqual(
		topology.Spec["includeTopologies"],
		[]string{"shared-services", "monitoring"},
	) {
		t.Fatalf("published includes = %v", topology.Spec["includeTopologies"])
	}
}

func TestFromTopologyConfigPreservesNodeSemantics(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, _ := documentFromConfig(t, config)

	router := nodeByHostname(t, doc, "router")

	if got := builder.DeviceNodeID("router"); router.ID != got {
		t.Fatalf("router node ID = %q, want %q", router.ID, got)
	}

	general, ok := router.Device.Spec["general"].(map[string]any)
	if !ok {
		t.Fatalf("router spec has no general section: %s", asJSON(t, router.Device.Spec))
	}

	if general["description"] != "core router" {
		t.Fatalf("description = %v, want %q", general["description"], "core router")
	}

	annotations, ok := router.Device.Spec["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("annotations were dropped: %s", asJSON(t, router.Device.Spec))
	}

	extension, ok := annotations["vendor/extension"].(map[string]any)
	if !ok || extension["nested"] != true {
		t.Fatalf("unknown annotation keys were not preserved: %s", asJSON(t, annotations))
	}

	network, ok := router.Device.Spec["network"].(map[string]any)
	if !ok {
		t.Fatalf("network section missing: %s", asJSON(t, router.Device.Spec))
	}

	if routes, ok := network["routes"].([]any); !ok || len(routes) != 1 {
		t.Fatalf("routes were dropped: %s", asJSON(t, network))
	}

	wantHandles := []builder.InterfaceHandle{
		{ID: builder.InterfaceHandleID("router", "eth0", 0), Name: "eth0", Index: 0},
		{ID: builder.InterfaceHandleID("router", "eth1", 1), Name: "eth1", Index: 1},
		{ID: builder.InterfaceHandleID("router", "eth2", 2), Name: "eth2", Index: 2},
	}

	if !reflect.DeepEqual(router.Device.Interfaces, wantHandles) {
		t.Fatalf("interface handles = %s, want %s",
			asJSON(t, router.Device.Interfaces), asJSON(t, wantHandles))
	}

	if router.Device.IconKey != "linux" {
		t.Fatalf("icon key = %q, want linux", router.Device.IconKey)
	}

	if !builder.IsIconKey(router.Device.IconKey) {
		t.Fatalf("icon key %q is not in the registry", router.Device.IconKey)
	}
}

func TestFromTopologyConfigLeavesUnconnectedInterfaces(t *testing.T) {
	config := loadConfig(t, "topology.json")
	doc, _ := documentFromConfig(t, config)

	standalone := nodeByHostname(t, doc, "standalone")

	if len(standalone.Device.Interfaces) != 1 {
		t.Fatalf("expected one handle, got %s", asJSON(t, standalone.Device.Interfaces))
	}

	for _, edge := range doc.Edges {
		if edge.SourceNodeID == standalone.ID || edge.TargetNodeID == standalone.ID {
			t.Fatalf("unconnected node was connected: %s", asJSON(t, edge))
		}
	}

	// The interface itself must still be part of the node spec.
	network, _ := standalone.Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)

	if len(ifaces) != 1 {
		t.Fatalf("unconnected interface was dropped: %s", asJSON(t, standalone.Device.Spec))
	}
}

func TestFromConfigIsDeterministic(t *testing.T) {
	config := loadConfig(t, "topology.json")

	first, _ := documentFromConfig(t, config)
	second, _ := documentFromConfig(t, config)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("generation is not deterministic:\nfirst: %s\nsecond: %s",
			asJSON(t, first), asJSON(t, second))
	}

	// Positions are laid out deterministically, devices first.
	devices := first.DeviceNodes()

	if devices[0].Position.X != 0 || devices[0].Position.Y != 0 {
		t.Fatalf("first device position = %v, want origin", devices[0].Position)
	}

	if devices[1].Position.X <= devices[0].Position.X {
		t.Fatalf("devices are not laid out left to right: %s", asJSON(t, devices))
	}

	switches := first.SwitchNodes()

	if switches[0].Position.Y <= devices[0].Position.Y {
		t.Fatalf("switches must be laid out below devices: %s", asJSON(t, switches))
	}
}

func TestFromExperimentConfig(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	doc, warnings := documentFromConfig(t, config)

	if doc.Source == nil || doc.Source.Kind != builder.SourceKindExperiment {
		t.Fatalf("unexpected source: %s", asJSON(t, doc.Source))
	}

	if doc.Source.Topology != "builder-fixture" {
		t.Fatalf("source topology = %q, want builder-fixture", doc.Source.Topology)
	}

	if !reflect.DeepEqual(doc.Source.Warnings, warnings) {
		t.Fatalf("warnings are not recorded on the document: %v vs %v", doc.Source.Warnings, warnings)
	}

	exp := doc.NetworkByName("EXP")
	if exp == nil || exp.Alias == nil || *exp.Alias != 101 {
		t.Fatalf("EXP alias was not imported: %s", asJSON(t, doc.Networks))
	}

	// An alias of 0 means "unassigned" in phenix.
	sensor := doc.NetworkByName("SENSOR")
	if sensor == nil || sensor.Alias != nil {
		t.Fatalf("SENSOR alias should be unset: %s", asJSON(t, sensor))
	}

	// A VLAN alias with no attached interface still becomes a canonical
	// network, but gets no switch hub.
	reserved := doc.NetworkByName("RESERVED")
	if reserved == nil || reserved.Alias == nil || *reserved.Alias != 250 {
		t.Fatalf("RESERVED alias was not imported: %s", asJSON(t, doc.Networks))
	}

	for _, node := range doc.SwitchNodes() {
		if node.Switch.NetworkID == reserved.ID {
			t.Fatal("an empty VLAN must not get a switch hub")
		}
	}

	if got := len(doc.SwitchNodes()); got != 2 {
		t.Fatalf("switch count = %d, want 2 (EXP and SENSOR)", got)
	}

	if doc.Scenario == nil {
		t.Fatal("scenario was not imported")
	}

	if doc.Scenario.Kind != builder.ScenarioRefStored || doc.Scenario.Name != "builder-scenario" {
		t.Fatalf("unexpected scenario reference: %s", asJSON(t, doc.Scenario))
	}

	digest, err := builder.ContentDigest(doc.Scenario.Content)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	if doc.Scenario.Digest != digest {
		t.Fatalf("scenario digest = %q, want %q", doc.Scenario.Digest, digest)
	}

	if !containsSubstring(warnings, "VLAN range minimum") {
		t.Fatalf("expected a warning about the VLAN range, got %v", warnings)
	}

	if !containsSubstring(warnings, "not represented in the builder document: baseDir, defaultBridge") {
		t.Fatalf("expected a warning about unrepresented experiment fields, got %v", warnings)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}
}

func TestFromExperimentSkipsBlankZeroVLANAlias(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	vlans, ok := config.Spec["vlans"].(map[string]any)
	if !ok {
		t.Fatal("fixture has no VLAN map")
	}

	aliases, ok := vlans["aliases"].(map[string]any)
	if !ok {
		t.Fatal("fixture has no VLAN aliases")
	}

	aliases[""] = 0

	doc, warnings := documentFromConfig(t, config)
	if doc.NetworkByName("") != nil {
		t.Fatal("blank VLAN alias created a network")
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}

	if !containsSubstring(warnings, `VLAN alias name "" is invalid`) {
		t.Fatalf("warnings = %v, want blank VLAN warning", warnings)
	}
}

func TestFromExperimentWithoutScenarioName(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	delete(config.Metadata.Annotations, "scenario")

	doc, _ := documentFromConfig(t, config)

	if doc.Scenario == nil || doc.Scenario.Kind != builder.ScenarioRefUploaded {
		t.Fatalf("expected an uploaded scenario reference: %s", asJSON(t, doc.Scenario))
	}

	if doc.Scenario.Digest == "" {
		t.Fatal("uploaded scenario reference must carry a content digest")
	}
}

func TestFromConfigWarnings(t *testing.T) {
	config := loadConfig(t, "topology.json")

	nodes, ok := config.Spec["nodes"].([]any)
	if !ok {
		t.Fatalf("fixture has no nodes")
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
			t.Fatalf("expected a warning containing %q, got %v", want, warnings)
		}
	}

	if doc.FindDevice("ROUTER") == nil {
		t.Fatal("the first node with a hostname should be kept")
	}

	if got := len(doc.DeviceNodes()); got != 5 {
		t.Fatalf("device count = %d, want 5", got)
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

	if !containsSubstring(warnings, "differs only by case") {
		t.Fatalf("expected a case-collision warning, got %v", warnings)
	}

	if got := len(doc.Networks); got != 3 {
		t.Fatalf("network count = %d, want 3 (VLANs must not collide)", got)
	}
}

func TestFromConfigUpgradesOlderVersions(t *testing.T) {
	config := store.Config{
		Version:  "phenix.sandia.gov/v0",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "legacy"},
		Spec: map[string]any{
			"nodes": []any{
				map[string]any{
					"type":     "VirtualMachine",
					"general":  map[string]any{"hostname": "legacy-node"},
					"hardware": map[string]any{"os_type": "linux", "vcpus": "2"},
					"network": map[string]any{
						"interfaces": []any{
							map[string]any{
								"name":    "eth0",
								"type":    "ethernet",
								"proto":   "static",
								"address": "10.0.0.1",
								"mask":    "24",
								"vlan":    "EXP",
							},
						},
					},
				},
			},
		},
	}

	doc, _, err := builder.FromConfig(config)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	node := doc.FindDevice("legacy-node")
	if node == nil {
		t.Fatalf("upgraded document has no node: %s", asJSON(t, doc))
	}

	if doc.NetworkByName("EXP") == nil {
		t.Fatalf("upgraded document has no EXP network: %s", asJSON(t, doc.Networks))
	}
}

func TestFromConfigRejectsUnsupportedKind(t *testing.T) {
	config := loadConfig(t, "topology.json")
	config.Kind = "Scenario"

	_, _, err := builder.FromConfig(config)
	if !errors.Is(err, builder.ErrUnsupportedKind) {
		t.Fatalf("error %v does not wrap ErrUnsupportedKind", err)
	}
}

func TestFromConfigAcceptsKindCaseInsensitively(t *testing.T) {
	config := loadConfig(t, "topology.json")
	config.Kind = "topology"

	if _, _, err := builder.FromConfig(config); err != nil {
		t.Fatalf("FromConfig: %v", err)
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
		t.Fatalf("FromConfig: %v", err)
	}

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	if len(doc.Nodes) != 0 || len(doc.Networks) != 0 || len(doc.Edges) != 0 {
		t.Fatalf("expected an empty document, got %s", asJSON(t, doc))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("empty document is invalid: %v", err)
	}
}
