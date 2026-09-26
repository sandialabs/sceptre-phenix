package builder_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestToTopologyOmitsNonDeviceNodes(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	nodes, ok := topology.Spec["nodes"].([]any)
	if !ok {
		t.Fatalf("spec has no nodes: %s", asJSON(t, topology.Spec))
	}

	if len(nodes) != 2 {
		t.Fatalf("mapped %d nodes, want 2 (switch, note, and group must be omitted): %s",
			len(nodes), asJSON(t, topology.Spec))
	}
}

func TestToTopologyAppliesConnectedNetworkVLAN(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	router := specNode(t, topology, "router")

	if vlan := specInterface(t, router, "eth0")["vlan"]; vlan != "EXP" {
		t.Fatalf("router eth0 vlan = %v, want EXP (the connected network name)", vlan)
	}

	// eth1 is not connected: it must be preserved untouched, without a VLAN.
	eth1 := specInterface(t, router, "eth1")

	if vlan, ok := eth1["vlan"]; ok {
		t.Fatalf("unconnected router eth1 gained vlan %v", vlan)
	}

	if eth1["proto"] != "dhcp" {
		t.Fatalf("unconnected interface was modified: %s", asJSON(t, eth1))
	}

	// host-a is connected by an edge whose switch is the *source* endpoint.
	host := specNode(t, topology, "host-a")

	if vlan := specInterface(t, host, "eth0")["vlan"]; vlan != "EXP" {
		t.Fatalf("host-a eth0 vlan = %v, want EXP", vlan)
	}

	// A connection's label and color are the canvas's alone.
	doc.Edges[0].Label = "uplink"
	doc.Edges[0].Color = "#c0392b"

	styled, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if asJSON(t, styled.Spec) != asJSON(t, topology.Spec) {
		t.Fatalf("a connection's label and color changed the spec:\nbefore: %s\nafter: %s",
			asJSON(t, topology.Spec), asJSON(t, styled.Spec))
	}
}

func TestToTopologyPreservesNodeSemantics(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	router := specNode(t, topology, "router")

	general, ok := router["general"].(map[string]any)
	if !ok {
		t.Fatalf("router has no general section: %s", asJSON(t, router))
	}

	if general["description"] != "core router" {
		t.Fatalf("general.description = %v, want %q", general["description"], "core router")
	}

	if router["type"] != "VirtualMachine" {
		t.Fatalf("node type = %v, want VirtualMachine", router["type"])
	}

	hardware, ok := router["hardware"].(map[string]any)
	if !ok || hardware["os_type"] != "linux" {
		t.Fatalf("hardware was not preserved: %s", asJSON(t, router))
	}
}

func TestToTopologyDoesNotMutateDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	before := asJSON(t, doc)

	if _, err := doc.ToTopology(); err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if after := asJSON(t, doc); after != before {
		t.Fatalf("ToTopology mutated the document:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestToTopologyVLANAliases(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	want := map[string]int{"EXP": 101, "RESERVED": 250}

	if !reflect.DeepEqual(topology.VLANAliases, want) {
		t.Fatalf("VLAN aliases = %v, want %v", topology.VLANAliases, want)
	}

	names := topology.SortedVLANAliasNames()
	if !reflect.DeepEqual(names, []string{"EXP", "RESERVED"}) {
		t.Fatalf("sorted alias names = %v", names)
	}
}

func TestToTopologyWarnsOnHandleWithoutInterface(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	router := nodeByHostname(t, doc, "router")

	router.Device.Interfaces = append(router.Device.Interfaces, builder.InterfaceHandle{
		ID:    idHRouterEth9,
		Name:  "eth9",
		Index: 9,
	})

	doc.Edges = append(doc.Edges, builder.Edge{
		ID:             idERouterEth9,
		SourceNodeID:   router.ID,
		SourceHandleID: idHRouterEth9,
		TargetNodeID:   idSwExp,
		NetworkID:      idNetExp,
	})

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if !containsSubstring(topology.Warnings, `interface "eth9"`) {
		t.Fatalf("expected a warning about the missing interface, got %v", topology.Warnings)
	}
}

func TestToTopologyRejectsInvalidDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Edges[0].NetworkID = idNetNope

	if _, err := doc.ToTopology(); err == nil {
		t.Fatal("expected ToTopology to reject an invalid document")
	}
}

