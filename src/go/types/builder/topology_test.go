package builder_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
	"phenix/types/builder"
)

// ignoreConfigSpec leaves a topology config's spec out of a comparison of its header.
var ignoreConfigSpec = cmpopts.IgnoreFields(store.Config{}, "Spec") //nolint:gochecknoglobals // test option

// errorTexts returns the text of each error, or nil for none.
func errorTexts(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}

	texts := make([]string, 0, len(errs))

	for _, err := range errs {
		texts = append(texts, err.Error())
	}

	return texts
}

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
		t.Errorf("ToTopology() = %d nodes, want 2 (switch, note, and group omitted): %s",
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
		t.Errorf("ToTopology() router eth0 vlan = %v, want EXP (the connected network name)", vlan)
	}

	// eth1 is not connected: it must be preserved untouched, without a VLAN.
	eth1 := specInterface(t, router, "eth1")

	if vlan, ok := eth1["vlan"]; ok {
		t.Errorf("ToTopology() unconnected router eth1 vlan = %v, want none", vlan)
	}

	if eth1["proto"] != "dhcp" {
		t.Errorf("ToTopology() unconnected router eth1 = %s, want it unchanged with proto dhcp", asJSON(t, eth1))
	}

	// host-a is connected by an edge whose switch is the *source* endpoint.
	host := specNode(t, topology, "host-a")

	if vlan := specInterface(t, host, "eth0")["vlan"]; vlan != "EXP" {
		t.Errorf("ToTopology() host-a eth0 vlan = %v, want EXP", vlan)
	}

	// A connection's label and color are the canvas's alone.
	doc.Edges[0].Label = "uplink"
	doc.Edges[0].Color = "#c0392b"

	styled, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, topology.Spec), jsonValue(t, styled.Spec)); diff != "" {
		t.Errorf("ToTopology() with a styled connection spec mismatch (-want +got):\n%s", diff)
	}
}

func TestToTopologyPreservesNodeSemantics(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	router := specNode(t, topology, "router")

	if general, ok := router["general"].(map[string]any); !ok {
		t.Errorf("ToTopology() router = %s, want a general section", asJSON(t, router))
	} else if general["description"] != "core router" {
		t.Errorf("ToTopology() router general.description = %v, want %q", general["description"], "core router")
	}

	if router["type"] != "VirtualMachine" {
		t.Errorf("ToTopology() router type = %v, want VirtualMachine", router["type"])
	}

	if hardware, ok := router["hardware"].(map[string]any); !ok || hardware["os_type"] != "linux" {
		t.Errorf("ToTopology() router = %s, want its hardware with os_type linux", asJSON(t, router))
	}
}

func TestToTopologyDoesNotMutateDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	before := jsonValue(t, doc)

	if _, err := doc.ToTopology(); err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if diff := cmp.Diff(before, jsonValue(t, doc)); diff != "" {
		t.Errorf("ToTopology() changed the document (-before +after):\n%s", diff)
	}
}

func TestToTopologyVLANAliases(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	want := map[string]int{"EXP": 101, "RESERVED": 250}

	if diff := cmp.Diff(want, topology.VLANAliases); diff != "" {
		t.Errorf("ToTopology() VLANAliases mismatch (-want +got):\n%s", diff)
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

	if !containsSubstring(builder.IssueMessages(topology.Warnings), `interface "eth9"`) {
		t.Errorf("ToTopology() warnings = %v, want one naming interface \"eth9\"", topology.Warnings)
	}

	if !slices.ContainsFunc(topology.Warnings, func(warning builder.Issue) bool {
		return warning.Code == builder.CodeInterfaceNameUnmatched && warning.NodeID == router.ID &&
			warning.Severity == builder.SeverityWarning
	}) {
		t.Errorf("ToTopology() warnings = %v, want a %s warning at the router", topology.Warnings,
			builder.CodeInterfaceNameUnmatched)
	}
}

func TestToTopologyRejectsInvalidDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Edges[0].NetworkID = idNetNope

	if _, err := doc.ToTopology(); err == nil {
		t.Error("ToTopology() of an edge to an unknown network = nil error, want an error")
	}
}

func TestToTopologySpecV1(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	// The schema allows text for vcpus and memory and one address for dns. phenix decodes them weakly,
	// and so must SpecV1, which decodes the projection on an experiment update.
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
		t.Fatalf("SpecV1() has no router node")
	}

	if got := node.General().Description(); got != "core router" {
		t.Errorf("SpecV1() router description = %q, want %q", got, "core router")
	}

	// Note the legacy v1 API: InterfaceVLAN maps a VLAN name to its interface.
	if got := node.Network().InterfaceVLAN("EXP"); got != "eth0" {
		t.Errorf("SpecV1() router InterfaceVLAN(EXP) = %q, want eth0", got)
	}

	if got := node.Network().InterfaceMask("eth0"); got != 24 {
		t.Errorf("SpecV1() router InterfaceMask(eth0) = %d, want 24", got)
	}

	type sizes struct{ VCPU, Memory int }

	hardware := node.Hardware()
	if diff := cmp.Diff(sizes{VCPU: 2, Memory: 4096}, sizes{VCPU: hardware.VCPU(), Memory: hardware.Memory()}); diff != "" {
		t.Errorf("SpecV1() router hardware mismatch (-want +got):\n%s", diff)
	}

	if interfaces := node.Network().Interfaces(); len(interfaces) == 0 {
		t.Errorf("SpecV1() router has no interfaces, want eth0")
	} else if diff := cmp.Diff([]string{"8.8.8.8"}, interfaces[0].DNS()); diff != "" {
		t.Errorf("SpecV1() router eth0 DNS() mismatch (-want +got):\n%s", diff)
	}
}

