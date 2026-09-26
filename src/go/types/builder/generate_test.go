package builder_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
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

// An experiment as phenix stores it (see storedExperiment) carries
// "external": null on every VM, null pointers and zero values throughout,
// and a struct-mapped scenario; none of it may change what is generated.
func TestFromStoredExperiment(t *testing.T) {
	doc, _ := documentFromConfig(t, storedExperiment(t))
	fromTopology, _ := documentFromConfig(t, loadConfig(t, "topology.json"))

	for _, node := range doc.DeviceNodes() {
		if external, stored := node.Device.Spec["external"]; !stored || external != nil {
			t.Fatalf("device %q spec should carry external: null as phenix stores it", node.Device.Hostname)
		}

		want := nodeByHostname(t, fromTopology, node.Device.Hostname).Device.IconKey
		if node.Device.IconKey != want {
			t.Fatalf("device %q icon key = %q, want %q as generated from its topology",
				node.Device.Hostname, node.Device.IconKey, want)
		}
	}

	if got := len(doc.DeviceNodes()); got != 4 {
		t.Fatalf("device count = %d, want 4", got)
	}

	exp := doc.NetworkByName("EXP")
	if exp == nil || exp.Alias == nil || *exp.Alias != 101 {
		t.Fatalf("EXP alias was not imported: %s", asJSON(t, doc.Networks))
	}

	ref := doc.Scenario
	if ref == nil || ref.Kind != builder.ScenarioRefStored || ref.Name != "builder-scenario" ||
		ref.APIVersion != builder.ScenarioAPIVersion() {
		t.Fatalf("unexpected scenario reference: %s", asJSON(t, ref))
	}

	if digest, err := builder.ContentDigest(ref.Content); err != nil || ref.Digest != digest {
		t.Fatalf("scenario digest = %q, want the digest of its content (%q, %v)", ref.Digest, digest, err)
	}

	// The projected topology is one phenix can read back.
	config, _, err := doc.ToTopologyConfig("builder-fixture")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	if _, err := types.DecodeTopologyFromConfig(*config); err != nil {
		t.Fatalf("DecodeTopologyFromConfig of the projection: %v", err)
	}

	if err := types.ValidateConfigSpec(*config); err != nil {
		t.Fatalf("the projection fails the phenix topology schema: %v", err)
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

	if !containsSubstring(warnings, `VLAN "exp" differs only by case from VLAN "EXP"`) {
		t.Fatalf("expected a case-variant warning, got %v", warnings)
	}

	// minimega VLAN names are case sensitive: EXP and exp are two isolated
	// segments, so they must stay two networks, each with its own switch.
	if got := len(doc.Networks); got != 4 {
		t.Fatalf("network count = %d, want 4 (EXP and exp are different VLANs)", got)
	}

	upper, lower := doc.NetworkByName("EXP"), doc.NetworkByName("exp")
	if upper == nil || lower == nil || upper.ID == lower.ID {
		t.Fatalf("EXP and exp must be distinct networks: %s", asJSON(t, doc.Networks))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}

	// Publishing unchanged keeps each interface on its own VLAN.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if vlan := specInterface(t, specNode(t, topology, "lower-case-vlan"), "eth0")["vlan"]; vlan != "exp" {
		t.Fatalf("lower-case-vlan eth0 vlan = %v, want exp", vlan)
	}

	if vlan := specInterface(t, specNode(t, topology, "host-a"), "eth0")["vlan"]; vlan != "EXP" {
		t.Fatalf("host-a eth0 vlan = %v, want EXP", vlan)
	}
}

