package builder_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types/builder"
)

// TestTopologyRoundTrip walks a stored topology through config, document, spec, config and
// document again, and checks that nothing semantic changes.
func TestTopologyRoundTrip(t *testing.T) {
	config := loadConfig(t, "topology.json")

	first, warnings := documentFromConfig(t, config)
	if len(warnings) != 0 {
		t.Errorf("FromConfig(topology.json) warnings = %v, want none", warnings)
	}

	topology, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if len(topology.Warnings) != 0 {
		t.Errorf("ToTopology() warnings = %v, want none", topology.Warnings)
	}

	// The mapped spec must be semantically identical to the stored one, with
	// the nodes in the same order, which is the order the VMs launch in.
	if diff := cmp.Diff(jsonValue(t, config.Spec["nodes"]), jsonValue(t, topology.Spec["nodes"])); diff != "" {
		t.Errorf("ToTopology() nodes mismatch, want the stored nodes in order (-want +got):\n%s", diff)
	}

	republished := store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: config.Metadata.Name},
		Spec:     topology.Spec,
	}

	second, _ := documentFromConfig(t, republished)

	if diff := canvasDiff(first, second); diff != "" {
		t.Errorf("FromConfig(republished topology) canvas mismatch (-want +got):\n%s", diff)
	}
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
		t.Fatalf("FromConfig(experiment.json): %v", err)
	}

	topology, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
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
		t.Fatalf("FromConfig(republished experiment): %v", err)
	}

	if diff := canvasDiff(first, second); diff != "" {
		t.Errorf("FromConfig(republished experiment) canvas mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"builder-scenario"}, first.Scenarios); diff != "" {
		t.Errorf("FromConfig(experiment.json) scenarios mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(first.Scenarios, second.Scenarios); diff != "" {
		t.Errorf("FromConfig(republished experiment) scenarios mismatch (-want +got):\n%s", diff)
	}

	wantAliases := map[string]int{"EXP": 101, "RESERVED": 250}

	if diff := cmp.Diff(wantAliases, topology.VLANAliases); diff != "" {
		t.Errorf("ToTopology() VLAN aliases mismatch (-want +got):\n%s", diff)
	}
}

// TestDocumentRoundTripThroughJSON ensures a generated document survives
// serialization, strict decoding, and mapping unchanged.
func TestDocumentRoundTripThroughJSON(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	first, _ := documentFromConfig(t, config)

	data, err := builder.Encode(first)
	if err != nil {
		t.Fatalf("Encode(): %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse(Encode()): %v", err)
	}

	if diff := cmp.Diff(first, decoded); diff != "" {
		t.Errorf("Parse(Encode()) mismatch (-want +got):\n%s", diff)
	}

	before, err := first.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	after, err := decoded.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology() of the decoded document: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, before.Spec), jsonValue(t, after.Spec)); diff != "" {
		t.Errorf("ToTopology() spec of the decoded document mismatch (-want +got):\n%s", diff)
	}
}

// TestLayoutAndRouteRoundTripUnpublished checks that the layout and edge routes survive encoding
// and decoding, and do not change the published topology.
func TestLayoutAndRouteRoundTripUnpublished(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	plain, _ := documentFromConfig(t, config)
	laid, _ := documentFromConfig(t, config)

	if len(laid.Edges) == 0 {
		t.Fatal("FromConfig(experiment.json) has no edges, want edges to route")
	}

	laid.Layout = "cards"
	laid.Edges[0].Route = []builder.Position{{X: 10, Y: 20}, {X: 60, Y: 20}, {X: 60, Y: 80.5}}

	data, err := builder.Encode(laid)
	if err != nil {
		t.Fatalf("Encode(laid): %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse(Encode(laid)): %v", err)
	}

	if diff := cmp.Diff(laid, decoded); diff != "" {
		t.Errorf("Parse(Encode(laid)) mismatch (-want +got):\n%s", diff)
	}

	want, err := plain.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	got, err := decoded.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology() with a layout and a route: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, want.Spec), jsonValue(t, got.Spec)); diff != "" {
		t.Errorf("ToTopology() spec with a layout and a route mismatch (-want +got):\n%s", diff)
	}

	// Absent, neither is written.
	encoded, err := builder.Encode(plain)
	if err != nil {
		t.Fatalf("Encode(plain): %v", err)
	}

	for _, key := range []string{`"layout"`, `"route"`} {
		if strings.Contains(string(encoded), key) {
			t.Errorf("Encode(plain) = %s, want no %s", encoded, key)
		}
	}
}

