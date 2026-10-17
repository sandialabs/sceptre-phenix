package builder_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

var updateLegacyGolden = flag.Bool( //nolint:gochecknoglobals // test flag
	"update-legacy-golden",
	false,
	"rewrite the golden documents of the legacy Builder conversion in testdata/legacy",
)

// The sentences a conversion of the captured sample diagram warns with.
const (
	legacyAddedB      = "Added a switch for 1 network that had none in the legacy diagram: b."
	legacyOneLine     = "Left out 1 line that was not a network link."
	legacyDecorations = "Text and containers were kept as notes and groups. " +
		"Their colors, fonts and other formatting were not converted."
)

// legacyFile reads a file of testdata/legacy.
func legacyFile(tb testing.TB, name string) []byte {
	tb.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "legacy", name))
	if err != nil {
		tb.Fatalf("reading %s: %v", name, err)
	}

	return data
}

// decodeLegacy decodes a diagram that must be readable.
func decodeLegacy(tb testing.TB, content string) *builder.LegacyDiagram {
	tb.Helper()

	diagram, err := builder.DecodeLegacy([]byte(content))
	if err != nil {
		tb.Fatalf("DecodeLegacy returned error: %v", err)
	}

	return diagram
}

// convertLegacy converts a diagram without a topology, checks it as checkLegacyDocument does, and
// checks that its source names no config.
func convertLegacy(tb testing.TB, content, name string) (*builder.Document, []string) {
	tb.Helper()

	doc, warnings, err := builder.FromLegacy(decodeLegacy(tb, content), name)
	if err != nil {
		tb.Fatalf("FromLegacy returned error: %v", err)
	}

	checkLegacyDocument(tb, doc, warnings)

	if doc.Source.Kind != builder.SourceKindManual || doc.Source.Name != "" || doc.Source.Digest != "" {
		tb.Fatalf("source = %+v, want one that names no config", doc.Source)
	}

	return doc, warnings
}

func checkLegacyDocument(tb testing.TB, doc *builder.Document, warnings []string) {
	tb.Helper()

	if err := doc.Validate(); err != nil {
		tb.Fatalf("the converted document is not valid: %v", err)
	}

	if !slices.Equal(doc.Source.Warnings, warnings) {
		tb.Fatalf("source warnings = %q, want the returned %q", doc.Source.Warnings, warnings)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		tb.Fatalf("Encode: %v", err)
	}

	if _, err := builder.Parse(data); err != nil {
		tb.Fatalf("the encoded document does not parse: %v", err)
	}
}

// checkLegacyGolden compares the document with testdata/legacy/<name>, or
// rewrites that file when the test runs with -update-legacy-golden.
func checkLegacyGolden(t *testing.T, name string, doc *builder.Document) {
	t.Helper()

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	data = append(data, '\n')
	path := filepath.Join("testdata", "legacy", name)

	if *updateLegacyGolden {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}

		return
	}

	if want := legacyFile(t, name); !bytes.Equal(data, want) {
		t.Errorf("the document differs from %s (rewrite it with -update-legacy-golden once the change is meant):\n%s",
			name, data)
	}
}

// legacySaved is what the legacy Builder posted when it saved the sample captured in
// testdata/legacy/sample-capture.json.
type legacySaved struct {
	Name       string         `json:"name"`
	Topology   map[string]any `json:"topology"`
	BuilderXML string         `json:"builderXML"`
}

func legacySave(tb testing.TB) legacySaved {
	tb.Helper()

	var capture struct {
		XML   string `json:"xml"`
		Posts []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Body   string `json:"body"`
		} `json:"posts"`
	}

	if err := json.Unmarshal(legacyFile(tb, "sample-capture.json"), &capture); err != nil {
		tb.Fatalf("decoding the capture: %v", err)
	}

	if len(capture.Posts) != 1 || capture.Posts[0].Method != http.MethodPost || capture.Posts[0].Path != "/api/v1/builder/topologies" {
		tb.Fatalf("the capture holds %+v, want the one save of the legacy Builder", capture.Posts)
	}

	var saved legacySaved

	if err := json.Unmarshal([]byte(capture.Posts[0].Body), &saved); err != nil {
		tb.Fatalf("decoding what the legacy Builder posted: %v", err)
	}

	// sample.xml is the diagram of that save, byte for byte.
	if sample := string(legacyFile(tb, "sample.xml")); saved.BuilderXML != sample || capture.XML != sample {
		tb.Fatal("sample.xml is not the diagram the legacy Builder serialized and posted")
	}

	return saved
}

// legacySampleTopology is the topology the legacy Builder stored for the
// captured sample: the nodes it posted, and the diagram as the annotation.
func legacySampleTopology(tb testing.TB) store.Config {
	tb.Helper()

	saved := legacySave(tb)

	return legacyTopology(saved.Name, saved.BuilderXML, saved.Topology["nodes"].([]any)...)
}

// legacyTopology returns a stored Topology config with a legacy diagram.
func legacyTopology(name, diagram string, nodes ...any) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Topology",
		Metadata: store.ConfigMetadata{
			Name:        name,
			Updated:     "2026-01-02T03:04:05Z",
			Annotations: store.Annotations{builder.LegacyXMLAnnotation: diagram, "owner": "ops"},
		},
		Spec: map[string]any{"nodes": nodes},
	}
}

// legacyNode is the spec of a topology node with interfaces on the given
// VLANs.
func legacyNode(hostname string, vlans ...string) map[string]any {
	interfaces := make([]any, 0, len(vlans))
	for i, vlan := range vlans {
		interfaces = append(interfaces, map[string]any{"name": fmt.Sprintf("eth%d", i), "vlan": vlan})
	}

	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname},
		"hardware": map[string]any{"os_type": "linux", "memory": 4096},
		"network":  map[string]any{"interfaces": interfaces},
	}
}

// legacyModel wraps cells in the mxGraph model the legacy Builder wrote.
func legacyModel(cells ...string) string {
	return `<mxGraphModel grid="1"><root><mxCell id="0"/><mxCell id="1" parent="0"/>` +
		strings.Join(cells, "") + `</root></mxGraphModel>`
}

// legacyVertex is a vertex cell with the given settings (none when nil) at
// a position, 80 by 80 like a legacy icon.
func legacyVertex(id, label string, settings map[string]any, style string, x, y int) string {
	attribute := ""

	if settings != nil {
		encoded, err := json.Marshal(settings)
		if err != nil {
			panic(err)
		}

		attribute = ` schemaVars="` + html.EscapeString(string(encoded)) + `"`
	}

	return fmt.Sprintf(
		`<object label="%s"%s id="%s"><mxCell style="%s" vertex="1" parent="1">`+
			`<mxGeometry x="%d" y="%d" width="80" height="80" as="geometry"/></mxCell></object>`,
		html.EscapeString(label), attribute, id, style, x, y,
	)
}