func TestToTopologyConfig(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	config, warnings, err := doc.ToTopologyConfig("generated-topo")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("ToTopologyConfig(generated-topo) warnings = %v, want none", warnings)
	}

	want := &store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "generated-topo"},
	}
	if diff := cmp.Diff(want, config, ignoreConfigSpec); diff != "" {
		t.Errorf("ToTopologyConfig(generated-topo) mismatch (-want +got):\n%s", diff)
	}

	if _, ok := config.Spec["nodes"]; !ok {
		t.Errorf("ToTopologyConfig(generated-topo) spec = %s, want nodes", asJSON(t, config.Spec))
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
		t.Errorf("ToTopology() renamed-router eth0 vlan = %v, want EXP", vlan)
	}

	if !containsSubstring(builder.IssueMessages(topology.Warnings), "The topology uses the device hostname") {
		t.Errorf("ToTopology() warnings = %v, want one saying the device hostname wins", topology.Warnings)
	}

	if !slices.ContainsFunc(topology.Warnings, func(warning builder.Issue) bool {
		return warning.Code == builder.CodeDeviceHostnameMismatch
	}) {
		t.Errorf("ToTopology() warnings = %v, want a %s warning", topology.Warnings, builder.CodeDeviceHostnameMismatch)
	}
}

func TestPublishTopologyReturnsTheProjectionTheConfigHolds(t *testing.T) {
	doc := loadDocumentFixture(t, "strict-document.json")

	config, projection, warnings, err := doc.PublishTopology("strict-topology")
	if err != nil {
		t.Fatalf("publishing topology: %v", err)
	}

	want, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("projecting topology: %v", err)
	}

	if diff := cmp.Diff(want, projection); diff != "" {
		t.Errorf("PublishTopology(strict-topology) projection mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(projection.Spec, config.Spec); diff != "" {
		t.Errorf("PublishTopology(strict-topology) config spec mismatch, want the projection spec (-want +got):\n%s", diff)
	}

	if len(warnings) != 0 {
		t.Errorf("PublishTopology(strict-topology) warnings = %v, want none", warnings)
	}
}

func TestPublishTopologyReturnsValidatedConfig(t *testing.T) {
	doc := loadDocumentFixture(t, "strict-document.json")

	if err := doc.Validate(); err != nil {
		t.Fatalf("fixture document is invalid: %v", err)
	}

	config, _, warnings, err := doc.PublishTopology("strict-topology")
	if err != nil {
		t.Fatalf("publishing topology: %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("PublishTopology(strict-topology) warnings = %v, want none", warnings)
	}

	want := &store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "strict-topology"},
	}
	if diff := cmp.Diff(want, config, ignoreConfigSpec); diff != "" {
		t.Errorf("PublishTopology(strict-topology) mismatch (-want +got):\n%s", diff)
	}

	router := storedNode(t, config.Spec, "router")
	if vlan := specInterface(t, router, "eth0")["vlan"]; vlan != "EXP" {
		t.Errorf("PublishTopology(strict-topology) router eth0 vlan = %v, want EXP: %s", vlan, asJSON(t, router))
	}

	export, err := doc.ExportTopologyConfig("strict-topology")
	if err != nil {
		t.Fatalf("exporting topology: %v", err)
	}

	if len(export.PublishBlockers) != 0 {
		t.Errorf("ExportTopologyConfig(strict-topology) PublishBlockers = %v, want none", export.PublishBlockers)
	}

	if diff := cmp.Diff(config, export.Config); diff != "" {
		t.Errorf("ExportTopologyConfig(strict-topology) config mismatch, want the published config (-want +got):\n%s", diff)
	}
}

func TestPublishTopologyRejectsDraftDocument(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	// The draft working copy is structurally valid even though it holds an
	// interface that is not connected to any network yet.
	if err := doc.Validate(); err != nil {
		t.Fatalf("draft document is invalid: %v", err)
	}

	// Its unconnected router eth1 has no VLAN.
	const want = `interface "eth1" of device "router" has no VLAN`

	if _, _, _, err := doc.PublishTopology("draft-topology"); err == nil {
		t.Error("PublishTopology(draft-topology) = nil error, want the draft refused")
	} else if !strings.Contains(err.Error(), "validating topology projection: "+want) {
		t.Errorf("PublishTopology(draft-topology) = %q, want it to name the interface without a VLAN: %q",
			err.Error(), want)
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

// Publishing refuses an interface with an empty VLAN, naming the device and interface, before
// minimega would. An export returns it as a blocker and refuses what phenix's schema refuses.
func TestPublishTopologyRefusesInterfaceWithoutVLAN(t *testing.T) {
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

			config, _, _, err := doc.PublishTopology("no-vlan")
			if err == nil {
				t.Fatalf("PublishTopology(no-vlan) = %s, want an error", asJSON(t, config.Spec))
			}

			var vlanErr *builder.InterfaceVLANError
			if !errors.As(err, &vlanErr) {
				t.Fatalf("PublishTopology(no-vlan) = %q, want an InterfaceVLANError", err.Error())
			}

			want := []string{
				`interface "eth0" of device "host-a" has no VLAN: connect it to a network, or type a VLAN for it`,
			}
			if diff := cmp.Diff(want, vlanErr.Problems); diff != "" {
				t.Errorf("PublishTopology(no-vlan) problems mismatch (-want +got):\n%s", diff)
			}

			export, exportErr := doc.ExportTopologyConfig("no-vlan")
			if !tt.exports {
				if export != nil {
					t.Errorf("ExportTopologyConfig(no-vlan) = %v, want nil", export)
				}

				if exportErr == nil || exportErr.Error() != err.Error() {
					t.Errorf("ExportTopologyConfig(no-vlan) error = %v, want the publish error %v", exportErr, err)
				}

				return
			}

			if exportErr != nil {
				t.Fatalf("ExportTopologyConfig returned error: %v", exportErr)
			}

			if diff := cmp.Diff([]string{err.Error()}, errorTexts(export.PublishBlockers)); diff != "" {
				t.Errorf("ExportTopologyConfig(no-vlan) PublishBlockers mismatch, want the publish error (-want +got):\n%s", diff)
			}

			if vlan := specInterface(t, storedNode(t, export.Config.Spec, "host-a"), "eth0")["vlan"]; vlan != eth0["vlan"] {
				t.Errorf("ExportTopologyConfig(no-vlan) host-a eth0 vlan = %q, want %q", vlan, eth0["vlan"])
			}
		})
	}
}

