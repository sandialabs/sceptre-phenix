package builder_test

import (
	"reflect"
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

	if _, err := doc.ValidateTopologyProjection("draft-topology"); err == nil {
		t.Fatal("expected the phenix topology schema to reject the draft projection")
	} else if !strings.Contains(err.Error(), "validating topology projection") {
		t.Fatalf("error %q is not a projection validation error", err.Error())
	}

	if _, _, err := doc.PublishTopologyConfig("draft-topology"); err == nil {
		t.Fatal("expected publishing a draft document to fail")
	}
}