func TestFromExperimentKeepsCaseVariantVLANAliases(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	vlans, _ := config.Spec["vlans"].(map[string]any)
	aliases, _ := vlans["aliases"].(map[string]any)
	aliases["exp"] = 102

	doc, _ := documentFromConfig(t, config)

	upper, lower := doc.NetworkByName("EXP"), doc.NetworkByName("exp")
	if upper == nil || upper.Alias == nil || *upper.Alias != 101 ||
		lower == nil || lower.Alias == nil || *lower.Alias != 102 {
		t.Fatalf("both case variants must keep their alias: %s", asJSON(t, doc.Networks))
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if topology.VLANAliases["EXP"] != 101 || topology.VLANAliases["exp"] != 102 {
		t.Fatalf("published aliases = %v", topology.VLANAliases)
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
		t.Fatalf("FromConfig: %v", err)
	}

	if len(warnings) != 1 || !strings.HasPrefix(warnings[0],
		"Added 3 nodes from included topologies shared-services (2 nodes) and monitoring (1 node).") {
		t.Fatalf("warnings = %q, want one summary of the included nodes", warnings)
	}

	// Only the direct include is the topology's own reference.
	if !reflect.DeepEqual(doc.Source.IncludeTopologies, []string{"shared-services"}) {
		t.Fatalf("included topologies = %v", doc.Source.IncludeTopologies)
	}

	for hostname, want := range map[string]string{
		"router": "", "host-a": "", "dns": "shared-services", "ntp": "shared-services", "collector": "monitoring",
	} {
		if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
			t.Fatalf("%s is included from %q, want %q", hostname, got, want)
		}
	}

	// Included devices join the topology's own switches, and bring their own.
	for hostname, want := range map[string]string{"dns": "EXP", "ntp": "SERVICES", "collector": "MGMT"} {
		if got := connectedSwitch(doc, nodeByHostname(t, doc, hostname), "eth0"); got != want {
			t.Fatalf("%s eth0 is connected to %q, want %q", hostname, got, want)
		}
	}

	if got := len(doc.SwitchNodes()); got != 4 {
		t.Fatalf("switch count = %d, want 4 (EXP, MGMT, SENSOR and SERVICES)", got)
	}

	// The topology's own devices come first; each included topology starts
	// a row of its own.
	devices := doc.DeviceNodes()
	if devices[3].Device.Hostname != "standalone" || devices[4].Device.Hostname != "collector" {
		t.Fatalf("device order: %s", asJSON(t, devices))
	}

	if devices[4].Position.Y <= devices[3].Position.Y || devices[4].Position.X != 0 ||
		devices[5].Position.Y <= devices[4].Position.Y {
		t.Fatalf("included topologies do not start rows of their own: %s", asJSON(t, devices))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}

	// Publishing writes the reference, never the included devices, so phenix
	// does not find their hostnames twice when it merges the includes back.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 4 {
		t.Fatalf("published %d nodes, want the topology's own 4: %s", len(nodes), asJSON(t, topology.Spec))
	}

	if !reflect.DeepEqual(topology.Spec["includeTopologies"], []string{"shared-services"}) {
		t.Fatalf("published includes = %v", topology.Spec["includeTopologies"])
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
		t.Fatalf("FromConfig: %v", err)
	}

	for _, want := range []string{
		// phenix compares hostnames exactly; the Builder folds case, so a
		// case variant is reported too rather than silently merged.
		`included topology "clash" defines node "ROUTER", which duplicates a hostname in the topology itself`,
		`included topology "loop" defines node "shared-name", which duplicates a hostname in included topology "clash"`,
		`included topology "missing" could not be read and its nodes are not shown`,
		`included topology "builder-fixture" includes itself through a cycle`,
		`included topology "self" includes itself through a cycle`,
		"Added 3 nodes from included topologies clash (1 node), loop (1 node) and self (1 node).",
	} {
		if !containsSubstring(warnings, want) {
			t.Fatalf("expected a warning containing %q, got %q", want, warnings)
		}
	}

	if router := nodeByHostname(t, doc, "router"); router.Device.IncludedFrom != "" {
		t.Fatalf("the topology's own router was replaced by an included one: %s", asJSON(t, router))
	}

	for _, hostname := range []string{"shared-name", "looped", "self-node"} {
		if nodeByHostname(t, doc, hostname).Device.IncludedFrom == "" {
			t.Fatalf("%s is not marked as included", hostname)
		}
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document is invalid: %v", err)
	}
}