// legacyDevice is a device cell whose settings name the hostname and the
// legacy kind.
func legacyDevice(id, hostname, kind string, x, y int, vlans ...string) string {
	settings := legacyNode(hostname, vlans...)
	settings["device"] = kind
	settings["hardware"] = map[string]any{"os_type": "linux", "memory": 1024}

	return legacyVertex(id, hostname, settings, "image;html=1;image=/"+kind+"_blue_vm.png", x, y)
}

func legacySwitch(id, vlan string, vlanID any, x, y int) string {
	return legacyVertex(id, vlan, map[string]any{"device": "switch", "name": vlan, "id": vlanID},
		"image;html=1;image=/switch_blue_vm.png", x, y)
}

func legacyLine(id, source, target, vlan string, vlanID any) string {
	encoded, err := json.Marshal(map[string]any{"device": "diagraming", "name": vlan, "id": vlanID})
	if err != nil {
		panic(err)
	}

	return fmt.Sprintf(
		`<object label="" schemaVars="%s" id="%s"><mxCell edge="1" parent="1" source="%s" target="%s">`+
			`<mxGeometry relative="1" as="geometry"/></mxCell></object>`,
		html.EscapeString(string(encoded)), id, source, target,
	)
}

func legacyPosition(t *testing.T, doc *builder.Document, hostname string) builder.Position {
	t.Helper()

	return nodeByHostname(t, doc, hostname).Position
}

// legacyNodes returns the nodes of a kind, by label (a group's title, a
// note's text).
func legacyNodes(doc *builder.Document, kind builder.NodeKind) map[string]*builder.Node {
	nodes := map[string]*builder.Node{}

	for i := range doc.Nodes {
		node := &doc.Nodes[i]
		if node.Kind != kind {
			continue
		}

		switch kind {
		case builder.NodeKindGroup:
			nodes[node.Group.Title] = node
		case builder.NodeKindNote:
			nodes[node.Note.Text] = node
		case builder.NodeKindDevice, builder.NodeKindSwitch,
			builder.NodeKindShape, builder.NodeKindIcon, builder.NodeKindLine:
			nodes[node.Label] = node
		}
	}

	return nodes
}

func legacyAlias(doc *builder.Document, network string) int {
	if found := doc.NetworkByName(network); found != nil && found.Alias != nil {
		return *found.Alias
	}

	return 0
}

