package builder_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

// TestTopologyRoundTrip walks a stored topology through the full builder cycle
// (config -> document -> topology spec -> config -> document) and asserts that
// nothing semantic changes along the way.
func TestTopologyRoundTrip(t *testing.T) {
	config := loadConfig(t, "topology.json")

	first, warnings := documentFromConfig(t, config)
	if len(warnings) != 0 {
		t.Fatalf("unexpected import warnings: %v", warnings)
	}

	topology, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if len(topology.Warnings) != 0 {
		t.Fatalf("unexpected mapping warnings: %v", topology.Warnings)
	}

	// The mapped spec must be semantically identical to the stored one, node for
	// node (documents order devices deterministically by hostname).
	for _, hostname := range []string{"router", "host-a", "sensor", "standalone"} {
		want := storedNode(t, config.Spec, hostname)
		got := specNode(t, topology, hostname)

		if asJSON(t, got) != asJSON(t, want) {
			t.Fatalf("node %q changed:\nwant: %s\ngot:  %s",
				hostname, asJSON(t, want), asJSON(t, got))
		}
	}

	if got, want := len(topology.Spec["nodes"].([]any)), len(config.Spec["nodes"].([]any)); got != want {
		t.Fatalf("mapped %d nodes, want %d", got, want)
	}

	republished := store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: config.Metadata.Name},
		Spec:     topology.Spec,
	}

	second, _ := documentFromConfig(t, republished)

	assertSameCanvas(t, first, second)
}

// TestExperimentRoundTrip covers the experiment import path, including VLAN
// aliases and the scenario the experiment names.
func TestExperimentRoundTrip(t *testing.T) {
	config := loadConfig(t, "experiment.json")
	stored := builder.WithScenarioResolver(func(name string) (bool, error) {
		return name == "builder-scenario", nil
	})

	first, _, err := builder.FromConfig(config, stored)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	topology, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	aliases := make(map[string]any, len(topology.VLANAliases))
	for name, alias := range topology.VLANAliases {
		aliases[name] = alias
	}

	scenario, _ := config.Spec["scenario"].(map[string]any)

	republished := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name:        config.Metadata.Name,
			Annotations: config.Metadata.Annotations,
		},
		Spec: map[string]any{
			"topology": topology.Spec,
			"scenario": scenario,
			"vlans":    map[string]any{"aliases": aliases},
		},
	}

	second, _, err := builder.FromConfig(republished, stored)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	assertSameCanvas(t, first, second)

	if !reflect.DeepEqual(first.Scenarios, []string{"builder-scenario"}) ||
		!reflect.DeepEqual(first.Scenarios, second.Scenarios) {
		t.Fatalf("scenarios changed: first %q, second %q", first.Scenarios, second.Scenarios)
	}

	wantAliases := map[string]int{"EXP": 101, "RESERVED": 250}

	if !reflect.DeepEqual(topology.VLANAliases, wantAliases) {
		t.Fatalf("VLAN aliases = %v, want %v", topology.VLANAliases, wantAliases)
	}
}

// TestDocumentRoundTripThroughJSON ensures a generated document survives
// serialization, strict decoding, and mapping unchanged.
func TestDocumentRoundTripThroughJSON(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	first, _ := documentFromConfig(t, config)

	data, err := builder.Encode(first)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !reflect.DeepEqual(first, decoded) {
		t.Fatalf("JSON round trip changed the document:\nbefore: %s\nafter: %s",
			asJSON(t, first), asJSON(t, decoded))
	}

	before, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	after, err := decoded.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if asJSON(t, before.Spec) != asJSON(t, after.Spec) {
		t.Fatalf("mapped spec changed across the JSON round trip:\nbefore: %s\nafter: %s",
			asJSON(t, before.Spec), asJSON(t, after.Spec))
	}
}