func TestToTopologySpecV1(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	// The schema allows a string for vcpus and memory, and one address for
	// dns. phenix decodes them weakly, and so must SpecV1, with which an
	// experiment update decodes the projection.
	for _, node := range doc.DeviceNodes() {
		if node.Device.Hostname != "router" {
			continue
		}

		spec := node.Device.Spec
		hardware, _ := spec["hardware"].(map[string]any)
		hardware["vcpus"] = "2"
		hardware["memory"] = "4096"
		network, _ := spec["network"].(map[string]any)
		interfaces, _ := network["interfaces"].([]any)
		first, _ := interfaces[0].(map[string]any)
		first["dns"] = "8.8.8.8"
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	spec, err := topology.SpecV1()
	if err != nil {
		t.Fatalf("SpecV1: %v", err)
	}

	node := spec.FindNodeByName("router")
	if node == nil {
		t.Fatalf("v1 spec has no router node")
	}

	if got := node.General().Description(); got != "core router" {
		t.Fatalf("description = %q, want %q", got, "core router")
	}

	// Note the legacy v1 API: InterfaceVLAN maps a VLAN name to its interface.
	if got := node.Network().InterfaceVLAN("EXP"); got != "eth0" {
		t.Fatalf("interface on VLAN EXP = %q, want eth0", got)
	}

	if got := node.Network().InterfaceMask("eth0"); got != 24 {
		t.Fatalf("eth0 mask = %d, want 24", got)
	}

	if hardware := node.Hardware(); hardware.VCPU() != 2 || hardware.Memory() != 4096 {
		t.Fatalf("vcpus, memory = %d, %d, want 2, 4096", hardware.VCPU(), hardware.Memory())
	}

	if got := node.Network().Interfaces()[0].DNS(); !slices.Equal(got, []string{"8.8.8.8"}) {
		t.Fatalf("eth0 dns = %v, want [8.8.8.8]", got)
	}
}

func TestToTopologyConfig(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	config, warnings, err := doc.ToTopologyConfig("generated-topo")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	if config.Kind != "Topology" {
		t.Fatalf("kind = %q, want Topology", config.Kind)
	}

	if config.Version != builder.TopologyAPIVersion {
		t.Fatalf("apiVersion = %q, want %q", config.Version, builder.TopologyAPIVersion)
	}

	if config.Metadata.Name != "generated-topo" {
		t.Fatalf("name = %q, want generated-topo", config.Metadata.Name)
	}

	if _, ok := config.Spec["nodes"]; !ok {
		t.Fatalf("config spec has no nodes: %s", asJSON(t, config.Spec))
	}
}

func TestToTopologyDeviceHostnameWins(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	router := nodeByHostname(t, doc, "router")
	router.Device.Hostname = "renamed-router"
	router.ID = builder.DeviceNodeID("renamed-router")

	for i := range doc.Edges {
		if doc.Edges[i].SourceNodeID == idDevRouter {
			doc.Edges[i].SourceNodeID = router.ID
		}
	}

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	node := specNode(t, topology, "renamed-router")

	if vlan := specInterface(t, node, "eth0")["vlan"]; vlan != "EXP" {
		t.Fatalf("renamed device lost its network: %v", vlan)
	}

	if !containsSubstring(topology.Warnings, "the device hostname wins") {
		t.Fatalf("expected a hostname override warning, got %v", topology.Warnings)
	}
}

func TestValidateTopologyProjectionAcceptsCompleteDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "strict-document.json")

	if err := doc.Validate(); err != nil {
		t.Fatalf("fixture document is invalid: %v", err)
	}

	warnings, err := doc.ValidateTopologyProjection("strict-topology")
	if err != nil {
		t.Fatalf("projection was rejected: %v", err)
	}

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestPublishTopologyConfigReturnsValidatedConfig(t *testing.T) {
	doc := loadDocumentFixture(t, "strict-document.json")

	config, _, err := doc.PublishTopologyConfig("strict-topology")
	if err != nil {
		t.Fatalf("publishing topology: %v", err)
	}

	if config.Kind != "Topology" {
		t.Fatalf("kind = %q, want Topology", config.Kind)
	}

	if config.Version != builder.TopologyAPIVersion {
		t.Fatalf("version = %q, want %q", config.Version, builder.TopologyAPIVersion)
	}

	if config.Metadata.Name != "strict-topology" {
		t.Fatalf("name = %q, want strict-topology", config.Metadata.Name)
	}

	router := storedNode(t, config.Spec, "router")
	if specInterface(t, router, "eth0")["vlan"] != "EXP" {
		t.Fatalf("router eth0 was not connected: %s", asJSON(t, router))
	}
}

func TestValidateTopologyProjectionRejectsDraftDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	// The draft working copy is structurally valid even though it holds an
	// interface that is not connected to any network yet.
	if err := doc.Validate(); err != nil {
		t.Fatalf("draft document is invalid: %v", err)
	}

	// Its unconnected router eth1 has no VLAN.
	const want = `interface "eth1" of device "router" has no VLAN`

	if _, err := doc.ValidateTopologyProjection("draft-topology"); err == nil {
		t.Fatal("expected the draft projection to be rejected")
	} else if !strings.Contains(err.Error(), "validating topology projection: "+want) {
		t.Fatalf("error %q does not name the interface without a VLAN", err.Error())
	}

	if _, _, err := doc.PublishTopologyConfig("draft-topology"); err == nil {
		t.Fatal("expected publishing a draft document to fail")
	}
}

// unconnectedHost returns the strict fixture with host-a's eth0 disconnected,
// and that interface's spec entry, for a test to set its VLAN.
func unconnectedHost(t *testing.T) (*builder.Document, map[string]any) {
	t.Helper()

	doc := loadDocumentFixture(t, "strict-document.json")
	doc.Edges = slices.DeleteFunc(doc.Edges, func(edge builder.Edge) bool {
		return edge.ID == idEHostEth0
	})

	network, _ := nodeByHostname(t, doc, "host-a").Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)

	eth0, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("host-a has no eth0: %s", asJSON(t, network))
	}

	return doc, eth0
}

// phenix stores a topology whose interface has an empty VLAN, and minimega
// refuses it only when the experiment starts; publishing refuses it first,
// naming the device and interface to fix.
func TestPublishTopologyConfigRefusesInterfaceWithoutVLAN(t *testing.T) {
	for name, set := range map[string]func(map[string]any){
		"empty":   func(iface map[string]any) { iface["vlan"] = "" },
		"blank":   func(iface map[string]any) { iface["vlan"] = "  " },
		"null":    func(iface map[string]any) { iface["vlan"] = nil },
		"missing": func(iface map[string]any) { delete(iface, "vlan") },
	} {
		t.Run(name, func(t *testing.T) {
			doc, eth0 := unconnectedHost(t)
			set(eth0)

			if err := doc.Validate(); err != nil {
				t.Fatalf("the draft must stay valid: %v", err)
			}

			config, _, err := doc.PublishTopologyConfig("no-vlan")
			if err == nil {
				t.Fatalf("published %s", asJSON(t, config.Spec))
			}

			var vlanErr *builder.InterfaceVLANError
			if !errors.As(err, &vlanErr) {
				t.Fatalf("error %q is not an InterfaceVLANError", err.Error())
			}

			want := []string{
				`interface "eth0" of device "host-a" has no VLAN: connect it to a network, or type a VLAN for it`,
			}
			if !reflect.DeepEqual(vlanErr.Problems, want) {
				t.Fatalf("problems = %q, want %q", vlanErr.Problems, want)
			}

			if _, err := doc.ValidateTopologyProjection("no-vlan"); !errors.As(err, &vlanErr) {
				t.Fatalf("ValidateTopologyProjection error = %v, want an InterfaceVLANError", err)
			}
		})
	}
}