// legacySwitchesOf returns the switch nodes of a network, in document order.
func legacySwitchesOf(doc *builder.Document, network string) []*builder.Node {
	var nodes []*builder.Node

	for _, node := range doc.SwitchNodes() {
		if found := doc.NetworkByName(network); found != nil && node.Switch.NetworkID == found.ID {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// legacyTargets returns the IDs of the switches the device's edges go to, by
// interface name.
func legacyTargets(t *testing.T, doc *builder.Document, hostname string) map[string]string {
	t.Helper()

	node := nodeByHostname(t, doc, hostname)
	targets := map[string]string{}

	for _, edge := range doc.Edges {
		if edge.SourceNodeID == node.ID {
			targets[node.Device.InterfaceHandle(edge.SourceHandleID).Name] = edge.TargetNodeID
		}
	}

	return targets
}

func wantLegacyWarnings(t *testing.T, got []string, want ...string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("warnings:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestLegacySample converts the diagram captured from the legacy Builder
// itself, without its topology.
func TestLegacySample(t *testing.T) {
	doc, warnings := convertLegacy(t, string(legacyFile(t, "sample.xml")), "sample")

	checkLegacyGolden(t, "sample.golden.json", doc)
	wantLegacyWarnings(t, warnings, legacyAddedB, legacyOneLine, legacyDecorations)

	// What the legacy Builder wrote when it saved: its variables resolved,
	// values kept as text, its own keys dropped, lists turned into maps.
	router := nodeByHostname(t, doc, "router-device-0").Device.Spec
	if got := asJSON(t, router["hardware"]); !strings.Contains(got, `"image": "vyos.qc2"`) ||
		!strings.Contains(got, `"vcpus": "1"`) || !strings.Contains(got, `"memory": "2048"`) {
		t.Errorf("router hardware = %s, want the variables resolved as text", got)
	}

	client := nodeByHostname(t, doc, "client-1").Device.Spec
	if !reflect.DeepEqual(client["annotations"], map[string]any{"owner": "ops"}) ||
		!reflect.DeepEqual(client["labels"], map[string]any{"zone": "corp"}) {
		t.Errorf("annotations = %v, labels = %v, want maps", client["annotations"], client["labels"])
	}

	for _, node := range doc.DeviceNodes() {
		for _, key := range []string{"device", "schema"} {
			if _, kept := node.Device.Spec[key]; kept {
				t.Errorf("%s keeps the legacy key %q", node.Device.Hostname, key)
			}
		}
	}
}

// TestLegacySampleMatchesLegacySave checks that the converted sample publishes the nodes the legacy
// Builder posted when it saved the same diagram.
func TestLegacySampleMatchesLegacySave(t *testing.T) {
	saved := legacySave(t)
	doc, _ := convertLegacy(t, saved.BuilderXML, saved.Name)

	published, _, err := doc.ToTopologyConfig(saved.Name)
	if err != nil {
		t.Fatalf("ToTopologyConfig returned error: %v", err)
	}

	// Compared as JSON, which is what both sides store.
	byHostname := func(nodes any) map[string]any {
		t.Helper()

		var list []map[string]any

		if err := json.Unmarshal([]byte(asJSON(t, nodes)), &list); err != nil {
			t.Fatalf("decoding nodes: %v", err)
		}

		found := map[string]any{}

		for _, node := range list {
			hostname, _ := node["general"].(map[string]any)["hostname"].(string)
			found[hostname] = node
		}

		if len(found) != len(list) {
			t.Fatalf("%d nodes with %d hostnames", len(list), len(found))
		}

		return found
	}

	got, want := byHostname(published.Spec["nodes"]), byHostname(saved.Topology["nodes"])
	if len(want) != 5 || !reflect.DeepEqual(got, want) {
		t.Errorf("the converted diagram publishes\n%s\nand the legacy Builder saved\n%s", asJSON(t, got), asJSON(t, want))
	}

	if len(published.Spec) != 1 {
		t.Errorf("the published spec holds %v, want only the nodes the legacy Builder saved", slices.Collect(maps.Keys(published.Spec)))
	}
}

// TestLegacySampleTopology converts the sample with the topology the legacy Builder saved for it,
// which gives the same diagram.
func TestLegacySampleTopology(t *testing.T) {
	config := legacySampleTopology(t)

	doc, warnings, err := builder.FromLegacyTopology(config)
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	checkLegacyDocument(t, doc, warnings)
	wantLegacyWarnings(t, warnings, legacyAddedB, legacyOneLine, legacyDecorations)

	// The digest covers the diagram that was read, not the spec alone.
	digest, err := builder.ImportDigest(config)
	if err != nil {
		t.Fatalf("ImportDigest returned error: %v", err)
	}

	if spec, err := builder.SourceDigest(config); err != nil || spec == digest {
		t.Fatalf("the digest of the spec alone is %q (%v), want another than %q", spec, err, digest)
	}

	source := doc.Source
	if source.Kind != builder.SourceKindTopology || source.Name != "sample" || source.Digest != digest ||
		source.UpdatedAt != config.Metadata.Updated || source.ImportedAt != "" {
		t.Errorf("source = %+v, want the topology with its digest", source)
	}

	if want := map[string]string{"owner": "ops"}; !maps.Equal(source.Annotations, want) {
		t.Errorf("source annotations = %v, want %v without the diagram", source.Annotations, want)
	}

	bare, _ := convertLegacy(t, string(legacyFile(t, "sample.xml")), "sample")
	if !reflect.DeepEqual(doc.Nodes, bare.Nodes) || !reflect.DeepEqual(doc.Networks, bare.Networks) ||
		!reflect.DeepEqual(doc.Edges, bare.Edges) || doc.Metadata.ID != bare.Metadata.ID {
		t.Error("the diagram converted with its topology differs from the diagram converted alone")
	}
}

// TestLegacyCells converts the diagrams of testdata/legacy/cells, which hold every kind of cell,
// against their golden documents.
func TestLegacyCells(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "legacy", "cells", "*.xml"))
	if err != nil || len(files) != 10 {
		t.Fatalf("found %d diagrams (%v), want 10", len(files), err)
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".xml")

		t.Run(name, func(t *testing.T) {
			doc, _ := legacyCells(t, name)

			checkLegacyGolden(t, filepath.Join("cells", name+".golden.json"), doc)
		})
	}
}

func legacyCells(t *testing.T, name string) (*builder.Document, []string) {
	t.Helper()

	return convertLegacy(t, string(legacyFile(t, filepath.Join("cells", name+".xml"))), name)
}

func TestLegacyDevices(t *testing.T) {
	doc, warnings := legacyCells(t, "devices")

	wantLegacyWarnings(t, warnings,
		`topology node at index 14 duplicates the hostname of the node at index 13 ("DUP") and was skipped`,
		"topology node at index 15 has no hostname and was skipped",
		`Renamed node "web server 1" to "web-server-1": a hostname cannot hold spaces.`,
		`Renamed node "from label" to "from-label": a hostname cannot hold spaces.`,
	)

	// The settings name the node, and the label does only when they name none.
	if node := nodeByHostname(t, doc, "edge-1"); node.Label != "edge-1" ||
		node.Device.Spec["general"].(map[string]any)["hostname"] != "edge-1" {
		t.Errorf("edge-1 = %s", asJSON(t, node))
	}

	if spec := nodeByHostname(t, doc, "from-label").Device.Spec; spec["general"].(map[string]any)["hostname"] != "from-label" {
		t.Errorf("from-label spec = %v, want the hostname written into it", spec)
	}
}

func TestLegacySwitches(t *testing.T) {
	doc, warnings := legacyCells(t, "switches")

	wantLegacyWarnings(t, warnings,
		"VLAN ID 5000 of network v5000 is not a number from 1 to 4094 and was left out.",
		"VLAN ID -1 of network vneg is not a number from 1 to 4094 and was left out.",
		"VLAN ID 1.5 of network vfrac is not a number from 1 to 4094 and was left out.",
		"Network twin has more than one VLAN ID in the legacy diagram. The first, 300, was kept.",
		"VLAN ID 101 is used by networks v101 and dupid. It was kept for v101 only.",
		"1 item has settings that could not be read and was left out: cell 23.",
		`Renamed network "my lan" to "my-lan": a network name cannot hold spaces.`,
	)

	// A switch without a VLAN name takes its label.
	if doc.NetworkByName("labelled") == nil {
		t.Errorf("networks = %s, want one named by the label of its switch", asJSON(t, doc.Networks))
	}
}

func TestLegacyLinks(t *testing.T) {
	_, warnings := legacyCells(t, "links")

	wantLegacyWarnings(t, warnings,
		"Added a switch for 2 networks that had none in the legacy diagram: p2p and quiet.",
		"Left out 3 links whose nodes have no interface on that network: beta to nowhere, beta to spare and gamma to nowhere.",
		"Left out 5 lines that were not network links.",
		legacyDecorations,
	)
}

func TestLegacyDecorations(t *testing.T) {
	_, warnings := legacyCells(t, "decorations")

	wantLegacyWarnings(t, warnings, "Left out 1 shape that had no text.", legacyDecorations)
}

func TestLegacyGroups(t *testing.T) {
	_, warnings := legacyCells(t, "groups")

	wantLegacyWarnings(t, warnings, legacyDecorations)
}

func TestLegacyStructure(t *testing.T) {
	_, warnings := legacyCells(t, "structure")

	wantLegacyWarnings(t, warnings,
		"The legacy diagram had 2 layers. They were merged into one.",
		legacyDecorations,
	)
}

func TestLegacySettings(t *testing.T) {
	_, warnings := legacyCells(t, "settings")

	wantLegacyWarnings(t, warnings,
		"6 items have settings that could not be read and were left out: "+
			"a list, a number too large, cell 7, empty, not json and too deep.",
		"Left out 1 line that was not a network link.",
	)
}

// TestLegacyInterfacesThatCannotBeRead checks that interfaces that are not a list of objects are
// left out with a warning that names the nodes, which are kept.
func TestLegacyInterfacesThatCannotBeRead(t *testing.T) {
	device := func(id, hostname string, interfaces any) string {
		return legacyVertex(id, hostname, map[string]any{
			"device":  "server",
			"general": map[string]any{"hostname": hostname},
			"network": map[string]any{"interfaces": interfaces, "routes": []any{}},
		}, "image;html=1;image=/server_blue_vm.png", 40, 40)
	}

	eth0 := map[string]any{"name": "eth0", "vlan": "lan"}

	doc, warnings := convertLegacy(t, legacyModel(
		device("2", "text", "x"),
		device("3", "object", map[string]any{"eth0": eth0}),
		device("4", "mixed", []any{nil, "x", 5, eth0}),
		device("5", "none", nil),
		device("6", "fine", []any{eth0}),
	), "interfaces")

	wantLegacyWarnings(t, warnings,
		"Added a switch for 1 network that had none in the legacy diagram: lan.",
		"3 nodes have interfaces that could not be read and were left out: mixed, object and text.",
	)

	for hostname, want := range map[string]any{
		"text": nil, "object": nil, "none": nil, "mixed": []any{eth0}, "fine": []any{eth0},
	} {
		network, _ := nodeByHostname(t, doc, hostname).Device.Spec["network"].(map[string]any)

		got, set := network["interfaces"]
		if set != (want != nil) || (set && !reflect.DeepEqual(got, want)) {
			t.Errorf("%s: interfaces = %s, want %s", hostname, asJSON(t, got), asJSON(t, want))
		}

		// The rest of the settings stay.
		if _, kept := network["routes"]; !kept {
			t.Errorf("%s: network = %s, want its routes kept", hostname, asJSON(t, network))
		}
	}

	if got := legacyTargets(t, doc, "mixed"); len(got) != 1 || got["eth0"] != legacySwitchesOf(doc, "lan")[0].ID {
		t.Errorf("connections of mixed = %v, want its one interface on the switch of lan", got)
	}

	// The device of one such node is named in the warning by its hostname.
	_, warnings = convertLegacy(t, legacyModel(device("2", "only", "x")), "interfaces")

	wantLegacyWarnings(t, warnings, "1 node has interfaces that could not be read and were left out: only.")
}

func TestLegacyVariables(t *testing.T) {
	const (
		unknown  = "2 placeholders have no value in the diagram and were kept as text: $OTHER_UNKNOWN and $UNKNOWN."
		defaults = "Used the legacy defaults for variables the diagram does not define: " +
			"$DEFAULT_MEMORY = 2048, $DEFAULT_ROUTER_IMAGE = vyos.qc2 and $DEFAULT_VM_IMAGE = ubuntu.qc2."
		allDefaults = "Used the legacy defaults for variables the diagram does not define: " +
			"$DEFAULT_MEMORY = 2048, $DEFAULT_ROUTER_IMAGE = vyos.qc2, $DEFAULT_VCPU = 1 and $DEFAULT_VM_IMAGE = ubuntu.qc2."
		allUnknown = "5 placeholders have no value in the diagram and were kept as text: " +
			"$COUNT, $DEFAULT_VM, $MY_IMAGE, $OTHER_UNKNOWN and $UNKNOWN."
	)

	for name, want := range map[string][]string{
		"variables":           {defaults, unknown},
		"variables-missing":   {allDefaults, allUnknown},
		"variables-malformed": {allDefaults, allUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			_, warnings := legacyCells(t, name)

			wantLegacyWarnings(t, warnings, want...)
		})
	}
}

// TestDecodeLegacyForms decodes one diagram in every form that is read:
// each gives the same document.
func TestDecodeLegacyForms(t *testing.T) {
	cells := legacyDevice("2", "alpha", "router", 40, 40, "lan") + legacySwitch("3", "lan", 7, 240, 40) +
		legacyLine("4", "2", "3", "lan", 0)
	plain := legacyModel(cells)

	want, _ := convertLegacy(t, plain, "forms")
	if len(want.Nodes) != 2 || legacyAlias(want, "lan") != 7 {
		t.Fatalf("the diagram converts into %s", asJSON(t, want))
	}

	for name, content := range map[string]string{
		"a byte order mark":       "\xef\xbb\xbf" + plain,
		"white space around it":   " \r\n\t" + plain + "\n\n",
		"an XML declaration":      `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + plain,
		"comments":                "<!-- saved by the legacy Builder -->" + strings.Replace(plain, "<root>", "<root><!-- cells -->", 1),
		"a processing step":       `<?xml-stylesheet href="x.css"?>` + plain,
		"a root alone":            `<root><mxCell id="0"/><mxCell id="1" parent="0"/>` + cells + `</root>`,
		"content after the root":  plain + "<mxGraphModel><root>" + legacyDevice("9", "later", "server", 0, 0) + "</root></mxGraphModel>",
		"text that is no XML too": plain + " and <<< more",
		"other elements": strings.Replace(plain, "<root>", `<extra><root><mxCell id="x" vertex="1"/></root></extra><root>`, 1) +
			"",
		"a second root": strings.Replace(plain, "</root>", `</root><root>`+legacyDevice("9", "later", "server", 0, 0)+`</root>`, 1),
		"a namespace":   strings.Replace(plain, "<mxGraphModel ", `<mxGraphModel xmlns="http://example.com/mx" `, 1),
	} {
		t.Run(name, func(t *testing.T) {
			got, _ := convertLegacy(t, content, "forms")
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the document differs: %s", asJSON(t, got))
			}
		})
	}

	// Nothing at all is an empty diagram, not an error.
	for name, content := range map[string]string{
		"empty": "", "white space": " \n\t ", "a byte order mark": "\xef\xbb\xbf", "an empty model": "<mxGraphModel/>",
		"a model without cells": legacyModel(),
	} {
		t.Run(name, func(t *testing.T) {
			doc, warnings := convertLegacy(t, content, "nothing")

			if len(doc.Nodes) != 0 || len(doc.Networks) != 0 || len(doc.Edges) != 0 {
				t.Errorf("the document holds %s", asJSON(t, doc))
			}

			wantLegacyWarnings(t, warnings, "The diagram has no nodes.")
		})
	}
}

// TestDecodeLegacyRefusals checks the reason and exact message of each refusal, which repeats
// nothing of the input but a line number or the name of the root element.
func TestDecodeLegacyRefusals(t *testing.T) {
	const (
		notDiagram = "this is not a legacy Builder diagram: expected mxGraph XML, " +
			"or a Topology config with the builder-xml annotation"
		doctype = "the legacy diagram has a DOCTYPE or entity declaration, which is not accepted"
	)

	nested := func(levels int) string {
		return "<root>" + strings.Repeat("<a>", levels-1) + strings.Repeat("</a>", levels-1) + "</root>"
	}

	for _, test := range []struct {
		name    string
		content string
		reason  builder.LegacyReason
		message string
	}{
		{"plain words", "hello world", builder.LegacyNotDiagram, notDiagram},
		{"a config", "apiVersion: phenix.sandia.gov/v1\nkind: Topology\n", builder.LegacyNotDiagram, notDiagram},
		{"JSON", `{"kind":"Topology"}`, builder.LegacyNotDiagram, notDiagram},
		// Encoded and compressed forms are not read.
		{"base64 of a diagram", "PG14R3JhcGhNb2RlbC8+", builder.LegacyNotDiagram, notDiagram},
		{
			"XML cut short", "<mxGraphModel><root>\n<mxCell id=\"0\"/>\n<mxCell", builder.LegacyMalformed,
			"the legacy diagram is not well-formed XML (line 3)",
		},
		{
			"a root that never closes", "<mxGraphModel><root></root>", builder.LegacyMalformed,
			"the legacy diagram is not well-formed XML (line 1)",
		},
		{
			"tags that do not match", "<mxGraphModel>\n<root>\n</mxGraphModel>", builder.LegacyMalformed,
			"the legacy diagram is not well-formed XML (line 3)",
		},
		{
			"no element", "<!-- nothing -->", builder.LegacyMalformed,
			"the legacy diagram is not well-formed XML (line 1)",
		},
		{
			"an undefined entity", "<mxGraphModel>\n<root><mxCell value=\"&xxe;\"/></root></mxGraphModel>",
			builder.LegacyMalformed, "the legacy diagram is not well-formed XML (line 2)",
		},
		{
			"another encoding", `<?xml version="1.0" encoding="ISO-8859-1"?><mxGraphModel/>`,
			builder.LegacyMalformed, "the legacy diagram is not well-formed XML (line 1)",
		},
		{
			"bytes that are not UTF-8", "<mxGraphModel><root><mxCell value=\"\xff\xfe\"/></root></mxGraphModel>",
			builder.LegacyMalformed, "the legacy diagram is not well-formed XML (line 1)",
		},
		{
			"a character XML does not allow", "<mxGraphModel><root><mxCell value=\"&#0;\"/></root></mxGraphModel>",
			builder.LegacyMalformed, "the legacy diagram is not well-formed XML (line 1)",
		},
		{"a DOCTYPE", `<!DOCTYPE mxGraphModel><mxGraphModel/>`, builder.LegacyDoctype, doctype},
		{
			"entities that expand", `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]>` +
				`<mxGraphModel><root><mxCell value="&lol2;"/></root></mxGraphModel>`,
			builder.LegacyDoctype, doctype,
		},
		{
			"an entity read from a file", `<!DOCTYPE x [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` +
				`<mxGraphModel><root><mxCell value="&xxe;"/></root></mxGraphModel>`,
			builder.LegacyDoctype, doctype,
		},
		{
			"a declaration inside the model", `<mxGraphModel><!ELEMENT a ANY><root/></mxGraphModel>`,
			builder.LegacyDoctype, doctype,
		},
		{
			"another root element", `<svg xmlns="http://www.w3.org/2000/svg"><root/></svg>`, builder.LegacyRoot,
			"the XML is not a legacy Builder diagram: its root element is <svg>, not <mxGraphModel>",
		},
		{
			// The wrapper draw.io saves a diagram in is not read.
			"a draw.io file", `<mxfile><diagram><mxGraphModel><root/></mxGraphModel></diagram></mxfile>`,
			builder.LegacyRoot,
			"the XML is not a legacy Builder diagram: its root element is <mxfile>, not <mxGraphModel>",
		},
		{
			"a root element with a long name", "<" + strings.Repeat("é", 100) + "/>", builder.LegacyRoot,
			"the XML is not a legacy Builder diagram: its root element is <" + strings.Repeat("é", 32) +
				">, not <mxGraphModel>",
		},
		{
			"elements nested too deep", nested(33), builder.LegacyTooDeep,
			"the legacy diagram nests elements more than 32 levels deep",
		},
		{
			"too many cells", "<root>" + strings.Repeat("<mxCell/>", 10001) + "</root>", builder.LegacyTooMany,
			"the legacy diagram has more than 10000 cells",
		},
		{
			"too large", "<root>" + strings.Repeat(" ", builder.MaxLegacyBytes-12) + "</root>", builder.LegacyTooLarge,
			"the legacy diagram is larger than 5242880 bytes",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagram, err := builder.DecodeLegacy([]byte(test.content))

			var refusal *builder.LegacyError
			if !errors.As(err, &refusal) || diagram != nil {
				t.Fatalf("DecodeLegacy = %v, %v, want a refusal", diagram, err)
			}

			if refusal.Reason != test.reason || refusal.Message != test.message || err.Error() != test.message {
				t.Errorf("refusal = %q %q, want %q %q", refusal.Reason, refusal.Message, test.reason, test.message)
			}

			if !errors.Is(err, builder.ErrLegacy) {
				t.Error("a refusal must match ErrLegacy")
			}
		})
	}

	// What is exactly at a bound is read.
	decodeLegacy(t, nested(32))

	if diagram := decodeLegacy(t, "<root>"+strings.Repeat("<mxCell/>", 10000)+"</root>"); diagram.Cells() != 10000 {
		t.Errorf("%d cells, want 10000", diagram.Cells())
	}
}

// TestFromLegacyTopologyMerge lays a diagram over its topology: the spec is the truth for every
// node, and the diagram only places them.
func TestFromLegacyTopologyMerge(t *testing.T) {
	diagram := legacyModel(
		// The settings of the diagram are out of date: the topology says
		// 4096 MB, and its hostname is in another case.
		legacyDevice("2", "Alpha", "router", 40, 40, "lan"),
		legacyDevice("3", "gone", "server", 240, 40, "lan"),
		legacyDevice("4", "gone-too", "server", 440, 40),
		legacyDevice("5", "gone", "server", 640, 40),
		legacySwitch("6", "lan", 42, 240, 240),
		legacyLine("7", "2", "6", "lan", 0),
		legacyLine("8", "3", "6", "lan", 0),
		// Settings that cannot be read: the label names the node.
		`<object label="beta" schemaVars="{broken" id="9"><mxCell style="image;image=/firewall_grey_vm.png" `+
			`vertex="1" parent="1"><mxGeometry x="40" y="240" width="80" height="80" as="geometry"/></mxCell></object>`,
		`<object label="nobody" schemaVars="{broken" id="10"><mxCell vertex="1" parent="1"/></object>`,
	)

	config := legacyTopology("site", diagram,
		legacyNode("alpha", "lan"), legacyNode("beta", "lan", "wan"), legacyNode("new-1"), legacyNode("new-2", "wan"))

	doc, warnings, err := builder.FromLegacyTopology(config)
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	checkLegacyDocument(t, doc, warnings)

	wantLegacyWarnings(t, warnings,
		"2 nodes of the legacy diagram are not in the topology and were left out: gone and gone-too.",
		"2 nodes of the topology were not in the legacy diagram and were placed below it: new-1 and new-2.",
		"Added a switch for 1 network that had none in the legacy diagram: wan.",
		"1 item has settings that could not be read and was left out: nobody.",
	)

	hostnames := make([]string, 0, 4)
	for _, node := range doc.DeviceNodes() {
		hostnames = append(hostnames, node.Device.Hostname)
	}

	if want := []string{"alpha", "beta", "new-1", "new-2"}; !slices.Equal(hostnames, want) {
		t.Fatalf("devices = %v, want the topology's %v", hostnames, want)
	}

	// The topology's spec, the diagram's position and icon.
	alpha := nodeByHostname(t, doc, "alpha")
	if alpha.Device.Spec["hardware"].(map[string]any)["memory"] != 4096 || alpha.Device.IconKey != "router" ||
		alpha.Position != (builder.Position{X: 80, Y: 112}) {
		t.Errorf("alpha = %s, want the topology's spec where the diagram has it", asJSON(t, alpha))
	}

	if _, kept := alpha.Device.Spec["device"]; kept {
		t.Error("alpha carries a setting of the diagram")
	}

	beta := nodeByHostname(t, doc, "beta")
	if beta.Position != (builder.Position{X: 80, Y: 512}) || beta.Device.IconKey != "firewall" {
		t.Errorf("beta = %s, want it placed by its label, with the icon of its image", asJSON(t, beta))
	}

	// What the diagram does not hold is laid out below everything it does,
	// from its left edge.
	if first, second := legacyPosition(t, doc, "new-1"), legacyPosition(t, doc, "new-2"); first != (builder.Position{X: 64, Y: 976}) ||
		second != (builder.Position{X: 384, Y: 976}) {
		t.Errorf("new-1 and new-2 are at %+v and %+v, want a row below the diagram", first, second)
	}

	if legacyAlias(doc, "lan") != 42 || legacyAlias(doc, "wan") != 0 {
		t.Errorf("networks = %s, want the VLAN ID of the diagram as the alias of lan", asJSON(t, doc.Networks))
	}

	// The switch of wan sits between the placed devices of the network.
	// Beta is the only one, so it is moved down until it is clear of beta.
	wan := legacySwitchesOf(doc, "wan")
	if len(wan) != 1 || wan[0].Position != (builder.Position{X: 64, Y: 656}) {
		t.Errorf("switches of wan = %s", asJSON(t, wan))
	}

	digest, err := builder.ImportDigest(config)
	if err != nil {
		t.Fatalf("ImportDigest returned error: %v", err)
	}

	if doc.Source.Kind != builder.SourceKindTopology || doc.Source.Digest != digest ||
		!maps.Equal(doc.Source.Annotations, map[string]string{"owner": "ops"}) {
		t.Errorf("source = %+v, want the topology's, without the diagram among its annotations", doc.Source)
	}

	// The topology itself is left as it was.
	if config.Metadata.Annotations[builder.LegacyXMLAnnotation] != diagram ||
		!reflect.DeepEqual(config.Spec["nodes"].([]any)[0], legacyNode("alpha", "lan")) {
		t.Error("the conversion changed the config")
	}
}

// TestFromLegacyTopologyKeepsPlainImport checks that an unused diagram leaves the plain import: an
// empty one with no warning, and an unreadable one with a warning first.
func TestFromLegacyTopologyKeepsPlainImport(t *testing.T) {
	nodes := []any{legacyNode("alpha", "lan"), legacyNode("alpha"), legacyNode("beta", "lan")}

	_, plainWarnings := documentFromConfig(t, legacyTopology("site", "", nodes...))
	if len(plainWarnings) != 1 {
		t.Fatalf("the plain import warns %q, want one warning of its own", plainWarnings)
	}

	for name, test := range map[string]struct {
		diagram string
		warning string
	}{
		"an empty annotation":   {"", ""},
		"an empty model":        {"<mxGraphModel/>", ""},
		"a model without cells": {legacyModel(), ""},
		"a diagram cut short": {
			"<mxGraphModel><root>",
			"The legacy diagram of topology site could not be read (the legacy diagram is not well-formed XML (line 1)), " +
				"so its layout was not used. Nodes were placed automatically.",
		},
		"something else": {
			"not a diagram",
			"The legacy diagram of topology site could not be read (this is not a legacy Builder diagram: " +
				"expected mxGraph XML, or a Topology config with the builder-xml annotation), " +
				"so its layout was not used. Nodes were placed automatically.",
		},
		"a diagram that is too large": {
			"<root>" + strings.Repeat(" ", builder.MaxLegacyBytes) + "</root>",
			"The legacy diagram of topology site could not be read (the legacy diagram is larger than 5242880 bytes), " +
				"so its layout was not used. Nodes were placed automatically.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// The plain import of the same topology: its digest covers the
			// diagram, read or not.
			plain, _ := documentFromConfig(t, legacyTopology("site", test.diagram, nodes...))

			doc, warnings, err := builder.FromLegacyTopology(legacyTopology("site", test.diagram, nodes...))
			if err != nil {
				t.Fatalf("FromLegacyTopology returned error: %v", err)
			}

			want := plainWarnings
			if test.warning != "" {
				want = append([]string{test.warning}, plainWarnings...)
			}

			wantLegacyWarnings(t, warnings, want...)
			checkLegacyDocument(t, doc, warnings)

			doc.Source.Warnings = plain.Source.Warnings
			if !reflect.DeepEqual(doc, plain) {
				t.Errorf("the document differs from the plain import: %s", asJSON(t, doc))
			}
		})
	}

	// A config that carries no diagram, and one that is no topology, are
	// generated as they always were.
	experiment := loadConfig(t, "experiment.json")
	experiment.Metadata.Annotations = store.Annotations{builder.LegacyXMLAnnotation: legacyModel(legacyDevice("2", "x", "server", 0, 0))}

	for name, config := range map[string]store.Config{
		"a plain topology": loadConfig(t, "topology.json"), "an experiment": experiment,
	} {
		want, wantWarnings := documentFromConfig(t, config)

		got, warnings, err := builder.FromLegacyTopology(config)
		if err != nil || !reflect.DeepEqual(got, want) || !slices.Equal(warnings, wantWarnings) {
			t.Errorf("%s: FromLegacyTopology differs from FromConfig (%v)", name, err)
		}
	}

	if _, _, err := builder.FromLegacyTopology(store.Config{Kind: "Scenario"}); !errors.Is(err, builder.ErrUnsupportedKind) {
		t.Errorf("a scenario gave %v, want ErrUnsupportedKind", err)
	}
}

// TestFromLegacyTopologyResolvesIncludes checks that with a loader the devices of included
// topologies are shown, and the diagram places those it holds.
func TestFromLegacyTopologyResolvesIncludes(t *testing.T) {
	config := legacyTopology("site",
		legacyModel(legacyDevice("2", "alpha", "server", 40, 40), legacyDevice("3", "dns", "server", 440, 40)),
		legacyNode("alpha"))
	config.Spec["includeTopologies"] = []any{"shared"}

	shared := store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: "shared"},
		Spec: map[string]any{"nodes": []any{legacyNode("dns"), legacyNode("mail")}},
	}

	doc, warnings, err := builder.FromLegacyTopology(config, builder.WithTopologyLoader(
		func(name string) (*store.Config, error) {
			if name != "shared" {
				return nil, errors.New("no such topology")
			}

			return &shared, nil
		},
	))
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	checkLegacyDocument(t, doc, warnings)

	dns, mail := nodeByHostname(t, doc, "dns"), nodeByHostname(t, doc, "mail")
	if dns.Device.IncludedFrom != "shared" || dns.Position != (builder.Position{X: 880, Y: 112}) ||
		mail.Device.IncludedFrom != "shared" || mail.Position != (builder.Position{X: 80, Y: 448}) {
		t.Errorf("dns = %s, mail = %s, want the included nodes, dns where the diagram has it", asJSON(t, dns), asJSON(t, mail))
	}

	if !slices.Equal(doc.Source.IncludeTopologies, []string{"shared"}) ||
		!containsSubstring(warnings, "1 node of the topology was not in the legacy diagram and was placed below it: mail.") {
		t.Errorf("includes = %v, warnings = %q", doc.Source.IncludeTopologies, warnings)
	}
}

// TestLegacyAddedSwitchesAreClear checks that a switch added for a VLAN covers no device or switch:
// it moves down until clear, or below the diagram when no place near is free.
func TestLegacyAddedSwitchesAreClear(t *testing.T) {
	// A chain of three devices on one VLAN, two VLANs between the same two devices, and a column of
	// devices, each on its own VLAN, that leaves no room below most.
	const column = 80

	cells := make([]string, 0, column+3)
	cells = append(cells,
		legacyDevice("a", "chain-a", "server", 0, 0, "chain", "pair-1", "pair-2"),
		legacyDevice("b", "chain-b", "server", 200, 0, "chain", "pair-1", "pair-2"),
		legacyDevice("c", "chain-c", "server", 400, 0, "chain"),
	)

	for i := range column {
		cells = append(cells, legacyDevice(
			fmt.Sprintf("d%d", i), fmt.Sprintf("column-%02d", i), "server", 2000, 64*i, fmt.Sprintf("own-%02d", i),
		))
	}

	doc, warnings := convertLegacy(t, legacyModel(cells...), "clear")

	wantLegacyWarnings(t, warnings,
		"Added a switch for 83 networks that had none in the legacy diagram: "+
			"chain, own-00, own-01, own-02, own-03, own-04, own-05, own-06 and 75 more.",
	)

	type card struct {
		node                     *builder.Node
		left, top, right, bottom float64
	}

	cards := make([]card, 0, len(doc.Nodes))

	for i := range doc.Nodes {
		node := &doc.Nodes[i]
		width, height := 160.0, 96.0

		if node.Kind == builder.NodeKindSwitch {
			width, height = 180.0, 72.0
		}

		cards = append(cards, card{node, node.Position.X, node.Position.Y, node.Position.X + width, node.Position.Y + height})
	}

	for i, first := range cards {
		for _, second := range cards[i+1:] {
			if first.node.Kind == builder.NodeKindDevice && second.node.Kind == builder.NodeKindDevice {
				continue
			}

			if first.left < second.right && second.left < first.right && first.top < second.bottom && second.top < first.bottom {
				t.Fatalf("%s at %+v covers %s at %+v", first.node.Label, first.node.Position, second.node.Label, second.node.Position)
			}
		}
	}

	for network, want := range map[string]builder.Position{
		// The middle of the chain is its second device: the switch sits
		// below it.
		"chain": {X: 384, Y: 176},
		// Two VLANs between the same two devices: one switch below the other.
		"pair-1": {X: 192, Y: 48}, "pair-2": {X: 192, Y: 176},
		// In the column a switch moves down past the devices below its own; those past the last device
		// sit below the column, one under the other.
		"own-16": {X: 3984, Y: 10288}, "own-17": {X: 3984, Y: 10416}, "own-79": {X: 3984, Y: 18352},
		// Those that do not get that far are laid out below everything.
		"own-00": {X: 0, Y: 18672}, "own-15": {X: 960, Y: 19152},
	} {
		if nodes := legacySwitchesOf(doc, network); len(nodes) != 1 || nodes[0].Position != want {
			t.Errorf("switches of %s = %s, want one at %+v", network, asJSON(t, nodes), want)
		}
	}
}

// TestLegacyGroupsNestOnlySoDeep asserts groups made from a diagram nest no
// deeper than its elements may: a cell below that is left at the top level.
func TestLegacyGroupsNestOnlySoDeep(t *testing.T) {
	cells := make([]string, 0, 40)

	for i := range 40 {
		cells = append(cells, fmt.Sprintf(
			`<mxCell id="g%d" value="group %02d" style="group" vertex="1" parent="%s">`+
				`<mxGeometry x="10" y="10" width="100" height="100" as="geometry"/></mxCell>`,
			i, i, map[bool]string{true: "1", false: fmt.Sprintf("g%d", i-1)}[i == 0],
		))
	}

	doc, _ := convertLegacy(t, legacyModel(cells...), "deep")

	byID := map[string]*builder.Node{}
	for i := range doc.Nodes {
		byID[doc.Nodes[i].ID] = &doc.Nodes[i]
	}

	deepest := 0

	for _, node := range byID {
		depth := 0
		for at := node; at.ParentID != ""; at = byID[at.ParentID] {
			depth++
		}

		deepest = max(deepest, depth)
	}

	// The innermost cell holds nothing, so it is a note; the cell that would be too deep joins an
	// outer group that lies around it.
	groups := legacyNodes(doc, builder.NodeKindGroup)
	if len(groups) != 39 || deepest != 32 || groups["group 32"].ParentID != groups["group 31"].ID ||
		groups["group 33"].ParentID == groups["group 32"].ID {
		t.Errorf("%d groups nest %d deep, want 39 that nest 32 deep, without group 33 in group 32", len(groups), deepest)
	}
}

func TestHasLegacyDiagram(t *testing.T) {
	for name, test := range map[string]struct {
		kind        string
		annotations store.Annotations
		want        bool
	}{
		"a diagram":            {"Topology", store.Annotations{builder.LegacyXMLAnnotation: "<mxGraphModel/>"}, true},
		"an empty diagram":     {"Topology", store.Annotations{builder.LegacyXMLAnnotation: ""}, true},
		"a kind in lower case": {"topology", store.Annotations{builder.LegacyXMLAnnotation: ""}, true},
		"beside a document":    {"Topology", store.Annotations{"builder-xml": "<x/>", "builder-doc": "{}"}, true},
		"other annotations":    {"Topology", store.Annotations{"builder-doc": "{}", "owner": "ops"}, false},
		"no annotations":       {"Topology", nil, false},
		"an experiment":        {"Experiment", store.Annotations{builder.LegacyXMLAnnotation: "<mxGraphModel/>"}, false},
		"a scenario":           {"Scenario", store.Annotations{builder.LegacyXMLAnnotation: "<mxGraphModel/>"}, false},
	} {
		config := store.Config{Kind: test.kind, Metadata: store.ConfigMetadata{Name: "lab", Annotations: test.annotations}}

		if got := builder.HasLegacyDiagram(config); got != test.want {
			t.Errorf("%s: HasLegacyDiagram = %t, want %t", name, got, test.want)
		}
	}

	if builder.LegacyXMLAnnotation != "builder-xml" || !builder.IsBuilderAnnotation(builder.LegacyXMLAnnotation) {
		t.Error("the legacy annotation must be the Builders' own builder-xml")
	}
}

func TestLegacyNodeID(t *testing.T) {
	first := builder.LegacyNodeID(builder.NodeKindSwitch, "20")

	if !builder.IsUUID(first) || first != builder.LegacyNodeID(builder.NodeKindSwitch, "20") {
		t.Errorf("LegacyNodeID = %q, want a UUID that is the same each time", first)
	}

	for _, other := range []string{
		builder.LegacyNodeID(builder.NodeKindGroup, "20"), builder.LegacyNodeID(builder.NodeKindSwitch, "21"),
		builder.SwitchNodeID("20"), builder.NoteNodeID("legacy/20"),
	} {
		if other == first {
			t.Errorf("LegacyNodeID collides with another ID: %s", first)
		}
	}
}

// legacySeeds are the inputs the fuzz test starts from: every test diagram,
// and input of every kind that is refused.
func legacySeeds(tb testing.TB) [][]byte {
	tb.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", "legacy", "cells", "*.xml"))
	if err != nil || len(files) == 0 {
		tb.Fatalf("finding the test diagrams: %v", err)
	}

	seeds := make([][]byte, 0, len(files)+16)
	seeds = append(seeds, legacyFile(tb, "sample.xml"))

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			tb.Fatalf("reading %s: %v", file, err)
		}

		seeds = append(seeds, data)
	}

	for _, text := range []string{
		"", "hello", "<", "<mxGraphModel", "<mxGraphModel><root>", "<mxGraphModel/>", "<root/>", "<svg/>",
		`<!DOCTYPE x [<!ENTITY a "b">]><mxGraphModel/>`, `<?xml version="1.0" encoding="latin1"?><root/>`,
		`<root><mxCell id="a" parent="a" vertex="1"/><mxCell id="b" parent="c" vertex="1"/><mxCell id="c" parent="b" vertex="1"/></root>`,
		`<root><object schemaVars="{&quot;device&quot;:&quot;switch&quot;,&quot;name&quot;:&quot;a b&quot;,&quot;id&quot;:&quot;7&quot;}">` +
			`<mxCell vertex="1"><mxGeometry x="NaN" y="1e999" width="-1" as="geometry"/></mxCell></object></root>`,
		`<root><object label="&lt;b&gt;x&lt;/b&gt;" schemaVars="{&quot;general&quot;:{&quot;hostname&quot;:&quot; a\tb &quot;},` +
			`&quot;network&quot;:{&quot;interfaces&quot;:[{&quot;name&quot;:&quot;e&quot;,&quot;vlan&quot;:&quot; v 1 &quot;}]}}">` +
			`<mxCell vertex="1" style="html=1"/></object></root>`,
		`<mxGraphModel experimentVars="[{&quot;name&quot;:&quot;A&quot;,&quot;value&quot;:&quot;$A&quot;}]"><root>` +
			`<object schemaVars="{&quot;general&quot;:{&quot;hostname&quot;:&quot;$A&quot;}}"><mxCell vertex="1"/></object></root></mxGraphModel>`,
		"<root>" + strings.Repeat("<a>", 40),
	} {
		seeds = append(seeds, []byte(text))
	}

	return seeds
}

// checkLegacyConversion checks that any input is refused with a LegacyError or converts, alone and
// over a topology, into the same valid document with no more devices than cells.
func checkLegacyConversion(t *testing.T, content []byte, topology store.Config) {
	t.Helper()

	diagram, err := builder.DecodeLegacy(content)
	if err != nil {
		var refusal *builder.LegacyError
		if !errors.As(err, &refusal) || refusal.Message == "" || diagram != nil {
			t.Fatalf("DecodeLegacy returned %v, %v, want a LegacyError", diagram, err)
		}

		return
	}

	encode := func(doc *builder.Document, warnings []string, err error) []byte {
		t.Helper()

		if err != nil {
			t.Fatalf("the conversion returned error: %v", err)
		}

		checkLegacyDocument(t, doc, warnings)

		data, err := builder.Encode(doc)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}

		return data
	}

	first, firstWarnings, err := builder.FromLegacy(diagram, "fuzz")
	data := encode(first, firstWarnings, err)

	if devices := len(first.DeviceNodes()); devices > diagram.Cells() {
		t.Fatalf("%d devices from %d cells", devices, diagram.Cells())
	}

	again, err := builder.DecodeLegacy(content)
	if err != nil {
		t.Fatalf("the second DecodeLegacy returned error: %v", err)
	}

	second, secondWarnings, err := builder.FromLegacy(again, "fuzz")
	if !bytes.Equal(data, encode(second, secondWarnings, err)) {
		t.Fatal("two conversions of one diagram differ")
	}

	// Converting the same decoded diagram again changes nothing either.
	third, thirdWarnings, err := builder.FromLegacy(diagram, "fuzz")
	if !bytes.Equal(data, encode(third, thirdWarnings, err)) {
		t.Fatal("converting a diagram changed it")
	}

	topology.Metadata.Annotations = store.Annotations{builder.LegacyXMLAnnotation: string(content)}

	merged, mergedWarnings, err := builder.FromLegacyTopology(topology)
	encode(merged, mergedWarnings, err)

	if got := len(merged.DeviceNodes()); got != 5 {
		t.Fatalf("the topology converted into %d devices, want its 5", got)
	}
}

// FuzzDecodeLegacy feeds arbitrary input to the decoder and the conversion. Run it longer with
// "go test -fuzz=FuzzDecodeLegacy -fuzztime=30s ./types/builder/".
func FuzzDecodeLegacy(f *testing.F) {
	for _, seed := range legacySeeds(f) {
		f.Add(seed)
	}

	topology := legacySampleTopology(f)

	f.Fuzz(func(t *testing.T, content []byte) {
		checkLegacyConversion(t, content, topology)
	})
}
