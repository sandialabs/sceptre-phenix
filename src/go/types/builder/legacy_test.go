package builder_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
	legacyAddedB      = "The Builder added a switch for 1 network that had none in the legacy diagram: b."
	legacyOneLine     = "The Builder did not import 1 line that was not a network link."
	legacyDecorations = "The Builder kept text and containers as notes and groups. " +
		"The Builder did not convert their colors, fonts and other formatting."
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
		tb.Fatalf("FromLegacy(%s): %v", name, err)
	}

	if err := checkLegacyDocument(doc, warnings); err != nil {
		tb.Errorf("FromLegacy(%s): %v", name, err)
	}

	if doc.Source.Kind != builder.SourceKindManual || doc.Source.Name != "" || doc.Source.Digest != "" {
		tb.Errorf("FromLegacy(%s) source = %+v, want one that names no config", name, doc.Source)
	}

	return doc, warnings
}

// checkLegacyDocument returns an error unless the converted document is valid, carries the warnings
// returned with it, and parses after it is encoded.
func checkLegacyDocument(doc *builder.Document, warnings []string) error {
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("Validate() = %w, want nil", err)
	}

	if !slices.Equal(doc.Source.Warnings, warnings) {
		return fmt.Errorf("source warnings = %q, want the returned %q", doc.Source.Warnings, warnings)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		return fmt.Errorf("Encode() = %w, want nil", err)
	}

	if _, err := builder.Parse(data); err != nil {
		return fmt.Errorf("Parse(Encode()) = %w, want nil", err)
	}

	return nil
}

// legacyGolden returns the encoded document and the content of testdata/legacy/<name>, after it
// rewrites that file when the test runs with -update-legacy-golden.
func legacyGolden(t *testing.T, name string, doc *builder.Document) (string, string) {
	t.Helper()

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode(): %v", err)
	}

	data = append(data, '\n')
	path := filepath.Join("testdata", "legacy", name)

	if *updateLegacyGolden {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}

		return string(data), string(data)
	}

	return string(data), string(legacyFile(t, name))
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

// legacyPositions returns the positions of nodes.
func legacyPositions(nodes []*builder.Node) []builder.Position {
	positions := make([]builder.Position, 0, len(nodes))
	for _, node := range nodes {
		positions = append(positions, node.Position)
	}

	return positions
}