func TestFromExperimentConfigMarksIncludedNodes(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	// phenix merged the included topology's sensor into the experiment's
	// topology when it created the experiment, and kept the reference.
	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("fixture has no topology")
	}

	topology["includeTopologies"] = []any{"sensors"}

	// The topology the experiment was created from, as it is now.
	root := includeFixture("builder-fixture", []string{"sensors"}, map[string]string{"router": "EXP", "host-a": "EXP"})

	// Since the experiment was created, the included topology gained a node,
	// and one named like a node of the root topology.
	sensors := includeFixture("sensors", nil,
		map[string]string{"sensor": "SENSOR", "added-later": "SENSOR", "router": "EXP"})

	doc, warnings := documentFromConfigWith(t, config, builder.WithTopologyLoader(storeLoader(root, sensors)))

	if !containsSubstring(warnings, "Marked 1 experiment node as coming from included topology sensors (1 node).") {
		t.Fatalf("expected a summary of the marked nodes, got %q", warnings)
	}

	if got := nodeByHostname(t, doc, "sensor").Device.IncludedFrom; got != "sensors" {
		t.Fatalf("sensor is included from %q, want sensors", got)
	}

	// The root topology's own router is never taken for the included one,
	// and the clash phenix would reject is reported as on import from the
	// topology.
	if got := nodeByHostname(t, doc, "router").Device.IncludedFrom; got != "" {
		t.Fatalf("the root topology's router is marked as included from %q", got)
	}

	if !containsSubstring(warnings,
		`included topology "sensors" defines node "router", which duplicates a hostname in the topology itself; `+
			"phenix rejects duplicate hostnames") {
		t.Fatalf("expected a warning about the router clash, got %q", warnings)
	}

	// The experiment is shown as it is: a node the included topology gained
	// since is not added, and nothing is duplicated.
	if doc.FindDevice("added-later") != nil || len(doc.DeviceNodes()) != 3 {
		t.Fatalf("experiment devices changed: %s", asJSON(t, doc.DeviceNodes()))
	}

	projection, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	nodes, _ := projection.Spec["nodes"].([]any)
	for _, entry := range nodes {
		if node, _ := entry.(map[string]any); specString(node, "general", "hostname") == "sensor" {
			t.Fatalf("publishing copied the included sensor into the topology: %s", asJSON(t, projection.Spec))
		}
	}

	if len(nodes) != 2 || !reflect.DeepEqual(projection.Spec["includeTopologies"], []string{"sensors"}) {
		t.Fatalf("projection = %s, want router, host-a and the sensors include", asJSON(t, projection.Spec))
	}

	// phenix merged in the nodes of every include, also of one that cannot be
	// read now. A node that neither the root topology nor a readable include
	// defines is marked as coming from it, so publishing the topology does
	// not copy it in beside the include phenix merges again.
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
					`included topology "sensors" could not be read: ` + store.ErrNotExist.Error() +
						`. The experiment's 1 node that neither topology "builder-fixture" nor a readable included ` +
						"topology defines is marked as coming from it: shown read only, and not copied into the topology",
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
					`included topology "../extra.yml" could not be read: ` + errFromFile.Error() +
						`. The experiment's 1 node that neither topology "builder-fixture" nor a readable included ` +
						"topology defines is marked as coming from it",
					"Marked 1 experiment node as coming from included topology sensors (1 node).",
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
						t.Fatalf("expected a warning containing %q, got %q", want, warnings)
					}
				}

				for hostname, want := range tc.want {
					if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
						t.Fatalf("%s is included from %q, want %q", hostname, got, want)
					}
				}

				projection, err := doc.ToTopology()
				if err != nil {
					t.Fatalf("ToTopology: %v", err)
				}

				if nodes, _ := projection.Spec["nodes"].([]any); len(nodes) != 2 ||
					!reflect.DeepEqual(projection.Spec["includeTopologies"], tc.includes) {
					t.Fatalf("projection = %s, want router, host-a and the includes", asJSON(t, projection.Spec))
				}
			})
		}
	})
}

