package builder_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/types"
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

	export, err := doc.ExportTopologyConfig("strict-topology")
	if err != nil {
		t.Fatalf("exporting topology: %v", err)
	}

	if len(export.PublishBlockers) != 0 || !reflect.DeepEqual(export.Config, config) {
		t.Fatalf("export = %s, %v, want the published config", asJSON(t, export.Config), export.PublishBlockers)
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
// naming the device and interface to fix. An export returns such a topology
// with that refusal, and refuses one phenix's schema refuses as publishing
// does.
func TestPublishTopologyConfigRefusesInterfaceWithoutVLAN(t *testing.T) {
	for name, tt := range map[string]struct {
		set     func(map[string]any)
		exports bool
	}{
		"empty":   {set: func(iface map[string]any) { iface["vlan"] = "" }, exports: true},
		"blank":   {set: func(iface map[string]any) { iface["vlan"] = "  " }, exports: true},
		"null":    {set: func(iface map[string]any) { iface["vlan"] = nil }, exports: false},
		"missing": {set: func(iface map[string]any) { delete(iface, "vlan") }, exports: false},
	} {
		t.Run(name, func(t *testing.T) {
			doc, eth0 := unconnectedHost(t)
			tt.set(eth0)

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

			export, exportErr := doc.ExportTopologyConfig("no-vlan")
			if !tt.exports {
				if export != nil || exportErr == nil || exportErr.Error() != err.Error() {
					t.Fatalf("ExportTopologyConfig = %v, %v, want the publish error %v", export, exportErr, err)
				}

				return
			}

			if exportErr != nil {
				t.Fatalf("ExportTopologyConfig returned error: %v", exportErr)
			}

			if len(export.PublishBlockers) != 1 || export.PublishBlockers[0].Error() != err.Error() {
				t.Fatalf("PublishBlockers = %v, want the publish error %v", export.PublishBlockers, err)
			}

			if vlan := specInterface(t, storedNode(t, export.Config.Spec, "host-a"), "eth0")["vlan"]; vlan != eth0["vlan"] {
				t.Fatalf("exported eth0 vlan = %q, want %q", vlan, eth0["vlan"])
			}
		})
	}
}

// A projection that phenix's schema refuses and whose interface also has no
// VLAN, or shares an address, is refused by publishing for the VLAN or the
// address, which says what to fix first. An export is refused for the VLAN
// only when the schema refuses it too (the key is missing or null); beside a
// blank VLAN or a shared address, which the schema accepts, it is refused
// with the schema's reason.
func TestExportTopologyConfigRefusesForTheSchemaReason(t *testing.T) {
	for name, tt := range map[string]struct {
		set func(map[string]any)
		// address is whether publishing refuses the shared address rather
		// than the VLAN.
		address bool
		// vlan is whether the export is refused for the VLAN.
		vlan bool
	}{
		"blank":   {set: func(iface map[string]any) { iface["vlan"] = " " }, address: false, vlan: false},
		"empty":   {set: func(iface map[string]any) { iface["vlan"] = "" }, address: false, vlan: false},
		"null":    {set: func(iface map[string]any) { iface["vlan"] = nil }, address: false, vlan: true},
		"missing": {set: func(iface map[string]any) { delete(iface, "vlan") }, address: false, vlan: true},
		// The router's eth0 has this address too.
		"shared address": {set: func(iface map[string]any) { iface["address"] = "10.0.0.1" }, address: true, vlan: false},
	} {
		t.Run(name, func(t *testing.T) {
			doc, eth0 := unconnectedHost(t)
			tt.set(eth0)
			eth0["mac"] = "not-a-mac"

			var (
				vlanErr    *builder.InterfaceVLANError
				addressErr *builder.InterfaceAddressError
			)

			// named reports whether err is the refusal publishing names first.
			named := func(err error) bool {
				if tt.address {
					return errors.As(err, &addressErr) && !errors.As(err, &vlanErr)
				}

				return errors.As(err, &vlanErr)
			}

			config, warnings, publishRefusal := doc.PublishTopologyConfig("mixed")
			if config != nil || !named(publishRefusal) {
				t.Fatalf("PublishTopologyConfig = %v, %v, want an InterfaceVLANError or InterfaceAddressError",
					config, publishRefusal)
			}

			if validated, err := doc.ValidateTopologyProjection("mixed"); !named(err) ||
				!slices.Equal(validated, warnings) {
				t.Fatalf("ValidateTopologyProjection = %q, %v, want %q and the publish refusal %v",
					validated, err, warnings, publishRefusal)
			}

			export, err := doc.ExportTopologyConfig("mixed")
			if export != nil || err == nil {
				t.Fatalf("ExportTopologyConfig = %v, %v, want a refusal", export, err)
			}

			if tt.vlan {
				if err.Error() != publishRefusal.Error() {
					t.Fatalf("export error = %v, want the publish error %v", err, publishRefusal)
				}

				return
			}

			if errors.As(err, &vlanErr) || errors.As(err, &addressErr) || !errors.Is(err, types.ErrValidationFailed) ||
				!strings.Contains(err.Error(), "/mac") {
				t.Fatalf("export error = %v, want the schema's refusal of the MAC address", err)
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

// withInterfaces returns the strict fixture, whose router and host-a each have
// one interface, eth0, with the spec interfaces of each device given in
// interfaces replaced: its eth0 extended with the first entry's fields, and
// the other entries added after it.
func withInterfaces(t *testing.T, interfaces map[string][]map[string]any) *builder.Document {
	t.Helper()

	doc := loadDocumentFixture(t, "strict-document.json")

	for hostname, entries := range interfaces {
		network, _ := nodeByHostname(t, doc, hostname).Device.Spec["network"].(map[string]any)
		ifaces, _ := network["interfaces"].([]any)

		eth0, ok := ifaces[0].(map[string]any)
		if !ok {
			t.Fatalf("%s has no eth0: %s", hostname, asJSON(t, network))
		}

		maps.Copy(eth0, entries[0])

		for _, entry := range entries[1:] {
			ifaces = append(ifaces, entry)
		}

		network["interfaces"] = ifaces
	}

	return doc
}

// sharedAddresses returns what publishing doc says about the addresses its
// interfaces share, or nil when it does not refuse them. The draft stays
// valid either way.
func sharedAddresses(t *testing.T, doc *builder.Document) []string {
	t.Helper()

	if err := doc.Validate(); err != nil {
		t.Fatalf("the draft must stay valid: %v", err)
	}

	_, _, err := doc.PublishTopologyConfig("shared")

	var addressErr *builder.InterfaceAddressError
	if !errors.As(err, &addressErr) {
		return nil
	}

	if _, err := doc.ValidateTopologyProjection("shared"); !errors.As(err, &addressErr) {
		t.Fatalf("ValidateTopologyProjection error = %v, want an InterfaceAddressError", err)
	}

	return addressErr.Problems
}

// Two interfaces with one IP or MAC address clash once the experiment runs,
// so publishing refuses them, however each is written: IP addresses are
// compared parsed, and MAC addresses in any case and with any separators.
func TestPublishTopologyConfigRefusesSharedAddresses(t *testing.T) {
	const both = `interface "eth0" of device "router" and interface "eth0" of device "host-a"`

	for name, test := range map[string]struct {
		router, host map[string]any
		want         string
	}{
		"IPv4": {
			router: map[string]any{"address": "10.0.0.5", "mask": 24},
			host:   map[string]any{"address": " 10.0.0.5/16 ", "mask": 16},
			want:   "IP address 10.0.0.5 is used by " + both,
		},
		"IPv6": {
			router: map[string]any{"address": "2001:db8::1"},
			host:   map[string]any{"address": "2001:DB8:0:0:0:0:0:1/64"},
			want:   "IP address 2001:db8::1 is used by " + both,
		},
		"IPv4 mapped into IPv6": {
			router: map[string]any{"address": "::ffff:10.0.0.5"},
			host:   map[string]any{"address": "10.0.0.5"},
			want:   "IP address 10.0.0.5 is used by " + both,
		},
		"MAC": {
			router: map[string]any{"mac": "AA-BB-CC-DD-EE-FF"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
			want:   "MAC address aa:bb:cc:dd:ee:ff is used by " + both,
		},
		// The phenix schema refuses this form, but it is the same address.
		"MAC in dotted form": {
			router: map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"mac": "AABB.CCDD.EEFF"},
			want:   "MAC address aa:bb:cc:dd:ee:ff is used by " + both,
		},
		// The editor trims the same characters (validate.js).
		"ASCII whitespace": {
			router: map[string]any{"address": "10.0.0.5", "mac": "aa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"address": "\t\v\f10.0.0.5\r\n", "mac": "\taa:bb:cc:dd:ee:ff\n"},
			want: "IP address 10.0.0.5 is used by " + both + "; " +
				"MAC address aa:bb:cc:dd:ee:ff is used by " + both,
		},
		// minirouter and Vyatta assign the address of a QinQ interface.
		"QinQ": {
			router: map[string]any{"address": "10.0.0.5", "qinq": true},
			host:   map[string]any{"address": "10.0.0.5"},
			want:   "IP address 10.0.0.5 is used by " + both,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			want := strings.Split(test.want, "; ")
			if got := sharedAddresses(t, doc); !reflect.DeepEqual(got, want) {
				t.Fatalf("problems = %q, want %q", got, want)
			}
		})
	}
}

// An export returns a document whose interfaces share an address, which
// publishing refuses, with the error publishing refuses it with as its only
// blocker. Beside an interface without a VLAN, which publishing refuses
// first, the export returns both, in that order.
func TestExportTopologyConfigReportsSharedAddresses(t *testing.T) {
	shared := map[string]any{"address": "10.0.0.5", "mask": 24}
	noVLAN := map[string]any{"name": "eth1", "type": "ethernet", "proto": "dhcp", "vlan": ""}

	for name, test := range map[string]struct {
		host []map[string]any
		// blockers are the export's blockers, by type.
		blockers []string
	}{
		"shared address":             {host: []map[string]any{shared}, blockers: []string{"address"}},
		"shared address and no VLAN": {host: []map[string]any{shared, noVLAN}, blockers: []string{"vlan", "address"}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{"router": {shared}, "host-a": test.host})

			_, _, refusal := doc.PublishTopologyConfig("shared")
			if refusal == nil {
				t.Fatal("published interfaces that share an address")
			}

			export, err := doc.ExportTopologyConfig("shared")
			if err != nil {
				t.Fatalf("ExportTopologyConfig returned error: %v", err)
			}

			kinds := make([]string, 0, len(export.PublishBlockers))

			for _, blocker := range export.PublishBlockers {
				var (
					vlanErr    *builder.InterfaceVLANError
					addressErr *builder.InterfaceAddressError
				)

				switch {
				case errors.As(blocker, &vlanErr):
					kinds = append(kinds, "vlan")
				case errors.As(blocker, &addressErr):
					kinds = append(kinds, "address")
				default:
					kinds = append(kinds, blocker.Error())
				}
			}

			if !reflect.DeepEqual(kinds, test.blockers) {
				t.Fatalf("blockers = %q (%v), want %q", kinds, export.PublishBlockers, test.blockers)
			}

			// Publishing refuses the document with the first.
			if export.PublishBlockers[0].Error() != refusal.Error() {
				t.Fatalf("first blocker = %v, want the publish error %v", export.PublishBlockers[0], refusal)
			}

			if got := export.PublishBlockers[len(export.PublishBlockers)-1].Error(); !strings.HasSuffix(
				got, `IP address 10.0.0.5 is used by interface "eth0" of device "router" and interface "eth0" of device "host-a"`,
			) {
				t.Fatalf("address blocker = %q", got)
			}
		})
	}
}

// Interfaces of one device are compared too, and past the two a problem
// names, the others are counted.
func TestPublishTopologyConfigRefusesAddressesOfOneDevice(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"host-a": {
			{"mac": "00:00:00:00:00:01"},
			{"name": "eth1", "vlan": "EXP", "proto": "static", "address": "10.0.0.2", "mac": "00-00-00-00-00-01"},
			{"name": "eth1", "vlan": "EXP", "mac": "00:00:00:00:00:01"},
		},
	})

	want := []string{
		`IP address 10.0.0.2 is used by interface "eth0" of device "host-a" and interface "eth1" (#2) of device "host-a"`,
		`MAC address 00:00:00:00:00:01 is used by interface "eth0" of device "host-a", ` +
			`interface "eth1" (#2) of device "host-a" and 1 more interface`,
	}
	if got := sharedAddresses(t, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}
}

// An interface that asks DHCP for its address has none of its own, phenix
// brings a manual one up with none, and minimega makes a MAC for one that has
// none; a mask or a gateway may be shared. Only ASCII whitespace is trimmed,
// as the editor trims it (validate.js), so an address in other whitespace
// does not parse.
func TestPublishTopologyConfigSkipsAddressesNotAssigned(t *testing.T) {
	for name, test := range map[string]struct {
		router, host map[string]any
	}{
		"DHCP":             {router: map[string]any{"proto": "dhcp", "address": "10.0.0.2"}, host: nil},
		"manual":           {router: map[string]any{"proto": "manual", "address": "10.0.0.2", "mask": 24}, host: nil},
		"blank address":    {router: map[string]any{"address": " "}, host: map[string]any{"address": " "}},
		"blank MAC":        {router: map[string]any{"mac": ""}, host: map[string]any{"mac": ""}},
		"unreadable":       {router: map[string]any{"address": "10.0.0.256"}, host: map[string]any{"address": "10.0.0.256"}},
		"mask and gateway": {router: map[string]any{"gateway": "10.0.0.254"}, host: map[string]any{"gateway": "10.0.0.254"}},
		"U+FEFF":           {router: map[string]any{"address": "\ufeff10.0.0.2"}, host: nil},
		"U+0085":           {router: map[string]any{"address": "10.0.0.2\u0085"}, host: nil},
		"no-break space":   {router: map[string]any{"address": "\u00a010.0.0.2"}, host: nil},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			if _, _, err := doc.PublishTopologyConfig("unshared"); err != nil {
				t.Fatalf("PublishTopologyConfig: %v", err)
			}
		})
	}
}