// TestLegacySample converts the diagram captured from the legacy Builder
// itself, without its topology.
func TestLegacySample(t *testing.T) {
	doc, warnings := convertLegacy(t, string(legacyFile(t, "sample.xml")), "sample")

	if got, want := legacyGolden(t, "sample.golden.json", doc); got != want {
		t.Errorf("FromLegacy(sample.xml) mismatch with sample.golden.json, rewrite it with -update-legacy-golden "+
			"once the change is meant (-want +got):\n%s", cmp.Diff(want, got))
	}

	if diff := cmp.Diff([]string{legacyAddedB, legacyOneLine, legacyDecorations}, warnings); diff != "" {
		t.Errorf("FromLegacy(sample.xml) warnings mismatch (-want +got):\n%s", diff)
	}

	// What the legacy Builder wrote when it saved: its variables resolved,
	// values kept as text, its own keys dropped, lists turned into maps.
	router := nodeByHostname(t, doc, "router-device-0").Device.Spec
	wantHardware := map[string]any{
		"drives": []any{map[string]any{"image": "vyos.qc2"}}, "memory": "2048", "os_type": "linux", "vcpus": "1",
	}

	if diff := cmp.Diff(wantHardware, jsonValue(t, router["hardware"])); diff != "" {
		t.Errorf("FromLegacy(sample.xml) router-device-0 hardware mismatch (-want +got):\n%s", diff)
	}

	client := nodeByHostname(t, doc, "client-1").Device.Spec
	if diff := cmp.Diff(map[string]any{"owner": "ops"}, client["annotations"]); diff != "" {
		t.Errorf("FromLegacy(sample.xml) client-1 annotations mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"zone": "corp"}, client["labels"]); diff != "" {
		t.Errorf("FromLegacy(sample.xml) client-1 labels mismatch (-want +got):\n%s", diff)
	}

	for _, node := range doc.DeviceNodes() {
		for _, key := range []string{"device", "schema"} {
			if value, kept := node.Device.Spec[key]; kept {
				t.Errorf("FromLegacy(sample.xml) %s spec %s = %v, want the legacy key dropped", node.Device.Hostname, key, value)
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
		t.Fatalf("ToTopologyConfig(%s): %v", saved.Name, err)
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
			t.Fatalf("%d nodes have %d hostnames, want one each", len(list), len(found))
		}

		return found
	}

	got, want := byHostname(published.Spec["nodes"]), byHostname(saved.Topology["nodes"])
	if len(want) != 5 {
		t.Errorf("the legacy Builder saved %d nodes, want 5", len(want))
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ToTopologyConfig(%s) nodes mismatch with the legacy save (-want +got):\n%s", saved.Name, diff)
	}

	if keys := slices.Sorted(maps.Keys(published.Spec)); !slices.Equal(keys, []string{"nodes"}) {
		t.Errorf("ToTopologyConfig(%s) spec keys = %v, want only the nodes the legacy Builder saved", saved.Name, keys)
	}
}

// TestLegacySampleTopology converts the sample with the topology the legacy Builder saved for it,
// which gives the same diagram.
func TestLegacySampleTopology(t *testing.T) {
	config := legacySampleTopology(t)

	doc, warnings, err := builder.FromLegacyTopology(config)
	if err != nil {
		t.Fatalf("FromLegacyTopology(sample): %v", err)
	}

	if err := checkLegacyDocument(doc, warnings); err != nil {
		t.Errorf("FromLegacyTopology(sample): %v", err)
	}

	if diff := cmp.Diff([]string{legacyAddedB, legacyOneLine, legacyDecorations}, warnings); diff != "" {
		t.Errorf("FromLegacyTopology(sample) warnings mismatch (-want +got):\n%s", diff)
	}

	// The digest covers the diagram that was read, not the spec alone.
	digest, err := builder.ImportDigest(config)
	if err != nil {
		t.Fatalf("ImportDigest(sample): %v", err)
	}

	if spec, err := builder.SourceDigest(config); err != nil || spec == digest {
		t.Errorf("SourceDigest(sample) = %q, %v, want another digest than ImportDigest(sample) = %q", spec, err, digest)
	}

	// The topology with its digest, and its annotations without the diagram.
	source := doc.Source
	gotSource := builder.Source{
		Kind: source.Kind, Name: source.Name, Digest: source.Digest, UpdatedAt: source.UpdatedAt,
		ImportedAt: source.ImportedAt, Annotations: source.Annotations,
	}
	wantSource := builder.Source{
		Kind: builder.SourceKindTopology, Name: "sample", Digest: digest, UpdatedAt: config.Metadata.Updated,
		Annotations: map[string]string{"owner": "ops"},
	}

	if diff := cmp.Diff(wantSource, gotSource); diff != "" {
		t.Errorf("FromLegacyTopology(sample) source mismatch (-want +got):\n%s", diff)
	}

	bare, _ := convertLegacy(t, string(legacyFile(t, "sample.xml")), "sample")
	if diff := canvasDiff(bare, doc); diff != "" {
		t.Errorf("FromLegacyTopology(sample) canvas mismatch with FromLegacy(sample.xml) (-want +got):\n%s", diff)
	}

	if doc.Metadata.ID != bare.Metadata.ID {
		t.Errorf("FromLegacyTopology(sample) ID = %q, want %q as FromLegacy(sample.xml) gives", doc.Metadata.ID, bare.Metadata.ID)
	}
}

// TestLegacyCells converts the diagrams of testdata/legacy/cells, which hold every kind of cell,
// against their golden documents and the warnings each gives (TestLegacyVariables has those of the
// variables diagrams).
func TestLegacyCells(t *testing.T) {
	checks := map[string]struct {
		warnings []string
		extra    func(t *testing.T, doc *builder.Document)
	}{
		"devices": {
			warnings: []string{
				`topology node at index 14 duplicates the hostname of the node at index 13 ("DUP"). The Builder skipped it`,
				"topology node at index 15 has no hostname. The Builder skipped it",
				`The Builder renamed node "web server 1" to "web-server-1": a hostname cannot hold spaces.`,
				`The Builder renamed node "from label" to "from-label": a hostname cannot hold spaces.`,
			},
			extra: func(t *testing.T, doc *builder.Document) {
				t.Helper()

				// The settings name the node, and the label does only when they name none.
				node := nodeByHostname(t, doc, "edge-1")
				if node.Label != "edge-1" {
					t.Errorf("FromLegacy(devices) edge-1 label = %q, want edge-1", node.Label)
				}

				if got := node.Device.Spec["general"].(map[string]any)["hostname"]; got != "edge-1" {
					t.Errorf("FromLegacy(devices) edge-1 spec hostname = %v, want edge-1", got)
				}

				if spec := nodeByHostname(t, doc, "from-label").Device.Spec; spec["general"].(map[string]any)["hostname"] != "from-label" {
					t.Errorf("FromLegacy(devices) from-label spec = %v, want the hostname written into it", spec)
				}
			},
		},
		"switches": {
			warnings: []string{
				"VLAN ID 5000 of network v5000 is not a number from 1 to 4094. The Builder did not import it.",
				"VLAN ID -1 of network vneg is not a number from 1 to 4094. The Builder did not import it.",
				"VLAN ID 1.5 of network vfrac is not a number from 1 to 4094. The Builder did not import it.",
				"Network twin has more than one VLAN ID in the legacy diagram. The Builder kept the first, 300.",
				"VLAN ID 101 occurs on networks v101 and dupid. The Builder kept it only for v101.",
				"1 item has settings that the Builder could not read. The Builder did not import it: cell 23.",
				`The Builder renamed network "my lan" to "my-lan": a network name cannot hold spaces.`,
			},
			extra: func(t *testing.T, doc *builder.Document) {
				t.Helper()

				// A switch without a VLAN name takes its label.
				if doc.NetworkByName("labelled") == nil {
					t.Errorf("FromLegacy(switches) networks = %s, want one named by the label of its switch", asJSON(t, doc.Networks))
				}
			},
		},
		"links": {warnings: []string{
			"The Builder added a switch for 2 networks that had none in the legacy diagram: p2p and quiet.",
			"The Builder did not import 3 links whose nodes have no interface on that network: beta to nowhere, beta to spare and gamma to nowhere.",
			"The Builder did not import 5 lines that were not network links.",
			legacyDecorations,
		}},
		"decorations": {warnings: []string{"The Builder did not import 1 shape that had no text.", legacyDecorations}},
		"groups":      {warnings: []string{legacyDecorations}},
		"structure": {warnings: []string{
			"The legacy diagram had 2 layers. The Builder merged them into one layer.",
			legacyDecorations,
		}},
		"settings": {warnings: []string{
			"6 items have settings that the Builder could not read. The Builder did not import them: " +
				"a list, a number too large, cell 7, empty, not json and too deep.",
			"The Builder did not import 1 line that was not a network link.",
		}},
	}

	files, err := filepath.Glob(filepath.Join("testdata", "legacy", "cells", "*.xml"))
	if err != nil || len(files) != 10 {
		t.Fatalf("filepath.Glob(testdata/legacy/cells/*.xml) = %d diagrams, %v, want 10", len(files), err)
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".xml")

		t.Run(name, func(t *testing.T) {
			doc, warnings := legacyCells(t, name)

			golden := filepath.Join("cells", name+".golden.json")
			if got, want := legacyGolden(t, golden, doc); got != want {
				t.Errorf("FromLegacy(%s.xml) mismatch with %s, rewrite it with -update-legacy-golden "+
					"once the change is meant (-want +got):\n%s", name, golden, cmp.Diff(want, got))
			}

			check, ok := checks[name]
			if !ok {
				return
			}

			if diff := cmp.Diff(check.warnings, warnings); diff != "" {
				t.Errorf("FromLegacy(%s.xml) warnings mismatch (-want +got):\n%s", name, diff)
			}

			if check.extra != nil {
				check.extra(t, doc)
			}
		})
	}
}

func legacyCells(t *testing.T, name string) (*builder.Document, []string) {
	t.Helper()

	return convertLegacy(t, string(legacyFile(t, filepath.Join("cells", name+".xml"))), name)
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

	wantWarnings := []string{
		"The Builder added a switch for 1 network that had none in the legacy diagram: lan.",
		"3 nodes have interfaces that the Builder could not read. The Builder did not import these interfaces: mixed, object and text.",
	}
	if diff := cmp.Diff(wantWarnings, warnings); diff != "" {
		t.Errorf("FromLegacy(interfaces) warnings mismatch (-want +got):\n%s", diff)
	}

	for hostname, want := range map[string]any{
		"text": nil, "object": nil, "none": nil, "mixed": []any{eth0}, "fine": []any{eth0},
	} {
		t.Run(hostname, func(t *testing.T) {
			network, _ := nodeByHostname(t, doc, hostname).Device.Spec["network"].(map[string]any)

			got, set := network["interfaces"]

			switch {
			case want == nil && set:
				t.Errorf("FromLegacy(interfaces) %s interfaces = %s, want none", hostname, asJSON(t, got))
			case want != nil:
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("FromLegacy(interfaces) %s interfaces mismatch (-want +got):\n%s", hostname, diff)
				}
			}

			// The rest of the settings stay.
			if _, kept := network["routes"]; !kept {
				t.Errorf("FromLegacy(interfaces) %s network = %s, want its routes kept", hostname, asJSON(t, network))
			}
		})
	}

	if lan := legacySwitchesOf(doc, "lan"); len(lan) == 0 {
		t.Errorf("FromLegacy(interfaces) has no switch of lan, want one")
	} else if diff := cmp.Diff(map[string]string{"eth0": lan[0].ID}, legacyTargets(t, doc, "mixed")); diff != "" {
		t.Errorf("FromLegacy(interfaces) connections of mixed mismatch (-want +got):\n%s", diff)
	}

	// The device of one such node is named in the warning by its hostname.
	_, warnings = convertLegacy(t, legacyModel(device("2", "only", "x")), "interfaces")

	want := []string{"1 node has interfaces that the Builder could not read. The Builder did not import these interfaces: only."}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("FromLegacy(interfaces of only) warnings mismatch (-want +got):\n%s", diff)
	}
}

