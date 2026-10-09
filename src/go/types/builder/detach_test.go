package builder_test

import (
	"reflect"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

// keptDocument imports the combine fixture the plain way: its included nodes
// are marked, and the includes that could not be read are listed.
func keptDocument(t *testing.T) *builder.Document {
	t.Helper()

	root, load := combineFixture()
	root.Metadata.Annotations = store.Annotations{"owner": "alice"}
	root.Metadata.Updated = "2026-03-04T05:06:07Z"

	doc, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(load))
	doc.Source.ImportedAt = "2026-03-05T00:00:00Z"

	return doc
}

// TestCombineIncludes makes the included nodes of a plain import the
// document's own, and leaves it including only what it does not hold.
func TestCombineIncludes(t *testing.T) {
	doc := keptDocument(t)

	if got := nodeByHostname(t, doc, "dns").Device.IncludedFrom; got != "lab-dns" {
		t.Fatalf("dns is included from %q before combining, want lab-dns", got)
	}

	before := asJSON(t, doc.Nodes)

	doc.CombineIncludes()

	for _, node := range doc.DeviceNodes() {
		if node.Device.IncludedFrom != "" {
			t.Errorf("%s is still marked as included from %q", node.Device.Hostname, node.Device.IncludedFrom)
		}
	}

	if want := []string{"site-b", "/srv/extra.yml"}; !reflect.DeepEqual(doc.Source.IncludeTopologies, want) {
		t.Errorf("included topologies = %v, want %v", doc.Source.IncludeTopologies, want)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("unresolved includes = %v, want none", doc.Source.UnresolvedIncludes)
	}

	// Nothing but the marks changes: the source still names the topology,
	// and the nodes are where they were.
	if doc.Source.Kind != builder.SourceKindTopology || doc.Source.Name != "site" || doc.Metadata.Name != "site" {
		t.Errorf("source = %s, name = %q, want the topology still named", asJSON(t, doc.Source), doc.Metadata.Name)
	}

	unmarked := keptDocument(t)
	for i := range unmarked.Nodes {
		if device := unmarked.Nodes[i].Device; device != nil {
			device.IncludedFrom = ""
		}
	}

	if after := asJSON(t, doc.Nodes); after != asJSON(t, unmarked.Nodes) || after == before {
		t.Errorf("nodes after combining = %s, want those of the import without their marks", after)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("combined document is invalid: %v", err)
	}

	// The combined document is the one an import that combines generates.
	root, load := combineFixture()
	generated, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())

	if !reflect.DeepEqual(doc.Nodes, generated.Nodes) || !reflect.DeepEqual(doc.Edges, generated.Edges) ||
		!reflect.DeepEqual(doc.Source.IncludeTopologies, generated.Source.IncludeTopologies) {
		t.Errorf("combined afterwards = %s, combined on import = %s", asJSON(t, doc), asJSON(t, generated))
	}
}

// TestCombineIncludesWithoutUnresolved asserts a document whose includes
// were all read, or that has no source at all, includes nothing afterwards.
func TestCombineIncludesWithoutUnresolved(t *testing.T) {
	root := includeFixture("site", []string{"lab-dns"}, map[string]string{"gateway": "EXP"})
	dns := includeFixture("lab-dns", nil, map[string]string{"dns": "MGMT"})

	doc, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(storeLoader(dns)))
	doc.CombineIncludes()

	if doc.Source.IncludeTopologies != nil || nodeByHostname(t, doc, "dns").Device.IncludedFrom != "" {
		t.Errorf("includes = %v, dns = %s, want no include and no mark",
			doc.Source.IncludeTopologies, asJSON(t, nodeByHostname(t, doc, "dns")))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("combined document is invalid: %v", err)
	}

	manual := builder.NewDocument("drawn")
	manual.CombineIncludes()

	if manual.Source != nil {
		t.Errorf("source = %s, want a document without one left without one", asJSON(t, manual.Source))
	}
}