// Publishing refuses a projection the schema refuses for its VLAN-less interface or shared address
// first. An export names the VLAN only when its key is missing or null, else the schema's reason.
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
		// The router's eth0 has this address too, on network EXP.
		"shared address": {
			set: func(iface map[string]any) {
				iface["address"] = "10.0.0.1"
				iface["vlan"] = "EXP"
			},
			address: true, vlan: false,
		},
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

			config, _, _, publishRefusal := doc.PublishTopology("mixed")
			if config != nil {
				t.Errorf("PublishTopology(mixed) = %v, want nil", config)
			}

			if !named(publishRefusal) {
				t.Fatalf("PublishTopology(mixed) error = %v, want an InterfaceVLANError or InterfaceAddressError",
					publishRefusal)
			}

			export, err := doc.ExportTopologyConfig("mixed")
			if export != nil {
				t.Errorf("ExportTopologyConfig(mixed) = %v, want nil", export)
			}

			if err == nil {
				t.Fatal("ExportTopologyConfig(mixed) error = nil, want a refusal")
			}

			if tt.vlan {
				if err.Error() != publishRefusal.Error() {
					t.Errorf("ExportTopologyConfig(mixed) error = %v, want the publish error %v", err, publishRefusal)
				}

				return
			}

			if errors.As(err, &vlanErr) || errors.As(err, &addressErr) {
				t.Errorf("ExportTopologyConfig(mixed) error = %v, want neither an InterfaceVLANError nor an InterfaceAddressError", err)
			}

			if !errors.Is(err, types.ErrValidationFailed) {
				t.Errorf("ExportTopologyConfig(mixed) error = %v, want an error matching types.ErrValidationFailed", err)
			}

			if !strings.Contains(err.Error(), "/mac") {
				t.Errorf("ExportTopologyConfig(mixed) error = %v, want it to name the MAC address (/mac)", err)
			}
		})
	}
}

// Every spec interface is checked, also one with no canvas handle, and one whose name does not tell
// it apart is named by its position.
func TestPublishTopologyNamesInterfacesByPosition(t *testing.T) {
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

	err := publishTopologyError(doc, "positions")

	var vlanErr *builder.InterfaceVLANError
	if !errors.As(err, &vlanErr) {
		t.Fatalf("PublishTopology(positions) = %v, want an InterfaceVLANError", err)
	}

	const fix = " has no VLAN: connect it to a network, or type a VLAN for it"

	want := []string{
		`interface #2 of device "host-a"` + fix,
		`interface "eth1" (#3) of device "host-a"` + fix,
		`interface "eth0" (#5) of device "host-a"` + fix,
	}
	if diff := cmp.Diff(want, vlanErr.Problems); diff != "" {
		t.Errorf("PublishTopology(positions) problems mismatch (-want +got):\n%s", diff)
	}

	// Each problem is an issue at its interface, of the device it is on.
	host := nodeByHostname(t, doc, "host-a")
	issues := vlanErr.Issues()

	fields := []string{"spec.network.interfaces.1.vlan", "spec.network.interfaces.2.vlan", "spec.network.interfaces.4.vlan"}
	wantIssues := make([]builder.Issue, 0, len(fields))

	for i, field := range fields {
		wantIssues = append(wantIssues, builder.Issue{
			Code: builder.CodeInterfaceVLANMissing, Severity: builder.SeverityError, Message: want[i], NodeID: host.ID, Field: field,
		})
	}

	if diff := cmp.Diff(wantIssues, issues, cmpopts.IgnoreFields(builder.Issue{}, "Path")); diff != "" {
		t.Errorf("PublishTopology(positions) issues mismatch (-want +got):\n%s", diff)
	}

	for _, issue := range issues {
		if !strings.HasPrefix(issue.Path, "nodes[") {
			t.Errorf("PublishTopology(positions) issue %q path = %q, want one starting nodes[", issue.Message, issue.Path)
		}
	}
}

