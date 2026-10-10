package builder_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/types"
	"phenix/types/builder"
)

// loadConfig reads a store.Config fixture from testdata.
func loadConfig(t *testing.T, name string) store.Config {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	var config store.Config

	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decoding fixture %s: %v", name, err)
	}

	return config
}

// storedExperiment builds an experiment from the topology.json fixture the
// way api/experiment.Create stores one, which hand-written fixtures do not
// show: the topology decoded (and upgraded) by types.DecodeTopologyFromConfig,
// a v2 scenario by types.DecodeScenarioFromConfig, the spec decoded by
// types.DecodeExperimentFromConfig and struct mapped by
// structs.MapDefaultCase, which writes every unset pointer as null and every
// unset field as its zero value, then round tripped through JSON as the store
// round trips it.
func storedExperiment(t *testing.T) store.Config {
	t.Helper()

	topologyConfig := loadConfig(t, "topology.json")

	topology, err := types.DecodeTopologyFromConfig(topologyConfig)
	if err != nil {
		t.Fatalf("DecodeTopologyFromConfig: %v", err)
	}

	scenario, err := types.DecodeScenarioFromConfig(store.Config{
		Version: "phenix.sandia.gov/v2",
		Kind:    "Scenario",
		Metadata: store.ConfigMetadata{
			Name:        "builder-scenario",
			Annotations: map[string]string{"topology": topologyConfig.Metadata.Name},
		},
		Spec: map[string]any{"apps": []any{map[string]any{
			"name":  "ntp",
			"hosts": []any{map[string]any{"hostname": "router", "metadata": map[string]any{"server": true}}},
		}}},
	})
	if err != nil {
		t.Fatalf("DecodeScenarioFromConfig: %v", err)
	}

	config := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "stored-experiment",
			Annotations: map[string]string{
				"topology": topologyConfig.Metadata.Name,
				"scenario": "builder-scenario",
			},
		},
		Spec: map[string]any{
			"experimentName": "stored-experiment",
			"baseDir":        "",
			"deployMode":     "",
			"defaultBridge":  "",
			"topology":       topology,
			"scenario":       scenario,
		},
	}

	experiment, err := types.DecodeExperimentFromConfig(config)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig: %v", err)
	}

	if err := experiment.Spec.SetVLANAlias("EXP", 101, false); err != nil {
		t.Fatalf("SetVLANAlias: %v", err)
	}

	config.Spec = structs.MapDefaultCase(experiment.Spec, structs.CASESNAKE)

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encoding experiment: %v", err)
	}

	var stored store.Config

	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	return stored
}

// loadDocumentFixture reads and strictly decodes a document fixture.
func loadDocumentFixture(t *testing.T, name string) *builder.Document {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	doc, err := builder.Decode(data)
	if err != nil {
		t.Fatalf("decoding fixture %s: %v", name, err)
	}

	return doc
}

// documentFromConfig imports a fixture config, failing the test on error.
func documentFromConfig(t *testing.T, config store.Config) (*builder.Document, []string) {
	t.Helper()

	doc, warnings, err := builder.FromConfig(config)
	if err != nil {
		t.Fatalf("FromConfig(%s/%s): %v", config.Kind, config.Metadata.Name, err)
	}

	return doc, warnings
}

// asJSON renders a value as indented JSON for readable test failures.
func asJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshaling value: %v", err)
	}

	return string(data)
}

// nodeByHostname returns the device node with the given hostname.
func nodeByHostname(t *testing.T, doc *builder.Document, hostname string) *builder.Node {
	t.Helper()

	node := doc.FindDevice(hostname)
	if node == nil {
		t.Fatalf("document has no device %q", hostname)
	}

	return node
}

// specNode returns the mapped topology node spec of a hostname.
func specNode(t *testing.T, topology *builder.Topology, hostname string) map[string]any {
	t.Helper()

	nodes, ok := topology.Spec["nodes"].([]any)
	if !ok {
		t.Fatalf("topology spec has no nodes list: %v", topology.Spec)
	}

	for _, entry := range nodes {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		general, ok := node["general"].(map[string]any)
		if !ok {
			continue
		}

		if general["hostname"] == hostname {
			return node
		}
	}

	t.Fatalf("topology spec has no node %q: %s", hostname, asJSON(t, topology.Spec))

	return nil
}