// Every interface of the node spec is checked, including those with no
// canvas handle, and one whose name does not tell it apart is named by its
// position: an unnamed one, and each of two with the same name.
func TestPublishTopologyConfigNamesInterfacesByPosition(t *testing.T) {
	doc, eth0 := unconnectedHost(t)
	eth0["vlan"] = "EXP"

	network, _ := nodeByHostname(t, doc, "host-a").Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)
	network["interfaces"] = append(ifaces,
		map[string]any{"name": " ", "vlan": ""},
		map[string]any{"name": "eth1"},
		map[string]any{"name": "eth1", "vlan": "EXP"},
		map[string]any{"name": "eth0"},
	)

	_, _, err := doc.PublishTopologyConfig("positions")

	var vlanErr *builder.InterfaceVLANError
	if !errors.As(err, &vlanErr) {
		t.Fatalf("error %v is not an InterfaceVLANError", err)
	}

	const fix = " has no VLAN: connect it to a network, or type a VLAN for it"

	want := []string{
		`interface #2 of device "host-a"` + fix,
		`interface "eth1" (#3) of device "host-a"` + fix,
		`interface "eth0" (#5) of device "host-a"` + fix,
	}
	if !reflect.DeepEqual(vlanErr.Problems, want) {
		t.Fatalf("problems = %q, want %q", vlanErr.Problems, want)
	}
}

// phenix allocates VLANs by name, so an unconnected interface's VLAN that
// names no network of the document is published as it is. An external node
// is not started, so its interfaces need no VLAN.
func TestPublishTopologyConfigKeepsVLANsOfUnconnectedInterfaces(t *testing.T) {
	doc, eth0 := unconnectedHost(t)
	eth0["vlan"] = "GHOST"

	config, _, err := doc.PublishTopologyConfig("ghost-vlan")
	if err != nil {
		t.Fatalf("publishing a VLAN that names no network: %v", err)
	}

	if vlan := specInterface(t, storedNode(t, config.Spec, "host-a"), "eth0")["vlan"]; vlan != "GHOST" {
		t.Fatalf("host-a eth0 vlan = %v, want GHOST", vlan)
	}

	doc, _ = unconnectedHost(t)
	nodeByHostname(t, doc, "host-a").Device.Spec = map[string]any{
		"external": true,
		"type":     "HIL",
		"general":  map[string]any{"hostname": "host-a"},
		"network": map[string]any{"interfaces": []any{
			map[string]any{"name": "eth0", "vlan": ""},
			map[string]any{"name": "eth1"},
		}},
	}

	if _, _, err := doc.PublishTopologyConfig("external-no-vlan"); err != nil {
		t.Fatalf("an external node's interfaces need no VLAN: %v", err)
	}
}

func TestToTopologyOmitsIncludedDevices(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Source.IncludeTopologies = []string{"shared"}
	nodeByHostname(t, doc, "host-a").Device.IncludedFrom = "shared"

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	nodes, _ := topology.Spec["nodes"].([]any)
	if len(nodes) != 1 || specNode(t, topology, "router") == nil {
		t.Fatalf("mapped nodes = %s, want only the document's own router", asJSON(t, nodes))
	}

	if !reflect.DeepEqual(topology.Spec["includeTopologies"], []string{"shared"}) {
		t.Fatalf("includeTopologies = %v, want the reference that brings host-a back", topology.Spec["includeTopologies"])
	}
}