// TestLayoutAndRouteRoundTripUnpublished covers the presentation a layout
// leaves in a document: the draft's own layout choice and each edge's route
// survive encoding and decoding, and the published topology does not change.
func TestLayoutAndRouteRoundTripUnpublished(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	plain, _ := documentFromConfig(t, config)
	laid, _ := documentFromConfig(t, config)

	if len(laid.Edges) == 0 {
		t.Fatal("the fixture has no edges to route")
	}

	laid.Layout = "cards"
	laid.Edges[0].Route = []builder.Position{{X: 10, Y: 20}, {X: 60, Y: 20}, {X: 60, Y: 80.5}}

	data, err := builder.Encode(laid)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !reflect.DeepEqual(laid, decoded) {
		t.Fatalf("JSON round trip changed the document:\nbefore: %s\nafter: %s",
			asJSON(t, laid), asJSON(t, decoded))
	}

	want, err := plain.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	got, err := decoded.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if asJSON(t, got.Spec) != asJSON(t, want.Spec) {
		t.Fatalf("layout or route reached the topology:\nwant: %s\ngot:  %s",
			asJSON(t, want.Spec), asJSON(t, got.Spec))
	}

	// Absent, neither is written.
	encoded, err := builder.Encode(plain)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for _, key := range []string{`"layout"`, `"route"`} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("a document without %s encodes one: %s", key, encoded)
		}
	}
}

// TestDecoratedDocumentRoundTrip covers what decorates a document (see
// decorationKeys): a document that uses every such field is valid, and
// survives encoding and decoding byte for byte.
func TestDecoratedDocumentRoundTrip(t *testing.T) {
	doc := decoratedDocument(t)

	if err := doc.Validate(); err != nil {
		t.Fatalf("a document with every field is refused: %v", err)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for _, key := range append([]string{`"description": "first rack\nsecond line"`}, decorationKeys...) {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the encoding has no %s: %s", key, data)
		}
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !reflect.DeepEqual(doc, decoded) {
		t.Fatalf("JSON round trip changed the document:\nbefore: %s\nafter: %s",
			asJSON(t, doc), asJSON(t, decoded))
	}

	again, err := builder.Encode(decoded)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if !bytes.Equal(again, data) {
		t.Fatalf("the document encodes differently after a round trip:\nbefore: %s\nafter: %s", data, again)
	}
}

// TestFixtureEncodingsArePinned pins the canonical encoding of the document
// fixtures, which use none of those fields. A stored document is named by
// the digest of this encoding, so a field of the document model must be left
// out when it is not set: one written empty would change the bytes, and the
// digest, of every document there is. A digest here changes only with its
// fixture file.
func TestFixtureEncodingsArePinned(t *testing.T) {
	for name, want := range map[string]struct {
		size   int
		digest string
	}{
		"document.json":        {4696, "sha256:9934fdca85f47e1127b06792887317b4d309fa6e73bf275e33f135bed46273d7"},
		"strict-document.json": {3743, "sha256:891f1ec9c5695582dec1ffcfe6f1d79dcb5bd58341af1ce38f9af45045a0a8aa"},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := builder.Encode(loadDocumentFixture(t, name))
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			sum := sha256.Sum256(data)

			if got := "sha256:" + hex.EncodeToString(sum[:]); len(data) != want.size || got != want.digest {
				t.Fatalf("the encoding is %d bytes with digest %s, want %d bytes with %s:\n%s",
					len(data), got, want.size, want.digest, data)
			}

			for _, key := range decorationKeys {
				if strings.Contains(string(data), key) {
					t.Fatalf("a document without %s encodes one: %s", key, data)
				}
			}
		})
	}

	// Set and then emptied, each field is left out again.
	doc := decoratedDocument(t)
	plain := loadDocumentFixture(t, "document.json")

	doc.Icons = map[string]builder.Icon{}
	doc.Templates = []builder.Template{}
	doc.Source = plain.Source
	doc.Networks[0].LineStyle = ""
	doc.Edges[0].LineStyle = ""

	for i := range doc.Nodes {
		doc.Nodes[i] = plain.Nodes[i]
	}

	want, err := builder.Encode(plain)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if got, err := builder.Encode(doc); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("empty fields are written (%v):\n%s", err, got)
	}
}

// assertSameCanvas compares the semantic canvas content of two documents.
func assertSameCanvas(t *testing.T, first, second *builder.Document) {
	t.Helper()

	if !reflect.DeepEqual(first.Nodes, second.Nodes) {
		t.Fatalf("nodes changed:\nfirst:  %s\nsecond: %s",
			asJSON(t, first.Nodes), asJSON(t, second.Nodes))
	}

	if !reflect.DeepEqual(first.Networks, second.Networks) {
		t.Fatalf("networks changed:\nfirst:  %s\nsecond: %s",
			asJSON(t, first.Networks), asJSON(t, second.Networks))
	}

	if !reflect.DeepEqual(first.Edges, second.Edges) {
		t.Fatalf("edges changed:\nfirst:  %s\nsecond: %s",
			asJSON(t, first.Edges), asJSON(t, second.Edges))
	}
}