// specInterface returns the named interface of a mapped topology node spec.
func specInterface(t *testing.T, node map[string]any, name string) map[string]any {
	t.Helper()

	network, ok := node["network"].(map[string]any)
	if !ok {
		t.Fatalf("node has no network: %s", asJSON(t, node))
	}

	ifaces, ok := network["interfaces"].([]any)
	if !ok {
		t.Fatalf("node has no interfaces: %s", asJSON(t, node))
	}

	for _, entry := range ifaces {
		iface, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		if iface["name"] == name {
			return iface
		}
	}

	t.Fatalf("node has no interface %q: %s", name, asJSON(t, node))

	return nil
}

// containsSubstring reports whether any element of values contains substr.
func containsSubstring(values []string, substr string) bool {
	for _, value := range values {
		if strings.Contains(value, substr) {
			return true
		}
	}

	return false
}

// storedNode returns the node with the given hostname from a raw config spec.
func storedNode(t *testing.T, spec map[string]any, hostname string) map[string]any {
	t.Helper()

	nodes, ok := spec["nodes"].([]any)
	if !ok {
		t.Fatalf("spec has no nodes list: %s", asJSON(t, spec))
	}

	for _, entry := range nodes {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		general, ok := node["general"].(map[string]any)
		if !ok {
			continue
		}

		if general["hostname"] == hostname {
			return node
		}
	}

	t.Fatalf("spec has no node %q", hostname)

	return nil
}

// Identifiers used by the testdata fixtures and by tests that add nodes.
const (
	idDevHost     = "880cc913-ef40-526f-b079-8c94e60bf767" // dev-host
	idDevRouter   = "4113dfc0-f4be-57f9-8115-6cecd1f1aea3" // dev-router
	idDocFixture  = "49d876d1-571a-5b5c-91b7-aadf5bb5209c" // doc-fixture
	idDocStrict   = "f8303221-fe85-5254-b687-e3c4f3d6aa99" // doc-strict
	idEDuplicate  = "81a19d1a-0688-5b9c-a0ec-758cf5a7ac20" // e-duplicate
	idEHostEth0   = "6cdcd274-ceeb-511f-a4b1-c5b06391999c" // e-host-eth0
	idERouterEth0 = "503c4f01-e7e0-5a2e-a07d-26e2776fea8f" // e-router-eth0
	idERouterEth9 = "f114edc6-2010-5ba7-b042-c6804f3c885b" // e-router-eth9
	idGrpFree     = "b9a18a27-6fce-5884-90bf-3ad537a28c33" // grp-free
	idGrpInner    = "c551cba0-706b-51bf-aadb-84e4cc6d106d" // grp-inner
	idGrpRack     = "c60601dd-6d6b-56c7-97e5-149caf5ed993" // grp-rack
	idHHostEth0   = "532fb184-8d0a-572f-8e69-a73688bbce90" // h-host-eth0
	idHRouterEth0 = "7f010dcd-e23d-514c-9206-3609df999026" // h-router-eth0
	idHRouterEth1 = "6727e6af-98cb-55b8-a50b-793e9c0da86e" // h-router-eth1
	idHRouterEth9 = "1a5d3fbf-70ee-5e79-919c-718e609f6826" // h-router-eth9
	idNetExp      = "a8af611d-c68f-54e0-94a5-9176cea64e44" // net-exp
	idNetNope     = "8954729f-99df-5e72-95d7-b88d3a5f6a24" // net-nope
	idNetUnused   = "6fd71ba6-69b9-5ff7-9dff-e76b03c43101" // net-unused
	idNote1       = "874e945f-4f75-57c7-9533-0d8ffd6ec32b" // note-1
	idNoteFree    = "a74971fd-94b9-545e-ae5d-353d4ec5236e" // note-free
	idSwExp       = "dee6bc70-8103-581e-8b31-6c95cda798a0" // sw-exp
	idShapeDMZ    = "3f2b6c1e-8d4a-5b7e-9c0f-1a2b3c4d5e6f" // shape-dmz
	idIconNet     = "5a6b7c8d-9e0f-5a1b-8c2d-3e4f5a6b7c8d" // icon-net
	idLineUplink  = "7c8d9e0f-1a2b-5c3d-ae4f-5a6b7c8d9e0f" // line-uplink
)