// An unconnected interface's VLAN that names no network is published as it is (phenix allocates
// VLANs by name), and an external node's interfaces need no VLAN.
func TestPublishTopologyKeepsVLANsOfUnconnectedInterfaces(t *testing.T) {
	doc, eth0 := unconnectedHost(t)
	eth0["vlan"] = "GHOST"

	config, _, _, err := doc.PublishTopology("ghost-vlan")
	if err != nil {
		t.Fatalf("publishing a VLAN that names no network: %v", err)
	}

	if vlan := specInterface(t, storedNode(t, config.Spec, "host-a"), "eth0")["vlan"]; vlan != "GHOST" {
		t.Errorf("PublishTopology(ghost-vlan) host-a eth0 vlan = %v, want GHOST", vlan)
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

	if _, _, _, err := doc.PublishTopology("external-no-vlan"); err != nil {
		t.Errorf("PublishTopology(external-no-vlan) = %v, want nil: an external node's interfaces need no VLAN", err)
	}
}

// withInterfaces returns the strict fixture with the spec interfaces of each named device replaced:
// eth0 extended with the first entry's fields, the other entries added after it.
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

// sharedAddresses returns what publishing doc says about shared interface addresses, or nil when it
// does not refuse them, and an error when its issues do not match those problems.
func sharedAddresses(t *testing.T, doc *builder.Document) ([]string, error) {
	t.Helper()

	if err := doc.Validate(); err != nil {
		t.Fatalf("the draft must stay valid: %v", err)
	}

	err := publishTopologyError(doc, "shared")

	var addressErr *builder.InterfaceAddressError
	if !errors.As(err, &addressErr) {
		return nil, nil
	}

	return addressErr.Problems, checkAddressIssues(addressErr)
}

// checkAddressIssues returns an error unless each problem of err is an issue of its kind of address, at
// an interface of a device that is not included.
func checkAddressIssues(err *builder.InterfaceAddressError) error {
	issues := err.Issues()
	if len(issues) != len(err.Problems) {
		return fmt.Errorf("issues = %+v, want one for each problem of %q", issues, err.Problems)
	}

	for i, issue := range issues {
		code := builder.CodeInterfaceIPShared
		if strings.HasPrefix(err.Problems[i], "MAC address ") {
			code = builder.CodeInterfaceMACShared
		}

		if issue.Code != code || issue.Message != err.Problems[i] || issue.NodeID == "" ||
			!strings.HasPrefix(issue.Field, "spec.network.interfaces.") {
			return fmt.Errorf("issue = %+v, want code %s at an interface of a device saying %q", issue, code, err.Problems[i])
		}
	}

	return nil
}

// Publishing refuses two interfaces on one network with one IP (compared parsed) or MAC (any case
// or separators). The fixture connects both to EXP though their specs name STALE.
func TestPublishTopologyRefusesSharedAddresses(t *testing.T) {
	const both = `interface "eth0" of device "router" and interface "eth0" of device "host-a"`

	for name, test := range map[string]struct {
		router, host map[string]any
		want         string
	}{
		"IPv4": {
			router: map[string]any{"address": "10.0.0.5", "mask": 24},
			host:   map[string]any{"address": " 10.0.0.5/16 ", "mask": 16},
			want:   `IP address 10.0.0.5 on VLAN "EXP" is used by ` + both,
		},
		"IPv6": {
			router: map[string]any{"address": "2001:db8::1"},
			host:   map[string]any{"address": "2001:DB8:0:0:0:0:0:1/64"},
			want:   `IP address 2001:db8::1 on VLAN "EXP" is used by ` + both,
		},
		"IPv4 mapped into IPv6": {
			router: map[string]any{"address": "::ffff:10.0.0.5"},
			host:   map[string]any{"address": "10.0.0.5"},
			want:   `IP address 10.0.0.5 on VLAN "EXP" is used by ` + both,
		},
		"MAC": {
			router: map[string]any{"mac": "AA-BB-CC-DD-EE-FF"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
			want:   `MAC address aa:bb:cc:dd:ee:ff on VLAN "EXP" is used by ` + both,
		},
		// The phenix schema refuses this form, but it is the same address.
		"MAC in dotted form": {
			router: map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"mac": "AABB.CCDD.EEFF"},
			want:   `MAC address aa:bb:cc:dd:ee:ff on VLAN "EXP" is used by ` + both,
		},
		// The editor trims the same characters (validate.js).
		"ASCII whitespace": {
			router: map[string]any{"address": "10.0.0.5", "mac": "aa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"address": "\t\v\f10.0.0.5\r\n", "mac": "\taa:bb:cc:dd:ee:ff\n"},
			want: `IP address 10.0.0.5 on VLAN "EXP" is used by ` + both + "; " +
				`MAC address aa:bb:cc:dd:ee:ff on VLAN "EXP" is used by ` + both,
		},
		// minirouter and Vyatta assign the address of a QinQ interface.
		"QinQ": {
			router: map[string]any{"address": "10.0.0.5", "qinq": true},
			host:   map[string]any{"address": "10.0.0.5"},
			want:   `IP address 10.0.0.5 on VLAN "EXP" is used by ` + both,
		},
		// A blank bridge and phenix both name the experiment's default
		// bridge.
		"default bridge": {
			router: map[string]any{"address": "10.0.0.5", "bridge": "phenix"},
			host:   map[string]any{"address": "10.0.0.5", "bridge": ""},
			want:   `IP address 10.0.0.5 on VLAN "EXP" is used by ` + both,
		},
		"one bridge": {
			router: map[string]any{"address": "10.0.0.5", "bridge": "lab"},
			host:   map[string]any{"address": "10.0.0.5", "bridge": "lab"},
			want:   `IP address 10.0.0.5 on VLAN "EXP" of bridge "lab" is used by ` + both,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			want := strings.Split(test.want, "; ")
			got, issuesErr := sharedAddresses(t, doc)
			if issuesErr != nil {
				t.Errorf("PublishTopology(shared) %v", issuesErr)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// An export returns a document whose interfaces share an address with that refusal as its only
// blocker, after the refusal of an interface without a VLAN when there is one.
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

			_, _, _, refusal := doc.PublishTopology("shared")
			if refusal == nil {
				t.Fatal("PublishTopology(shared) = nil error, want interfaces that share an address refused")
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

			if diff := cmp.Diff(test.blockers, kinds); diff != "" {
				t.Errorf("ExportTopologyConfig(shared) PublishBlockers kinds mismatch (-want +got):\n%s\nblockers: %v",
					diff, export.PublishBlockers)
			}

			if len(export.PublishBlockers) == 0 {
				return
			}

			// Publishing refuses the document with the first.
			if got := export.PublishBlockers[0].Error(); got != refusal.Error() {
				t.Errorf("ExportTopologyConfig(shared) first blocker = %q, want the publish error %q", got, refusal)
			}

			const wantSuffix = `IP address 10.0.0.5 on VLAN "EXP" is used by interface "eth0" of device "router" ` +
				`and interface "eth0" of device "host-a"`

			if got := export.PublishBlockers[len(export.PublishBlockers)-1].Error(); !strings.HasSuffix(got, wantSuffix) {
				t.Errorf("ExportTopologyConfig(shared) last blocker = %q, want it to end with %q", got, wantSuffix)
			}
		})
	}
}

// Interfaces of one device are compared too, and past the two a problem
// names, the others are counted.
func TestPublishTopologyRefusesAddressesOfOneDevice(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"host-a": {
			{"mac": "00:00:00:00:00:01"},
			{"name": "eth1", "vlan": "EXP", "proto": "static", "address": "10.0.0.2", "mac": "00-00-00-00-00-01"},
			{"name": "eth1", "vlan": "EXP", "mac": "00:00:00:00:00:01"},
		},
	})

	want := []string{
		`IP address 10.0.0.2 on VLAN "EXP" is used by interface "eth0" of device "host-a" and interface "eth1" (#2) of device "host-a"`,
		`MAC address 00:00:00:00:00:01 on VLAN "EXP" is used by interface "eth0" of device "host-a", ` +
			`interface "eth1" (#2) of device "host-a" and 1 more interface`,
	}

	got, issuesErr := sharedAddresses(t, doc)
	if issuesErr != nil {
		t.Errorf("PublishTopology(shared) %v", issuesErr)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
	}
}

// Interfaces without an address of their own (DHCP, manual, no MAC) and shared masks or gateways do
// not clash. Only ASCII whitespace is trimmed, as validate.js trims it.
func TestPublishTopologySkipsAddressesNotAssigned(t *testing.T) {
	for name, test := range map[string]struct {
		router, host map[string]any
	}{
		"DHCP":                {router: map[string]any{"proto": "dhcp", "address": "10.0.0.2"}, host: nil},
		"manual":              {router: map[string]any{"proto": "manual", "address": "10.0.0.2", "mask": 24}, host: nil},
		"blank address":       {router: map[string]any{"address": " "}, host: map[string]any{"address": " "}},
		"blank MAC":           {router: map[string]any{"mac": ""}, host: map[string]any{"mac": ""}},
		"unreadable":          {router: map[string]any{"address": "10.0.0.256"}, host: map[string]any{"address": "10.0.0.256"}},
		"mask and gateway":    {router: map[string]any{"gateway": "10.0.0.254"}, host: map[string]any{"gateway": "10.0.0.254"}},
		"byte order mark":     {router: map[string]any{"address": "\ufeff10.0.0.2"}, host: nil},
		"next line character": {router: map[string]any{"address": "10.0.0.2\u0085"}, host: nil},
		"no-break space":      {router: map[string]any{"address": "\u00a010.0.0.2"}, host: nil},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			if _, _, _, err := doc.PublishTopology("unshared"); err != nil {
				t.Errorf("PublishTopology(unshared) = %v, want nil", err)
			}
		})
	}
}