// TestDetach unlinks a generated document from its config: it takes the new
// name and that name's ID, and its source keeps only what does not name the
// config.
func TestDetach(t *testing.T) {
	doc := keptDocument(t)

	source := *doc.Source
	if source.Name == "" || source.APIVersion == "" || source.Digest == "" || source.UpdatedAt == "" ||
		len(source.Annotations) == 0 || len(source.Warnings) == 0 {
		t.Fatalf("source = %s, want every field a detached document loses set", asJSON(t, source))
	}

	nodes := asJSON(t, doc.Nodes)

	doc.Detach("site-copy")

	if doc.Metadata.Name != "site-copy" || doc.Metadata.ID != builder.DocumentID("site-copy") {
		t.Errorf("name = %q, id = %q, want site-copy and its document ID", doc.Metadata.Name, doc.Metadata.ID)
	}

	want := &builder.Source{
		Kind:               builder.SourceKindManual,
		ImportedAt:         "2026-03-05T00:00:00Z",
		IncludeTopologies:  []string{"corp-services", "site-b", "/srv/extra.yml"},
		UnresolvedIncludes: []string{"site-b", "/srv/extra.yml"},
		Warnings:           source.Warnings,
	}
	if !reflect.DeepEqual(doc.Source, want) {
		t.Errorf("source = %s, want %s", asJSON(t, doc.Source), asJSON(t, want))
	}

	// A copy keeps its included nodes read only.
	if asJSON(t, doc.Nodes) != nodes || nodeByHostname(t, doc, "dns").Device.IncludedFrom != "lab-dns" {
		t.Errorf("nodes = %s, want them unchanged", asJSON(t, doc.Nodes))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("detached document is invalid: %v", err)
	}

	// It publishes as a topology of its own, with the includes it had.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 1 ||
		!reflect.DeepEqual(topology.Spec["includeTopologies"], want.IncludeTopologies) {
		t.Errorf("published topology = %s, want the one own node and the three includes", asJSON(t, topology.Spec))
	}
}

// TestDetachCombined asserts a combined document that is detached has the
// source the Import dialog's "Combine into one new topology" makes: manual,
// with the includes that were kept and no unresolved ones.
func TestDetachCombined(t *testing.T) {
	root, load := combineFixture()

	doc, warnings := documentFromConfigWith(t, root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())
	doc.Detach("site-combined")

	want := &builder.Source{
		Kind:              builder.SourceKindManual,
		IncludeTopologies: []string{"site-b", "/srv/extra.yml"},
		Warnings:          warnings,
	}
	if !reflect.DeepEqual(doc.Source, want) {
		t.Errorf("source = %s, want %s", asJSON(t, doc.Source), asJSON(t, want))
	}

	if doc.Metadata.ID != builder.DocumentID("site-combined") {
		t.Errorf("id = %q, want the ID of a document named site-combined", doc.Metadata.ID)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("detached document is invalid: %v", err)
	}
}

// TestDetachWithoutSource renames a document that names no config and gives
// it no source.
func TestDetachWithoutSource(t *testing.T) {
	doc := builder.NewDocument("drawn")
	doc.Detach("redrawn")

	if doc.Metadata.Name != "redrawn" || doc.Metadata.ID != builder.DocumentID("redrawn") || doc.Source != nil {
		t.Errorf("document = %s, want it renamed and without a source", asJSON(t, doc))
	}
}

// TestFromLegacyTopologyCombinesIncludes asserts a topology that carries a
// diagram of the legacy Builder takes the same options as any other: its
// included nodes become its own, placed where the diagram has them.
func TestFromLegacyTopologyCombinesIncludes(t *testing.T) {
	config := legacyTopology("site",
		legacyModel(legacyDevice("2", "alpha", "server", 40, 40), legacyDevice("3", "dns", "server", 440, 40)),
		legacyNode("alpha"))
	config.Spec["includeTopologies"] = []any{"shared", "gone"}

	shared := store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: "shared"},
		Spec: map[string]any{"nodes": []any{legacyNode("dns"), legacyNode("mail")}},
	}

	doc, warnings, err := builder.FromLegacyTopology(config,
		builder.WithTopologyLoader(storeLoader(shared)), builder.WithCombinedIncludes())
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	checkLegacyDocument(t, doc, warnings)

	dns, mail := nodeByHostname(t, doc, "dns"), nodeByHostname(t, doc, "mail")
	if dns.Device.IncludedFrom != "" || mail.Device.IncludedFrom != "" ||
		dns.Position != (builder.Position{X: 880, Y: 112}) {
		t.Errorf("dns = %s, mail = %s, want the document's own nodes, dns where the diagram has it",
			asJSON(t, dns), asJSON(t, mail))
	}

	if !reflect.DeepEqual(doc.Source.IncludeTopologies, []string{"gone"}) || doc.Source.UnresolvedIncludes != nil {
		t.Errorf("includes = %v, unresolved = %v, want only the include that could not be read",
			doc.Source.IncludeTopologies, doc.Source.UnresolvedIncludes)
	}

	for _, want := range []string{
		"Copied 2 nodes from included topology shared (2 nodes).",
		"Included topology gone was not combined and stays in includeTopologies: publishing keeps the reference.",
		"1 node of the topology was not in the legacy diagram and was placed below it: mail.",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("warnings = %q, want one containing %q", warnings, want)
		}
	}
}