// TestFromExperimentConfigWithoutRootTopology falls back to matching nodes
// by hostname alone when the experiment's own topology cannot be read. The
// nodes of an include that cannot be read either are then indistinguishable
// from the topology's own, and the warning says they publish as its own.
func TestFromExperimentConfigWithoutRootTopology(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("fixture has no topology")
	}

	topology["includeTopologies"] = []any{"sensors", "missing"}

	sensors := includeFixture("sensors", nil, map[string]string{"sensor": "SENSOR"})

	doc, warnings := documentFromConfigWith(t, config, builder.WithTopologyLoader(storeLoader(sensors)))

	for _, want := range []string{
		`topology "builder-fixture", which the experiment was created from, could not be read, ` +
			"so its nodes are matched to included topologies by hostname alone",
		`included topology "missing" could not be read: ` + store.ErrNotExist.Error() +
			". Without the topology the experiment was created from, its nodes in the experiment cannot be " +
			"told apart from the topology's own, so they are shown as the topology's own " +
			"and publishing the topology copies them into it",
	} {
		if !containsSubstring(warnings, want) {
			t.Fatalf("expected a warning containing %q, got %q", want, warnings)
		}
	}

	for hostname, want := range map[string]string{"sensor": "sensors", "router": "", "host-a": ""} {
		if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != want {
			t.Fatalf("%s is included from %q, want %q", hostname, got, want)
		}
	}
}

// TestFromTopologyConfigIncludedTwice resolves a topology that two includes
// both include (a diamond) once, with one warning naming both paths, rather
// than reporting it as clashing with itself.
func TestFromTopologyConfigIncludedTwice(t *testing.T) {
	root := loadConfig(t, "topology.json")
	root.Spec["includeTopologies"] = []any{"left", "right"}

	left := includeFixture("left", []string{"shared"}, map[string]string{"left-node": "EXP"})
	right := includeFixture("right", []string{"shared", "shared"}, map[string]string{"right-node": "EXP"})
	shared := includeFixture("shared", nil, map[string]string{"shared-node": "EXP"})

	doc, warnings, err := builder.FromConfig(root, builder.WithTopologyLoader(storeLoader(left, right, shared)))
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	want := []string{
		`included topology "shared" is included more than once (through left and right twice); ` +
			"phenix rejects the duplicate hostnames this causes, so its nodes are shown once",
		"Added 3 nodes from included topologies left (1 node), shared (1 node) and right (1 node).",
	}

	if len(warnings) != len(want) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}

	for i := range want {
		if !strings.HasPrefix(warnings[i], want[i]) {
			t.Fatalf("warning %d = %q, want %q", i, warnings[i], want[i])
		}
	}

	if got := nodeByHostname(t, doc, "shared-node").Device.IncludedFrom; got != "shared" {
		t.Fatalf("shared-node is included from %q, want shared", got)
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

	services := includeFixture("services", []string{"storage"}, map[string]string{"dns": "EXP", "DB": "EXP"})
	storage := includeFixture("storage", []string{"root"}, map[string]string{"web": "EXP", "nas": "EXP"})

	report, err := builder.CheckIncludes("root", spec, storeLoader(services, storage))
	if err != nil {
		t.Fatalf("CheckIncludes: %v", err)
	}

	wantClashes := []builder.HostnameClash{
		{Include: "services", Hostname: "DB"},
		{Include: "storage", Hostname: "web"},
	}
	if !reflect.DeepEqual(report.Clashes, wantClashes) {
		t.Fatalf("clashes = %+v, want %+v", report.Clashes, wantClashes)
	}

	if len(report.Unreadable) != 1 || report.Unreadable[0].Name != "missing" ||
		!errors.Is(report.Unreadable[0], store.ErrNotExist) {
		t.Fatalf("unreadable = %+v, want the missing include", report.Unreadable)
	}

	clean, err := builder.CheckIncludes("root", map[string]any{"nodes": []any{}}, storeLoader())
	if err != nil || len(clean.Clashes) != 0 || len(clean.Unreadable) != 0 {
		t.Fatalf("a topology without includes: %+v, %v", clean, err)
	}
}

func TestFromConfigWithoutLoaderKeepsIncludesUnresolved(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	topology, ok := config.Spec["topology"].(map[string]any)
	if !ok {
		t.Fatal("fixture has no topology")
	}

	topology["includeTopologies"] = []any{"sensors"}

	doc, warnings := documentFromConfig(t, config)

	if !containsSubstring(warnings, "includes 1 other topologies that were not resolved") {
		t.Fatalf("expected a warning about unresolved includes, got %q", warnings)
	}

	for _, node := range doc.DeviceNodes() {
		if node.Device.IncludedFrom != "" {
			t.Fatalf("%s was marked without a loader", node.Device.Hostname)
		}
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