// The phenix schema refuses these interfaces, but not for their shared address: a proto is read in
// any case and trimmed of ASCII whitespace, and a MAC in other whitespace does not parse.
func TestPublishTopologySkipsAddressesNotAssignedAsTyped(t *testing.T) {
	for name, test := range map[string]struct {
		router, host map[string]any
	}{
		"DHCP in any case": {router: map[string]any{"proto": " DHCP\t", "address": "10.0.0.2"}, host: nil},
		"manual in any case": {
			router: map[string]any{"proto": "\tManual ", "address": "10.0.0.2"},
			host:   nil,
		},
		"MAC after a byte order mark": {
			router: map[string]any{"mac": "\ufeffaa:bb:cc:dd:ee:ff"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
		},
		"MAC before a next line character": {
			router: map[string]any{"mac": "aa:bb:cc:dd:ee:ff\u0085"},
			host:   map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := withInterfaces(t, map[string][]map[string]any{
				"router": {test.router},
				"host-a": {test.host},
			})

			got, issuesErr := sharedAddresses(t, doc)
			if issuesErr != nil {
				t.Errorf("PublishTopology(shared) %v", issuesErr)
			}

			if got != nil {
				t.Errorf("PublishTopology(shared) problems = %q, want none", got)
			}
		})
	}
}

// phenix does not start an external device, and its schema has no MAC, so its
// MAC does not count. Its IP address, which the real device uses, does.
func TestPublishTopologyComparesExternalDevicesByIP(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"router": {{"mac": "aa:bb:cc:dd:ee:ff"}},
		"host-a": {{"mac": "AA:BB:CC:DD:EE:FF"}},
	})
	host := nodeByHostname(t, doc, "host-a")
	host.Device.Spec["external"] = true
	host.Device.Spec["type"] = "HIL"

	if _, _, _, err := doc.PublishTopology("external"); err != nil {
		t.Errorf("PublishTopology(external) = %v, want nil: an external device's MAC does not count", err)
	}

	network, _ := host.Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)
	eth0, _ := ifaces[0].(map[string]any)
	eth0["address"] = "10.0.0.1"

	want := []string{
		`IP address 10.0.0.1 on VLAN "EXP" is used by interface "eth0" of device "router" and interface "eth0" of device "host-a"`,
	}

	got, issuesErr := sharedAddresses(t, doc)
	if issuesErr != nil {
		t.Errorf("PublishTopology(shared) %v", issuesErr)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
	}
}

// The addresses of included devices count, as phenix merges them, but one only included devices
// share is their topology's to fix.
func TestPublishTopologyComparesIncludedDevices(t *testing.T) {
	doc := withInterfaces(t, map[string][]map[string]any{
		"host-a": {{"address": "10.0.0.1"}},
	})
	doc.Source.IncludeTopologies = []string{"shared"}
	nodeByHostname(t, doc, "host-a").Device.IncludedFrom = "shared"

	want := []string{
		`IP address 10.0.0.1 on VLAN "EXP" is used by interface "eth0" of device "router" and interface "eth0" of device "host-a"`,
	}

	got, issuesErr := sharedAddresses(t, doc)
	if issuesErr != nil {
		t.Errorf("PublishTopology(shared) %v", issuesErr)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
	}

	nodeByHostname(t, doc, "router").Device.IncludedFrom = "shared"

	got, issuesErr = sharedAddresses(t, doc)
	if issuesErr != nil {
		t.Errorf("PublishTopology(shared) %v", issuesErr)
	}

	if got != nil {
		t.Errorf("PublishTopology(shared) problems = %q, want none among included devices", got)
	}
}

// unconnectedPair returns the strict fixture without connections, the eth0 of the router and of
// host-a extended with the given fields, so each is on the VLAN its spec names.
func unconnectedPair(t *testing.T, router, host map[string]any) *builder.Document {
	t.Helper()

	doc := withInterfaces(t, map[string][]map[string]any{"router": {router}, "host-a": {host}})
	doc.Edges = []builder.Edge{}

	return doc
}

