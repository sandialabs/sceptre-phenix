package builder_test

import (
	"reflect"
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
// aliases and the scenario reference.
func TestExperimentRoundTrip(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	first, _ := documentFromConfig(t, config)

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

	second, _ := documentFromConfig(t, republished)

	assertSameCanvas(t, first, second)

	if !reflect.DeepEqual(first.Scenario, second.Scenario) {
		t.Fatalf("scenario reference changed:\nfirst:  %s\nsecond: %s",
			asJSON(t, first.Scenario), asJSON(t, second.Scenario))
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