// The phenix schema refuses these interfaces, but not for the address they
// share: a proto is read in any case and without ASCII whitespace, and a MAC
// in whitespace other than ASCII does not parse.
func TestPublishTopologyConfigSkipsAddressesNotAssignedAsTyped(t *testing.T) {
	for name, test := range map[string]struct {
		router, host map[string]any
	}{
		"DHCP in any case": {router: map[string]any{"proto": " DHCP\t", "address": "10.0.0.2"}, host: nil},
		"manual in any case": {
			router: map[string]any{"proto": "\tManual ", "address": "10.0.0.2"},
			host:   nil,
		},
		"MAC after U+FEFF": {
			router: map[string]any{"mac": "\ufeffaa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
		},
		"MAC before U+0085": {
			router: map[string]any{"mac": "aa:bb:cc:dd:ee:ff\u0085"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			if got := sharedAddresses(t, doc); got != nil {
				t.Fatalf("problems = %q, want none", got)
			}
		})
	}
}

// phenix does not start an external device, and its schema has no MAC, so its
// MAC does not count. Its IP address, which the real device uses, does.
func TestPublishTopologyConfigComparesExternalDevicesByIP(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"router": {{"mac": "aa:bb:cc:dd:ee:ff"}},
		"host-a": {{"mac": "AA:BB:CC:DD:EE:FF"}},
	})
	host := nodeByHostname(t, doc, "host-a")
	host.Device.Spec["external"] = true
	host.Device.Spec["type"] = "HIL"

	if _, _, err := doc.PublishTopologyConfig("external"); err != nil {
		t.Fatalf("PublishTopologyConfig: %v", err)
	}

	network, _ := host.Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)
	eth0, _ := ifaces[0].(map[string]any)
	eth0["address"] = "10.0.0.1"

	want := []string{
		`IP address 10.0.0.1 is used by interface "eth0" of device "router" and interface "eth0" of device "host-a"`,
	}
	if got := sharedAddresses(t, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}
}