// An address clashes only on one network: the VLAN and bridge an interface is published on after
// the diagram's connections, since isolated networks often reuse private addresses.
func TestPublishTopologyComparesAddressesOnOneNetwork(t *testing.T) {
	const both = `interface "eth0" of device "router" and interface "eth0" of device "host-a"`

	static := func(vlan, bridge string) map[string]any {
		return map[string]any{"vlan": vlan, "bridge": bridge, "proto": "static", "address": "10.0.0.1", "mask": 24}
	}
	mac := func(vlan string) map[string]any {
		return map[string]any{"vlan": vlan, "mac": "00:00:00:00:00:01"}
	}

	for name, doc := range map[string]*builder.Document{
		"two VLANs":              unconnectedPair(t, static("ISOLATED-1", ""), static("ISOLATED-2", "")),
		"a VLAN in another case": unconnectedPair(t, static("ISOLATED-1", ""), static("isolated-1", "")),
		"two bridges":            unconnectedPair(t, static("EXP", "lab-a"), static("EXP", "lab-b")),
		"a MAC on two VLANs":     unconnectedPair(t, mac("ISOLATED-1"), mac("ISOLATED-2")),
	} {
		t.Run(name, func(t *testing.T) {
			blockers, err := doc.PublishBlockers("isolated")
			if err != nil || len(blockers) != 0 {
				t.Errorf("PublishBlockers(isolated) = %v, %v, want none", blockers, err)
			}

			if _, _, _, err := doc.PublishTopology("isolated"); err != nil {
				t.Errorf("PublishTopology(isolated) = %v, want nil", err)
			}
		})
	}

	for name, test := range map[string]struct {
		doc  *builder.Document
		want string
	}{
		"one VLAN": {
			doc:  unconnectedPair(t, static("ISOLATED-1", ""), static(" ISOLATED-1 ", "phenix")),
			want: `IP address 10.0.0.1 on VLAN "ISOLATED-1" is used by ` + both,
		},
		"a MAC on one VLAN": {
			doc:  unconnectedPair(t, mac("ISOLATED-1"), mac("ISOLATED-1")),
			want: `MAC address 00:00:00:00:00:01 on VLAN "ISOLATED-1" is used by ` + both,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, issuesErr := sharedAddresses(t, test.doc)
			if issuesErr != nil {
				t.Errorf("PublishTopology(shared) %v", issuesErr)
			}

			if diff := cmp.Diff([]string{test.want}, got); diff != "" {
				t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The router's eth0 is connected to EXP though its spec names STALE, so host-a's disconnected eth0
// shares its address only on EXP.
func TestPublishTopologyComparesAddressesAfterConnections(t *testing.T) {
	for vlan, want := range map[string][]string{
		"STALE": nil,
		"EXP": {
			`IP address 10.0.0.1 on VLAN "EXP" is used by interface "eth0" of device "router" ` +
				`and interface "eth0" of device "host-a"`,
		},
	} {
		t.Run(vlan, func(t *testing.T) {
			doc, eth0 := unconnectedHost(t)
			eth0["vlan"] = vlan
			eth0["address"] = "10.0.0.1"

			got, issuesErr := sharedAddresses(t, doc)
			if issuesErr != nil {
				t.Errorf("PublishTopology(shared) %v", issuesErr)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Identifiers of the network ISOLATED-2 and of its switch, which
// connectedOnTwoNetworks adds to the strict fixture.
const (
	idNetIsolated2 = "2c4e6a8b-1d3f-5a7c-9e0b-2d4f6a8c0e1a" // net-isolated-2
	idSwIsolated2  = "3d5f7b9c-2e4a-5b8d-a01c-3e5a7b9d1f2b" // sw-isolated-2
)

// connectHost connects host-a's eth0, by its edge in the strict fixture, to
// the switch hub of network.
func connectHost(t *testing.T, doc *builder.Document, hub, network string) {
	t.Helper()

	for index := range doc.Edges {
		if doc.Edges[index].ID == idEHostEth0 {
			doc.Edges[index].SourceNodeID = hub
			doc.Edges[index].NetworkID = network

			return
		}
	}

	t.Fatalf("the document has no edge %s", idEHostEth0)
}

// connectedOnTwoNetworks returns the strict fixture with EXP renamed ISOLATED-1 for the router's
// eth0 and host-a's eth0 on a new ISOLATED-2, both extended with fields. Specs still name STALE.
func connectedOnTwoNetworks(t *testing.T, fields map[string]any) *builder.Document {
	t.Helper()

	const first, second = "ISOLATED-1", "ISOLATED-2"

	doc := withInterfaces(t, map[string][]map[string]any{"router": {fields}, "host-a": {fields}})
	doc.Networks[0].Name = first
	doc.NodeByID(idSwExp).Label = first
	doc.Networks = append(doc.Networks, builder.Network{ID: idNetIsolated2, Name: second})
	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       idSwIsolated2,
		Kind:     builder.NodeKindSwitch,
		Label:    second,
		Position: builder.Position{X: 480, Y: 260},
		Switch:   &builder.Switch{NetworkID: idNetIsolated2},
	})
	connectHost(t, doc, idSwIsolated2, idNetIsolated2)

	return doc
}

// Devices connected to switches of two networks may share an IP or MAC address though their specs
// name one VLAN. On one network they may not, and publishing names that network.
func TestPublishTopologyComparesConnectedAddressesOnTheirNetworks(t *testing.T) {
	const both = `interface "eth0" of device "router" and interface "eth0" of device "host-a"`

	for name, test := range map[string]struct {
		fields map[string]any
		want   string
	}{
		"IP": {
			fields: map[string]any{"proto": "static", "address": "10.0.0.1", "mask": 24},
			want:   `IP address 10.0.0.1 on VLAN "ISOLATED-1" is used by ` + both,
		},
		"MAC": {
			fields: map[string]any{"mac": "00:00:00:00:00:01"},
			want:   `MAC address 00:00:00:00:00:01 on VLAN "ISOLATED-1" is used by ` + both,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := connectedOnTwoNetworks(t, test.fields)

			blockers, err := doc.PublishBlockers("connected")
			if err != nil || len(blockers) != 0 {
				t.Errorf("PublishBlockers(connected) = %v, %v, want none", blockers, err)
			}

			if _, _, _, err := doc.PublishTopology("connected"); err != nil {
				t.Errorf("PublishTopology(connected) = %v, want nil", err)
			}

			// host-a's eth0 connected to the switch of ISOLATED-1 too.
			connectHost(t, doc, idSwExp, idNetExp)

			blockers, err = doc.PublishBlockers("connected")

			var addressErr *builder.InterfaceAddressError

			switch {
			case err != nil:
				t.Errorf("PublishBlockers(connected) returned error: %v", err)
			case len(blockers) != 1 || !errors.As(blockers[0], &addressErr):
				t.Errorf("PublishBlockers(connected) = %v, want one InterfaceAddressError", blockers)
			default:
				if diff := cmp.Diff([]string{test.want}, addressErr.Problems); diff != "" {
					t.Errorf("PublishBlockers(connected) problems mismatch (-want +got):\n%s", diff)
				}
			}

			got, issuesErr := sharedAddresses(t, doc)
			if issuesErr != nil {
				t.Errorf("PublishTopology(shared) %v", issuesErr)
			}

			if diff := cmp.Diff([]string{test.want}, got); diff != "" {
				t.Errorf("PublishTopology(shared) problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Interfaces without a VLAN, which publishing refuses unless their device is
// external, are compared with each other, and with no interface on a VLAN.
func TestPublishBlockersCompareAddressesWithoutVLAN(t *testing.T) {
	for name, test := range map[string]struct {
		host map[string]any
		want []string
	}{
		"both without a VLAN": {
			host: map[string]any{"vlan": " ", "address": "10.0.0.1"},
			want: []string{
				`IP address 10.0.0.1 without a VLAN is used by interface "eth0" of device "router" ` +
					`and interface "eth0" of device "host-a"`,
			},
		},
		"one on a VLAN": {host: map[string]any{"vlan": "EXP", "address": "10.0.0.1"}, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			doc := unconnectedPair(t, map[string]any{"vlan": ""}, test.host)

			blockers, err := doc.PublishBlockers("no-vlan")
			if err != nil {
				t.Fatalf("PublishBlockers: %v", err)
			}

			var got []string

			for _, blocker := range blockers {
				var addressErr *builder.InterfaceAddressError
				if errors.As(blocker, &addressErr) {
					got = addressErr.Problems
				}
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("PublishBlockers(no-vlan) problems mismatch (-want +got):\n%s", diff)
			}
		})
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
	if len(nodes) != 1 {
		t.Errorf("ToTopology() nodes = %s, want only the document's own router", asJSON(t, nodes))
	}

	specNode(t, topology, "router")

	if diff := cmp.Diff([]string{"shared"}, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch, want the reference that brings host-a back (-want +got):\n%s",
			diff)
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

// checkHostnameRefusal returns an error unless the refusal's issue has the code of phenix's reason, at
// the device's hostname, and says what the refusal says.
func checkHostnameRefusal(err error, code builder.Code) error {
	var refusal *builder.NodeHostnameError
	if !errors.As(err, &refusal) {
		return nil
	}

	issues := refusal.Issues()
	if len(issues) != 1 || len(refusal.Problems) == 0 || issues[0].Code != code || issues[0].NodeID == "" ||
		issues[0].Severity != builder.SeverityError || !strings.HasSuffix(issues[0].Path, ".device.hostname") ||
		issues[0].Message != refusal.Problems[0] {
		return fmt.Errorf("refusal issues = %+v, want one of code %s at the device's hostname", issues, code)
	}

	return nil
}

// checkHostnameWarning returns an error unless the hostname warnings are none (start "") or one
// starting with start, of code and severity warning, at the device's hostname.
func checkHostnameWarning(warnings []builder.Issue, start string, code builder.Code) error {
	var found []builder.Issue

	for _, warning := range warnings {
		if strings.HasPrefix(warning.Message, "hostname '") {
			found = append(found, warning)
		}
	}

	if start == "" {
		if len(found) != 0 {
			return fmt.Errorf("hostname warnings = %+v, want none", found)
		}

		return nil
	}

	if len(found) != 1 || !strings.HasPrefix(found[0].Message, start) || found[0].Code != code ||
		found[0].Severity != builder.SeverityWarning || !strings.HasSuffix(found[0].Path, ".device.hostname") {
		return fmt.Errorf("hostname warnings = %+v, want one of code %s starting %q", found, code, start)
	}

	return nil
}

// Publishing refuses a hostname phenix refuses, which a draft keeps. An export returns it as a
// blocker, or is refused as the schema refuses it. A hostname phenix only warns about publishes.
func TestPublishTopologyChecksHostnames(t *testing.T) {
	for name, tt := range map[string]struct {
		hostname, osType string
		// refused starts publishing's reason, or is "" when it publishes.
		refused string
		// exports is whether an export returns the topology.
		exports bool
		// warning starts the warning phenix logs, or is "" for none.
		warning string
		// code is the code of the refusal, or else of the warning.
		code builder.Code
	}{
		"all": {
			hostname: "all", osType: "linux", refused: "hostname 'all' is reserved", exports: true, warning: "",
			code: builder.CodeNodeHostnameReserved,
		},
		"digits": {
			hostname: "42", osType: "linux", refused: "hostname '42' is all digits", exports: true, warning: "",
			code: builder.CodeNodeHostnameNumeric,
		},
		"phenix on Windows": {
			hostname: "phenix", osType: "windows", refused: "hostname 'phenix' can't be used for a Windows node",
			exports: true, warning: "", code: builder.CodeNodeHostnameWindows,
		},
		"one character": {
			hostname: "a", osType: "linux", refused: "hostname 'a' is 1 character long", exports: false, warning: "",
			code: builder.CodeNodeHostnameShort,
		},
		"all in another case": {
			hostname: "All", osType: "linux", refused: "", exports: true,
			warning: "hostname 'All' differs from the reserved name 'all' only by case",
			code:    builder.CodeNodeHostnameReservedCase,
		},
		"phenix": {
			hostname: "Phenix", osType: "linux", refused: "", exports: true, warning: "hostname 'Phenix' matches 'phenix'",
			code: builder.CodeNodeHostnamePhenix,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := renamedHost(t, tt.hostname, tt.osType)

			if err := doc.Validate(); err != nil {
				t.Fatalf("the draft must stay valid: %v", err)
			}

			config, _, warnings, err := doc.PublishTopology("hostnames")

			var hostnameErr *builder.NodeHostnameError

			switch {
			case tt.refused == "" && err != nil:
				t.Errorf("PublishTopology(hostnames) = %v, want nil", err)
			case tt.refused != "" && !errors.As(err, &hostnameErr):
				t.Fatalf("PublishTopology(hostnames) = %v, want a NodeHostnameError starting %q", err, tt.refused)
			case tt.refused != "":
				if config != nil {
					t.Errorf("PublishTopology(hostnames) = %v, want nil", config)
				}

				if len(hostnameErr.Problems) != 1 || !strings.HasPrefix(hostnameErr.Problems[0], tt.refused) {
					t.Errorf("PublishTopology(hostnames) problems = %q, want one starting %q", hostnameErr.Problems, tt.refused)
				}
			}

			if err := checkHostnameRefusal(err, tt.code); err != nil {
				t.Errorf("PublishTopology(hostnames) %v", err)
			}

			if err := checkHostnameWarning(warnings, tt.warning, tt.code); err != nil {
				t.Errorf("PublishTopology(hostnames) %v", err)
			}

			export, exportErr := doc.ExportTopologyConfig("hostnames")
			if !tt.exports {
				if export != nil {
					t.Errorf("ExportTopologyConfig(hostnames) = %v, want nil", export)
				}

				if exportErr == nil || err == nil || exportErr.Error() != err.Error() {
					t.Errorf("ExportTopologyConfig(hostnames) error = %v, want the publish error %v", exportErr, err)
				}

				return
			}

			if exportErr != nil {
				t.Fatalf("ExportTopologyConfig returned error: %v", exportErr)
			}

			if diff := cmp.Diff(warnings, export.Warnings); diff != "" {
				t.Errorf("ExportTopologyConfig(hostnames) warnings mismatch, want the publish warnings (-want +got):\n%s", diff)
			}

			var wantBlockers []string
			if tt.refused != "" {
				wantBlockers = []string{err.Error()}
			}

			if diff := cmp.Diff(wantBlockers, errorTexts(export.PublishBlockers)); diff != "" {
				t.Errorf("ExportTopologyConfig(hostnames) PublishBlockers mismatch, want the publish error (-want +got):\n%s", diff)
			}
		})
	}
}

// Publishing names interfaces without a VLAN before a refused hostname, and an export reports both
// in that order. An external device's hostname is not checked.
func TestPublishTopologyChecksHostnamesAfterInterfaces(t *testing.T) {
	doc := renamedHost(t, "all", "linux")

	network, _ := nodeByHostname(t, doc, "all").Device.Spec["network"].(map[string]any)
	ifaces, _ := network["interfaces"].([]any)
	network["interfaces"] = append(ifaces, map[string]any{"name": "eth1", "type": "ethernet", "proto": "dhcp", "vlan": ""})

	refusal := publishTopologyError(doc, "ordered")

	var vlanErr *builder.InterfaceVLANError
	if !errors.As(refusal, &vlanErr) {
		t.Fatalf("PublishTopology(ordered) error = %v, want an InterfaceVLANError", refusal)
	}

	export, err := doc.ExportTopologyConfig("ordered")
	if err != nil {
		t.Fatalf("ExportTopologyConfig returned error: %v", err)
	}

	var hostnameErr *builder.NodeHostnameError

	switch blockers := export.PublishBlockers; {
	case len(blockers) != 2:
		t.Errorf("ExportTopologyConfig(ordered) PublishBlockers = %v, want the VLAN, then the hostname", blockers)
	default:
		if blockers[0].Error() != refusal.Error() {
			t.Errorf("ExportTopologyConfig(ordered) first blocker = %v, want the publish error %v", blockers[0], refusal)
		}

		if !errors.As(blockers[1], &hostnameErr) {
			t.Errorf("ExportTopologyConfig(ordered) second blocker = %v, want a NodeHostnameError", blockers[1])
		}
	}

	external := renamedHost(t, "42", "linux")
	nodeByHostname(t, external, "42").Device.Spec["external"] = true

	if _, _, _, err := external.PublishTopology("external"); errors.As(err, &hostnameErr) {
		t.Errorf("PublishTopology(external) = %v, want an external device's hostname not checked", err)
	}
}

func TestTopologyNameMatchesPublishDialog(t *testing.T) {
	t.Parallel()

	// The cases of configName in src/js/test/builder/publish.test.js, and a
	// name that leaves none.
	for _, test := range []struct{ desc, name, want string }{
		{desc: "untitled", name: "Untitled topology", want: "Untitled-topology"},
		{desc: "padded with punctuation", name: "  lab #2 (copy) ", want: "lab-2-copy"},
		{desc: "underscore at sign and dot", name: "core_net@site.v2", want: "core_net@site.v2"},
		{desc: "padded with parentheses", name: " Lab topology (2) ", want: "Lab-topology-2"},
		{desc: "two words", name: "Pump station", want: "Pump-station"},
		{desc: "empty", name: "", want: builder.DefaultTopologyName},
		{desc: "punctuation only", name: "!!", want: builder.DefaultTopologyName},
	} {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			if got := builder.TopologyName(test.name); got != test.want {
				t.Errorf("TopologyName(%q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}

// TestToTopologyOmitsVisualNodes checks that shapes, icons and lines, one of them in a group, leave
// the published topology unchanged.
func TestToTopologyOmitsVisualNodes(t *testing.T) {
	plain := loadDocumentFixture(t, "document.json")

	want, _, err := plain.ToTopologyConfig("visual")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	drawn := loadDocumentFixture(t, "document.json")
	drawn.Nodes = append(drawn.Nodes, visualNodes()...)

	if err := drawn.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	got, _, err := drawn.ToTopologyConfig("visual")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, want), jsonValue(t, got)); diff != "" {
		t.Errorf("ToTopologyConfig(visual) with visual nodes mismatch (-want +got):\n%s", diff)
	}

	encoded := asJSON(t, got)

	for _, text := range []string{"DMZ", "Internet", "uplink", "rectangle", "#c0392b", `"points"`} {
		if strings.Contains(encoded, text) {
			t.Errorf("ToTopologyConfig(visual) = %s, want no %s in it", encoded, text)
		}
	}
}

// TestToTopologyOmitsPresentationFields checks that the presentation fields, which belong to the
// diagram alone, leave the published topology unchanged.
func TestToTopologyOmitsPresentationFields(t *testing.T) {
	decorated := decoratedDocument(t)

	plain := loadDocumentFixture(t, "document.json")
	plain.Source.Kind = decorated.Source.Kind
	plain.Source.IncludeTopologies = decorated.Source.IncludeTopologies

	want, _, err := plain.ToTopologyConfig("decorated")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	got, _, err := decorated.ToTopologyConfig("decorated")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, want), jsonValue(t, got)); diff != "" {
		t.Errorf("ToTopologyConfig(decorated) of a decorated document mismatch (-want +got):\n%s", diff)
	}

	encoded := asJSON(t, got)

	presentation := []string{iconFixtureName, iconFixtureData, "#2f6fbf", "dotted", "dash-dot", "PLC", "Mirror port"}

	for _, text := range append(presentation, decorationKeys...) {
		if strings.Contains(encoded, text) {
			t.Errorf("ToTopologyConfig(decorated) = %s, want no %s in it", encoded, text)
		}
	}

	// The includes themselves are written back, all of them.
	if diff := cmp.Diff([]string{"shared", "plant"}, got.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopologyConfig(decorated) includeTopologies mismatch (-want +got):\n%s", diff)
	}
}