// visualNodes returns a shape inside group grp-rack, an icon and a line with
// an arrowhead and a bend: one node of each kind that is drawn and never
// published.
func visualNodes() []builder.Node {
	return []builder.Node{
		{
			ID: idShapeDMZ, Kind: builder.NodeKindShape, ParentID: idGrpRack,
			Position: builder.Position{X: 0, Y: 200}, Size: &builder.Size{Width: 160, Height: 96},
			Shape: &builder.Shape{
				Shape:        "rectangle",
				Label:        "DMZ",
				FillColor:    "#eef4fb",
				OutlineColor: "#2f6fbf",
				BorderStyle:  "dashed",
			},
		},
		{
			ID: idIconNet, Kind: builder.NodeKindIcon,
			Position: builder.Position{X: 900, Y: 0}, Size: &builder.Size{Width: 64, Height: 64},
			Icon: &builder.IconNode{IconKey: "external", Label: "Internet"},
		},
		{
			ID: idLineUplink, Kind: builder.NodeKindLine,
			Position: builder.Position{X: 700, Y: 200}, Size: &builder.Size{Width: 160, Height: 80},
			Line: &builder.Line{
				Points:    []builder.Position{{X: 0, Y: 0}, {X: 0, Y: 80}, {X: 160, Y: 80}},
				Label:     "uplink",
				Color:     "#c0392b",
				LineStyle: "dotted",
				EndArrow:  true,
			},
		},
	}
}

// decorationKeys are the JSON keys of what decorates a document and is left
// out when not set: custom icons, icon sizes, colors, line and border
// styles, templates, the notes of a switch, the includes that were not
// resolved, and Purdue layers. A document that uses none of them encodes
// without any of these.
var decorationKeys = []string{ //nolint:gochecknoglobals // test fixture
	`"templates"`, `"icons"`, `"icon"`, `"iconSize"`, `"outlineColor"`, `"fillColor"`, `"borderStyle"`,
	`"lineStyle"`, `"notes"`, `"unresolvedIncludes"`, `"purdueLevel"`,
}

// decoratedDocument loads the document.json fixture and sets each of those
// fields in it: the icon size of the document, the custom icon, icon size,
// colors and Purdue layer of a device, the colors, icon size, Purdue layer
// and notes of a switch, a group's description, border, icon key, custom
// icon and icon size, the line style of a network and of an edge, the
// includes that were not resolved, a template and the icon they use.
func decoratedDocument(t *testing.T) *builder.Document {
	t.Helper()

	doc := loadDocumentFixture(t, "document.json")

	doc.IconSize = builder.IconSizeLarge
	doc.Icons = map[string]builder.Icon{iconFixtureName: {Data: iconFixtureData}}
	doc.Templates = []builder.Template{sampleTemplate()}

	router := nodeByHostname(t, doc, "router").Device
	router.Icon = iconFixtureName
	router.IconSize = builder.IconSizeSmall
	router.OutlineColor = "#2f6fbf"
	router.FillColor = "#eef4fb"
	router.PurdueLevel = "3.5"

	hub := doc.NodeByID(idSwExp).Switch
	hub.OutlineColor = "#1f7a5a"
	hub.FillColor = "#E8F5F0"
	hub.IconSize = builder.IconSizeMedium
	hub.PurdueLevel = "2"
	hub.Notes = []string{"Mirror port 24 feeds the IDS.", "Patch panel B\n\track 2"}

	rack := doc.NodeByID(idGrpRack).Group
	rack.Description = "first rack\nsecond line"
	rack.BorderStyle = "dotted"
	rack.IconKey = "container"
	rack.Icon = iconFixtureName
	rack.IconSize = builder.IconSizeMedium

	doc.Networks[0].LineStyle = "dashed"
	doc.Edges[0].LineStyle = "dash-dot"

	doc.Source.Kind = builder.SourceKindTopology
	doc.Source.IncludeTopologies = []string{"shared", "plant"}
	doc.Source.UnresolvedIncludes = []string{"plant"}

	return doc
}