// phenix merges the devices of included topologies into the experiment, so
// their addresses count, but one that only included devices share is their
// topology's to fix.
func TestPublishTopologyConfigComparesIncludedDevices(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"host-a": {{"address": "10.0.0.1"}},
	})
	doc.Source.IncludeTopologies = []string{"shared"}
	nodeByHostname(t, doc, "host-a").Device.IncludedFrom = "shared"

	want := []string{
		`IP address 10.0.0.1 is used by interface "eth0" of device "router" and interface "eth0" of device "host-a"`,
	}
	if got := sharedAddresses(t, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %q, want %q", got, want)
	}

	nodeByHostname(t, doc, "router").Device.IncludedFrom = "shared"

	if got := sharedAddresses(t, doc); got != nil {
		t.Fatalf("problems = %q, want none among included devices", got)
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

// renamedHost returns the strict fixture with host-a renamed to hostname and
// its os_type set to osType.
func renamedHost(t *testing.T, hostname, osType string) *builder.Document {
	t.Helper()

	doc := loadDocumentFixture(t, "strict-document.json")
	node := nodeByHostname(t, doc, "host-a")
	node.Device.Hostname = hostname

	general, _ := node.Device.Spec["general"].(map[string]any)
	general["hostname"] = hostname

	hardware, _ := node.Device.Spec["hardware"].(map[string]any)
	hardware["os_type"] = osType

	return doc
}

// phenix stores a topology with a hostname it refuses once it creates an
// experiment from it, and its schema refuses a hostname of one character.
// Publishing refuses both, in phenix's words, which name the hostname, and a
// draft keeps them, as one imported from a topology an older phenix stored
// has them. An export returns the topology with the first as a publish
// blocker, and is refused for the second, as the schema refuses it. A
// hostname phenix only warns about publishes, with phenix's warning.
func TestPublishTopologyConfigChecksHostnames(t *testing.T) {
	for name, tt := range map[string]struct {
		hostname, osType string
		// refused starts publishing's reason, or is "" when it publishes.
		refused string
		// exports is whether an export returns the topology.
		exports bool
		// warning starts the warning phenix logs, or is "" for none.
		warning string
	}{
		"all": {
			hostname: "all", osType: "linux", refused: "hostname 'all' is reserved", exports: true, warning: "",
		},
		"digits": {
			hostname: "42", osType: "linux", refused: "hostname '42' is all digits", exports: true, warning: "",
		},
		"phenix on Windows": {
			hostname: "phenix", osType: "windows", refused: "hostname 'phenix' can't be used for a Windows node",
			exports: true, warning: "",
		},
		"one character": {
			hostname: "a", osType: "linux", refused: "hostname 'a' is 1 character long", exports: false, warning: "",
		},
		"all in another case": {
			hostname: "All", osType: "linux", refused: "", exports: true,
			warning: "hostname 'All' differs from the reserved name 'all' only by case",
		},
		"phenix": {
			hostname: "Phenix", osType: "linux", refused: "", exports: true, warning: "hostname 'Phenix' matches 'phenix'",
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := renamedHost(t, tt.hostname, tt.osType)

			if err := doc.Validate(); err != nil {
				t.Fatalf("the draft must stay valid: %v", err)
			}

			config, warnings, err := doc.PublishTopologyConfig("hostnames")

			var hostnameErr *builder.NodeHostnameError

			switch {
			case tt.refused == "" && err != nil:
				t.Fatalf("PublishTopologyConfig returned error: %v", err)
			case tt.refused != "" && (config != nil || !errors.As(err, &hostnameErr) ||
				len(hostnameErr.Problems) != 1 || !strings.HasPrefix(hostnameErr.Problems[0], tt.refused)):
				t.Fatalf("PublishTopologyConfig = %v, %v, want a NodeHostnameError starting %q", config, err, tt.refused)
			}

			var hostnameWarnings []string

			for _, warning := range warnings {
				if strings.HasPrefix(warning, "hostname '") {
					hostnameWarnings = append(hostnameWarnings, warning)
				}
			}

			if tt.warning == "" && len(hostnameWarnings) != 0 ||
				tt.warning != "" && (len(hostnameWarnings) != 1 || !strings.HasPrefix(hostnameWarnings[0], tt.warning)) {
				t.Fatalf("hostname warnings = %q, want one starting %q", hostnameWarnings, tt.warning)
			}

			export, exportErr := doc.ExportTopologyConfig("hostnames")
			if !tt.exports {
				if export != nil || exportErr == nil || exportErr.Error() != err.Error() {
					t.Fatalf("ExportTopologyConfig = %v, %v, want the publish error %v", export, exportErr, err)
				}

				return
			}

			if exportErr != nil {
				t.Fatalf("ExportTopologyConfig returned error: %v", exportErr)
			}

			if !slices.Equal(export.Warnings, warnings) {
				t.Fatalf("export warnings = %q, want %q", export.Warnings, warnings)
			}

			switch {
			case tt.refused == "" && len(export.PublishBlockers) != 0:
				t.Fatalf("PublishBlockers = %v, want none", export.PublishBlockers)
			case tt.refused != "" && (len(export.PublishBlockers) != 1 || export.PublishBlockers[0].Error() != err.Error()):
				t.Fatalf("PublishBlockers = %v, want the publish error %v", export.PublishBlockers, err)
			}
		})
	}
}