// TestDecoratedDocumentRoundTrip checks that a document using every decoration (decorationKeys) is
// valid and survives encoding and decoding byte for byte.
func TestDecoratedDocumentRoundTrip(t *testing.T) {
	doc := decoratedDocument(t)

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of a document with every decoration = %v, want nil", err)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode(): %v", err)
	}

	for _, key := range append([]string{`"description": "first rack\nsecond line"`}, decorationKeys...) {
		if !strings.Contains(string(data), key) {
			t.Errorf("Encode() = %s, want %s in it", data, key)
		}
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse(Encode()): %v", err)
	}

	if diff := cmp.Diff(doc, decoded); diff != "" {
		t.Errorf("Parse(Encode()) mismatch (-want +got):\n%s", diff)
	}

	again, err := builder.Encode(decoded)
	if err != nil {
		t.Fatalf("Encode(Parse(Encode())): %v", err)
	}

	if diff := cmp.Diff(string(data), string(again)); diff != "" {
		t.Errorf("Encode(Parse(Encode())) mismatch (-want +got):\n%s", diff)
	}
}

// TestFixtureEncodingsArePinned pins the fixture encodings: a stored document is named by their
// digest, so an unset field must be left out. A digest changes only with its fixture.
func TestFixtureEncodingsArePinned(t *testing.T) {
	type encoding struct {
		Size   int
		Digest string
	}

	for name, want := range map[string]encoding{
		"document.json":        {4678, "sha256:d6829934bf4ecd4e8ab479cce7793ae7747b3cef45c649e1f2ea2527ec6931e8"},
		"strict-document.json": {3725, "sha256:eff17128b70afc767adcfa33b03eff19fa83f13a596bfdf61c2f1f1465a09c40"},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := builder.Encode(loadDocumentFixture(t, name))
			if err != nil {
				t.Fatalf("Encode(%s): %v", name, err)
			}

			sum := sha256.Sum256(data)
			got := encoding{Size: len(data), Digest: "sha256:" + hex.EncodeToString(sum[:])}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Encode(%s) mismatch (-want +got):\n%s\nencoding:\n%s", name, diff, data)
			}

			for _, key := range decorationKeys {
				if strings.Contains(string(data), key) {
					t.Errorf("Encode(%s) = %s, want no %s", name, data, key)
				}
			}
		})
	}

	// Set and then emptied, each field is left out again.
	doc := decoratedDocument(t)
	plain := loadDocumentFixture(t, "document.json")

	doc.IconSize = ""
	doc.Icons = map[string]builder.Icon{}
	doc.Templates = []builder.Template{}
	doc.Layers = []builder.Layer{}
	doc.Metadata.Notes = []string{}
	doc.Source = plain.Source
	doc.Networks[0].LineStyle = ""
	doc.Edges[0].LineStyle = ""

	for i := range doc.Nodes {
		doc.Nodes[i] = plain.Nodes[i]
	}

	want, err := builder.Encode(plain)
	if err != nil {
		t.Fatalf("Encode(plain): %v", err)
	}

	got, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode(emptied): %v", err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("Encode(emptied) mismatch with Encode(plain) (-want +got):\n%s", diff)
	}
}

// canvasDiff returns the differences of the semantic canvas content of two documents (-want +got).
func canvasDiff(want, got *builder.Document) string {
	type canvas struct {
		Nodes    []builder.Node
		Networks []builder.Network
		Edges    []builder.Edge
	}

	return cmp.Diff(
		canvas{Nodes: want.Nodes, Networks: want.Networks, Edges: want.Edges},
		canvas{Nodes: got.Nodes, Networks: got.Networks, Edges: got.Edges},
	)
}