func TestLegacyVariables(t *testing.T) {
	const (
		unknown  = "2 placeholders have no value in the diagram. The Builder kept them as text: $OTHER_UNKNOWN and $UNKNOWN."
		defaults = "Used the legacy defaults for variables the diagram does not define: " +
			"$DEFAULT_MEMORY = 2048, $DEFAULT_ROUTER_IMAGE = vyos.qc2 and $DEFAULT_VM_IMAGE = ubuntu.qc2."
		allDefaults = "Used the legacy defaults for variables the diagram does not define: " +
			"$DEFAULT_MEMORY = 2048, $DEFAULT_ROUTER_IMAGE = vyos.qc2, $DEFAULT_VCPU = 1 and $DEFAULT_VM_IMAGE = ubuntu.qc2."
		allUnknown = "5 placeholders have no value in the diagram. The Builder kept them as text: " +
			"$COUNT, $DEFAULT_VM, $MY_IMAGE, $OTHER_UNKNOWN and $UNKNOWN."
	)

	for name, want := range map[string][]string{
		"variables":           {defaults, unknown},
		"variables-missing":   {allDefaults, allUnknown},
		"variables-malformed": {allDefaults, allUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			_, warnings := legacyCells(t, name)

			if diff := cmp.Diff(want, warnings); diff != "" {
				t.Errorf("FromLegacy(%s.xml) warnings mismatch (-want +got):\n%s", name, diff)
			}
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
		t.Fatalf("FromLegacy(forms) = %s, want 2 nodes and VLAN 7 for lan", asJSON(t, want))
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
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("FromLegacy(forms with %s) mismatch (-want +got):\n%s", name, diff)
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
				t.Errorf("FromLegacy(%s) = %s, want no nodes, networks or edges", name, asJSON(t, doc))
			}

			if diff := cmp.Diff([]string{"The diagram has no nodes."}, warnings); diff != "" {
				t.Errorf("FromLegacy(%s) warnings mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// TestDecodeLegacyRefusals checks the reason and exact message of each refusal, which repeats
// nothing of the input but a line number or the name of the root element.
func TestDecodeLegacyRefusals(t *testing.T) {
	const (
		notDiagram = "this is not a legacy Builder diagram. A legacy diagram is mxGraph XML, " +
			"or a Topology config with the builder-xml annotation"
		doctype = "the legacy diagram has a DOCTYPE or entity declaration, which the Builder does not accept"
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
				t.Fatalf("DecodeLegacy() = %v, %v, want a LegacyError", diagram, err)
			}

			want := &builder.LegacyError{Reason: test.reason, Message: test.message}
			if diff := cmp.Diff(want, refusal); diff != "" {
				t.Errorf("DecodeLegacy() refusal mismatch (-want +got):\n%s", diff)
			}

			if err.Error() != test.message {
				t.Errorf("DecodeLegacy() error = %q, want %q", err.Error(), test.message)
			}

			if !errors.Is(err, builder.ErrLegacy) {
				t.Errorf("DecodeLegacy() error = %v, want one wrapping ErrLegacy", err)
			}
		})
	}

	// What is exactly at a bound is read.
	decodeLegacy(t, nested(32))

	if diagram := decodeLegacy(t, "<root>"+strings.Repeat("<mxCell/>", 10000)+"</root>"); diagram.Cells() != 10000 {
		t.Errorf("DecodeLegacy(10000 cells).Cells() = %d, want 10000", diagram.Cells())
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
		t.Fatalf("FromLegacyTopology(site): %v", err)
	}

	if err := checkLegacyDocument(doc, warnings); err != nil {
		t.Errorf("FromLegacyTopology(site): %v", err)
	}

	wantWarnings := []string{
		"2 nodes of the legacy diagram are not in the topology. The Builder did not import them: gone and gone-too.",
		"2 nodes of the topology were not in the legacy diagram. The Builder placed them below the diagram: new-1 and new-2.",
		"The Builder added a switch for 1 network that had none in the legacy diagram: wan.",
		"1 item has settings that the Builder could not read. The Builder did not import it: nobody.",
	}
	if diff := cmp.Diff(wantWarnings, warnings); diff != "" {
		t.Errorf("FromLegacyTopology(site) warnings mismatch (-want +got):\n%s", diff)
	}

	hostnames := make([]string, 0, 4)
	for _, node := range doc.DeviceNodes() {
		hostnames = append(hostnames, node.Device.Hostname)
	}

	if diff := cmp.Diff([]string{"alpha", "beta", "new-1", "new-2"}, hostnames); diff != "" {
		t.Errorf("FromLegacyTopology(site) devices mismatch with the topology (-want +got):\n%s", diff)
	}

	// The diagram places a node and gives it its icon.
	type placed struct {
		IconKey  string
		Position builder.Position
	}

	// The topology's spec, the diagram's position and icon.
	alpha := nodeByHostname(t, doc, "alpha")
	if got := alpha.Device.Spec["hardware"].(map[string]any)["memory"]; got != 4096 {
		t.Errorf("FromLegacyTopology(site) alpha memory = %v, want the topology's 4096", got)
	}

	gotAlpha := placed{IconKey: alpha.Device.IconKey, Position: alpha.Position}
	if diff := cmp.Diff(placed{IconKey: "router", Position: builder.Position{X: 80, Y: 112}}, gotAlpha); diff != "" {
		t.Errorf("FromLegacyTopology(site) alpha mismatch with the diagram (-want +got):\n%s", diff)
	}

	if value, kept := alpha.Device.Spec["device"]; kept {
		t.Errorf("FromLegacyTopology(site) alpha spec device = %v, want no setting of the diagram", value)
	}

	// Beta is placed by its label, with the icon of its image.
	beta := nodeByHostname(t, doc, "beta")
	gotBeta := placed{IconKey: beta.Device.IconKey, Position: beta.Position}

	if diff := cmp.Diff(placed{IconKey: "firewall", Position: builder.Position{X: 80, Y: 512}}, gotBeta); diff != "" {
		t.Errorf("FromLegacyTopology(site) beta mismatch (-want +got):\n%s", diff)
	}

	// What the diagram does not hold is laid out below everything it does,
	// from its left edge.
	wantRow := []builder.Position{{X: 64, Y: 976}, {X: 384, Y: 976}}
	gotRow := []builder.Position{legacyPosition(t, doc, "new-1"), legacyPosition(t, doc, "new-2")}

	if diff := cmp.Diff(wantRow, gotRow); diff != "" {
		t.Errorf("FromLegacyTopology(site) new-1 and new-2 positions mismatch (-want +got):\n%s", diff)
	}

	// The VLAN ID of the diagram is the alias of lan.
	aliases := map[string]int{"lan": legacyAlias(doc, "lan"), "wan": legacyAlias(doc, "wan")}
	if diff := cmp.Diff(map[string]int{"lan": 42, "wan": 0}, aliases); diff != "" {
		t.Errorf("FromLegacyTopology(site) aliases mismatch (-want +got):\n%s", diff)
	}

	// The switch of wan sits between the placed devices of the network.
	// Beta is the only one, so it is moved down until it is clear of beta.
	wan := legacyPositions(legacySwitchesOf(doc, "wan"))
	if diff := cmp.Diff([]builder.Position{{X: 64, Y: 656}}, wan); diff != "" {
		t.Errorf("FromLegacyTopology(site) switches of wan mismatch (-want +got):\n%s", diff)
	}

	digest, err := builder.ImportDigest(config)
	if err != nil {
		t.Fatalf("ImportDigest(site): %v", err)
	}

	// The topology's source, without the diagram among its annotations.
	gotSource := builder.Source{Kind: doc.Source.Kind, Digest: doc.Source.Digest, Annotations: doc.Source.Annotations}
	wantSource := builder.Source{
		Kind: builder.SourceKindTopology, Digest: digest, Annotations: map[string]string{"owner": "ops"},
	}

	if diff := cmp.Diff(wantSource, gotSource); diff != "" {
		t.Errorf("FromLegacyTopology(site) source mismatch (-want +got):\n%s", diff)
	}

	// The topology itself is left as it was.
	if config.Metadata.Annotations[builder.LegacyXMLAnnotation] != diagram {
		t.Error("FromLegacyTopology(site) changed the builder-xml annotation of the config, want it unchanged")
	}

	if diff := cmp.Diff(legacyNode("alpha", "lan"), config.Spec["nodes"].([]any)[0]); diff != "" {
		t.Errorf("FromLegacyTopology(site) changed the config node alpha (-want +got):\n%s", diff)
	}
}

// TestFromLegacyTopologyKeepsPlainImport checks that an unused diagram leaves the plain import: an
// empty one with no warning, and an unreadable one with a warning first.
func TestFromLegacyTopologyKeepsPlainImport(t *testing.T) {
	nodes := []any{legacyNode("alpha", "lan"), legacyNode("alpha"), legacyNode("beta", "lan")}

	_, plainWarnings := documentFromConfig(t, legacyTopology("site", "", nodes...))
	if len(plainWarnings) != 1 {
		t.Fatalf("FromConfig(site) warnings = %q, want one warning of its own", plainWarnings)
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
			"The Builder could not read the legacy diagram of topology site (the legacy diagram is not well-formed XML " +
				"(line 1)). The Builder did not use its layout. The Builder placed the nodes automatically.",
		},
		"something else": {
			"not a diagram",
			"The Builder could not read the legacy diagram of topology site (this is not a legacy Builder diagram. " +
				"A legacy diagram is mxGraph XML, or a Topology config with the builder-xml annotation). " +
				"The Builder did not use its layout. The Builder placed the nodes automatically.",
		},
		"a diagram that is too large": {
			"<root>" + strings.Repeat(" ", builder.MaxLegacyBytes) + "</root>",
			"The Builder could not read the legacy diagram of topology site (the legacy diagram is larger than " +
				"5242880 bytes). The Builder did not use its layout. The Builder placed the nodes automatically.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// The plain import of the same topology: its digest covers the
			// diagram, read or not.
			plain, _ := documentFromConfig(t, legacyTopology("site", test.diagram, nodes...))

			doc, warnings, err := builder.FromLegacyTopology(legacyTopology("site", test.diagram, nodes...))
			if err != nil {
				t.Fatalf("FromLegacyTopology(site): %v", err)
			}

			want := plainWarnings
			if test.warning != "" {
				want = append([]string{test.warning}, plainWarnings...)
			}

			if diff := cmp.Diff(want, warnings); diff != "" {
				t.Errorf("FromLegacyTopology(site) warnings mismatch (-want +got):\n%s", diff)
			}

			if err := checkLegacyDocument(doc, warnings); err != nil {
				t.Errorf("FromLegacyTopology(site): %v", err)
			}

			doc.Source.Warnings = plain.Source.Warnings
			if diff := cmp.Diff(plain, doc); diff != "" {
				t.Errorf("FromLegacyTopology(site) mismatch with FromConfig(site) (-want +got):\n%s", diff)
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
		t.Run(name, func(t *testing.T) {
			want, wantWarnings := documentFromConfig(t, config)

			got, warnings, err := builder.FromLegacyTopology(config)
			if err != nil {
				t.Fatalf("FromLegacyTopology(%s): %v", name, err)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("FromLegacyTopology(%s) mismatch with FromConfig (-want +got):\n%s", name, diff)
			}

			if diff := cmp.Diff(wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("FromLegacyTopology(%s) warnings mismatch with FromConfig (-want +got):\n%s", name, diff)
			}
		})
	}

	if _, _, err := builder.FromLegacyTopology(store.Config{Kind: "Scenario"}); !errors.Is(err, builder.ErrUnsupportedKind) {
		t.Errorf("FromLegacyTopology(Scenario) error = %v, want ErrUnsupportedKind", err)
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
		t.Fatalf("FromLegacyTopology(site): %v", err)
	}

	if err := checkLegacyDocument(doc, warnings); err != nil {
		t.Errorf("FromLegacyTopology(site): %v", err)
	}

	// The included nodes, dns where the diagram has it.
	type included struct {
		IncludedFrom string
		Position     builder.Position
	}

	got := map[string]included{}
	for _, hostname := range []string{"dns", "mail"} {
		node := nodeByHostname(t, doc, hostname)
		got[hostname] = included{IncludedFrom: node.Device.IncludedFrom, Position: node.Position}
	}

	want := map[string]included{
		"dns":  {IncludedFrom: "shared", Position: builder.Position{X: 880, Y: 112}},
		"mail": {IncludedFrom: "shared", Position: builder.Position{X: 80, Y: 448}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FromLegacyTopology(site) included nodes mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"shared"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromLegacyTopology(site) includes mismatch (-want +got):\n%s", diff)
	}

	const placedBelow = "1 node of the topology was not in the legacy diagram. The Builder placed it below the diagram: mail."
	if !containsSubstring(warnings, placedBelow) {
		t.Errorf("FromLegacyTopology(site) warnings = %q, want one containing %q", warnings, placedBelow)
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

	want := []string{
		"The Builder added a switch for 83 networks that had none in the legacy diagram: " +
			"chain, own-00, own-01, own-02, own-03, own-04, own-05, own-06 and 75 more.",
	}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("FromLegacy(clear) warnings mismatch (-want +got):\n%s", diff)
	}

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
				t.Errorf("FromLegacy(clear) places %s at %+v over %s at %+v, want them clear",
					first.node.Label, first.node.Position, second.node.Label, second.node.Position)
			}
		}
	}

	for network, want := range map[string]builder.Position{
		// The middle of the chain is its second device: the switch sits
		// below it.
		"chain": {X: 384, Y: 176},
		// Two VLANs between the same two devices: one switch below the other.
		"pair-1": {X: 192, Y: 48}, "pair-2": {X: 192, Y: 176},
		// In the column a switch moves down past the devices below its own. Those past the last device
		// sit below the column, one under the other.
		"own-16": {X: 3984, Y: 10288}, "own-17": {X: 3984, Y: 10416}, "own-79": {X: 3984, Y: 18352},
		// Those that do not get that far are laid out below everything.
		"own-00": {X: 0, Y: 18672}, "own-15": {X: 960, Y: 19152},
	} {
		if diff := cmp.Diff([]builder.Position{want}, legacyPositions(legacySwitchesOf(doc, network))); diff != "" {
			t.Errorf("FromLegacy(clear) switches of %s mismatch (-want +got):\n%s", network, diff)
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

	// The innermost cell holds nothing, so it is a note. The cell that would be too deep joins an
	// outer group that lies around it.
	groups := legacyNodes(doc, builder.NodeKindGroup)
	if len(groups) != 39 {
		t.Errorf("FromLegacy(deep) has %d groups, want 39", len(groups))
	}

	if deepest != 32 {
		t.Errorf("FromLegacy(deep) nests %d deep, want 32", deepest)
	}

	g31, g32, g33 := groups["group 31"], groups["group 32"], groups["group 33"]
	if g31 == nil || g32 == nil || g33 == nil {
		t.Fatalf("FromLegacy(deep) groups 31, 32 and 33 = %v, %v, %v, want all three", g31, g32, g33)
	}

	if g32.ParentID != g31.ID {
		t.Errorf("FromLegacy(deep) group 32 parent = %q, want group 31 %q", g32.ParentID, g31.ID)
	}

	if g33.ParentID == g32.ID {
		t.Errorf("FromLegacy(deep) group 33 parent = group 32 %q, want an outer group", g33.ParentID)
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
		t.Run(name, func(t *testing.T) {
			config := store.Config{Kind: test.kind, Metadata: store.ConfigMetadata{Name: "lab", Annotations: test.annotations}}

			if got := builder.HasLegacyDiagram(config); got != test.want {
				t.Errorf("HasLegacyDiagram(%s with %v) = %t, want %t", test.kind, test.annotations, got, test.want)
			}
		})
	}

	if builder.LegacyXMLAnnotation != "builder-xml" {
		t.Errorf("LegacyXMLAnnotation = %q, want builder-xml", builder.LegacyXMLAnnotation)
	}

	if !builder.IsBuilderAnnotation(builder.LegacyXMLAnnotation) {
		t.Errorf("IsBuilderAnnotation(%q) = false, want true", builder.LegacyXMLAnnotation)
	}
}

func TestLegacyNodeID(t *testing.T) {
	first := builder.LegacyNodeID(builder.NodeKindSwitch, "20")

	if !builder.IsUUID(first) {
		t.Errorf("IsUUID(LegacyNodeID(switch, 20)) = false for %q, want true", first)
	}

	if again := builder.LegacyNodeID(builder.NodeKindSwitch, "20"); again != first {
		t.Errorf("LegacyNodeID(switch, 20) = %q, then %q, want the same each time", first, again)
	}

	for name, other := range map[string]string{
		"LegacyNodeID(group, 20)":  builder.LegacyNodeID(builder.NodeKindGroup, "20"),
		"LegacyNodeID(switch, 21)": builder.LegacyNodeID(builder.NodeKindSwitch, "21"),
		"SwitchNodeID(20)":         builder.SwitchNodeID("20"),
		"NoteNodeID(legacy/20)":    builder.NoteNodeID("legacy/20"),
	} {
		if other == first {
			t.Errorf("%s = %s, want another ID than LegacyNodeID(switch, 20)", name, other)
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

// checkLegacyConversion returns an error unless any input is refused with a LegacyError or converts,
// alone and over a topology, into the same valid document with no more devices than cells.
func checkLegacyConversion(content []byte, topology store.Config) error {
	diagram, err := builder.DecodeLegacy(content)
	if err != nil {
		var refusal *builder.LegacyError
		if !errors.As(err, &refusal) || refusal.Message == "" || diagram != nil {
			return fmt.Errorf("DecodeLegacy() = %v, %w, want a LegacyError", diagram, err)
		}

		return nil
	}

	encode := func(conversion string, doc *builder.Document, warnings []string, err error) (string, error) {
		if err != nil {
			return "", fmt.Errorf("%s error = %w, want nil", conversion, err)
		}

		if err := checkLegacyDocument(doc, warnings); err != nil {
			return "", fmt.Errorf("%s: %w", conversion, err)
		}

		data, err := builder.Encode(doc)
		if err != nil {
			return "", fmt.Errorf("Encode(%s) = %w, want nil", conversion, err)
		}

		return string(data), nil
	}

	first, firstWarnings, err := builder.FromLegacy(diagram, "fuzz")

	data, err := encode("FromLegacy()", first, firstWarnings, err)
	if err != nil {
		return err
	}

	if devices := len(first.DeviceNodes()); devices > diagram.Cells() {
		return fmt.Errorf("FromLegacy() = %d devices from %d cells, want no more devices than cells", devices, diagram.Cells())
	}

	again, err := builder.DecodeLegacy(content)
	if err != nil {
		return fmt.Errorf("second DecodeLegacy() = %w, want nil", err)
	}

	second, secondWarnings, err := builder.FromLegacy(again, "fuzz")

	secondData, err := encode("FromLegacy() of the second decoding", second, secondWarnings, err)
	if err != nil {
		return err
	}

	if diff := cmp.Diff(data, secondData); diff != "" {
		return fmt.Errorf("FromLegacy() of two decodings of one diagram mismatch (-first +second):\n%s", diff)
	}

	// Converting the same decoded diagram again changes nothing either.
	third, thirdWarnings, err := builder.FromLegacy(diagram, "fuzz")

	thirdData, err := encode("FromLegacy() again", third, thirdWarnings, err)
	if err != nil {
		return err
	}

	if diff := cmp.Diff(data, thirdData); diff != "" {
		return fmt.Errorf("FromLegacy() of one decoded diagram twice mismatch (-first +again):\n%s", diff)
	}

	topology.Metadata.Annotations = store.Annotations{builder.LegacyXMLAnnotation: string(content)}

	merged, mergedWarnings, err := builder.FromLegacyTopology(topology)
	if _, err := encode("FromLegacyTopology()", merged, mergedWarnings, err); err != nil {
		return err
	}

	if got := len(merged.DeviceNodes()); got != 5 {
		return fmt.Errorf("FromLegacyTopology() = %d devices, want the topology's 5", got)
	}

	return nil
}

// FuzzDecodeLegacy feeds arbitrary input to the decoder and the conversion. Run it longer with
// "go test -fuzz=FuzzDecodeLegacy -fuzztime=30s ./types/builder/".
func FuzzDecodeLegacy(f *testing.F) {
	for _, seed := range legacySeeds(f) {
		f.Add(seed)
	}

	topology := legacySampleTopology(f)

	f.Fuzz(func(t *testing.T, content []byte) {
		if err := checkLegacyConversion(content, topology); err != nil {
			t.Error(err)
		}
	})
}

// TestLegacyName checks that a legacy name loses all the white space of
// [unicode.IsSpace] and of a name, so it is a valid network name or hostname.
func TestLegacyName(t *testing.T) {
	for text, want := range map[string]string{
		" my lan ":      "my-lan",
		"my\r\nlan":     "my-lan",
		"my\u00a0lan":   "my-lan",
		"my\u3000lan":   "my-lan",
		"my\ufefflan":   "my-lan",
		"my\u0085lan":   "my-lan",
		"\ufeff\u0085 ": "",
	} {
		if got := builder.LegacyName(text); got != want {
			t.Errorf("LegacyName(%q) = %q, want %q", text, got, want)
		}
	}
}