// Publishing names interfaces without a VLAN before a hostname phenix
// refuses, and an export reports both in that order. An external device's
// hostname is not checked, as phenix does not start it.
func TestPublishTopologyConfigChecksHostnamesAfterInterfaces(t *testing.T) {
	doc := renamedHost(t, "all", "linux")

	network, _ := nodeByHostname(t, doc, "all").Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)
	network["interfaces"] = append(ifaces, map[string]any{"name": "eth1", "type": "ethernet", "proto": "dhcp", "vlan": ""})

	_, _, refusal := doc.PublishTopologyConfig("ordered")

	var vlanErr *builder.InterfaceVLANError
	if !errors.As(refusal, &vlanErr) {
		t.Fatalf("PublishTopologyConfig error = %v, want an InterfaceVLANError", refusal)
	}

	export, err := doc.ExportTopologyConfig("ordered")
	if err != nil {
		t.Fatalf("ExportTopologyConfig returned error: %v", err)
	}

	var hostnameErr *builder.NodeHostnameError
	if len(export.PublishBlockers) != 2 || export.PublishBlockers[0].Error() != refusal.Error() ||
		!errors.As(export.PublishBlockers[1], &hostnameErr) {
		t.Fatalf("PublishBlockers = %v, want the VLAN, then the hostname", export.PublishBlockers)
	}

	external := renamedHost(t, "42", "linux")
	nodeByHostname(t, external, "42").Device.Spec["external"] = true

	if _, _, err := external.PublishTopologyConfig("external"); errors.As(err, &hostnameErr) {
		t.Fatalf("PublishTopologyConfig refused an external device's hostname: %v", err)
	}
}
