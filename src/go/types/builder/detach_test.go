package builder_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
		t.Fatalf("dns IncludedFrom before CombineIncludes() = %q, want lab-dns", got)
	}

	doc.CombineIncludes()

	// Nothing but the marks and the includes changes: the source still names
	// the topology, and the nodes are where they were.
	want := keptDocument(t)
	want.Metadata.Name = "site"
	want.Source.Kind = builder.SourceKindTopology
	want.Source.Name = "site"
	want.Source.IncludeTopologies = []string{"site-b", "/srv/extra.yml"}
	want.Source.UnresolvedIncludes = nil

	for i := range want.Nodes {
		if device := want.Nodes[i].Device; device != nil {
			device.IncludedFrom = ""
		}
	}

	if diff := cmp.Diff(want, doc); diff != "" {
		t.Errorf("CombineIncludes() mismatch (-want +got):\n%s", diff)
	}

	if cmp.Equal(keptDocument(t).Nodes, doc.Nodes) {
		t.Error("CombineIncludes() left the nodes unchanged, want their marks removed")
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the combined document = %v, want nil", err)
	}

	// The combined document is the one an import that combines generates.
	root, load := combineFixture()
	generated, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())

	if diff := cmp.Diff(generated.Nodes, doc.Nodes); diff != "" {
		t.Errorf("nodes of CombineIncludes() and of an import that combines mismatch (-import +combined):\n%s", diff)
	}

	if diff := cmp.Diff(generated.Edges, doc.Edges); diff != "" {
		t.Errorf("edges of CombineIncludes() and of an import that combines mismatch (-import +combined):\n%s", diff)
	}

	if diff := cmp.Diff(generated.Source.IncludeTopologies, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("includes of CombineIncludes() and of an import that combines mismatch (-import +combined):\n%s", diff)
	}
}

// TestCombineIncludesWithoutUnresolved asserts a document whose includes
// were all read, or that has no source at all, includes nothing afterwards.
func TestCombineIncludesWithoutUnresolved(t *testing.T) {
	root := includeFixture("site", []string{"lab-dns"}, map[string]string{"gateway": "EXP"})
	dns := includeFixture("lab-dns", nil, map[string]string{"dns": "MGMT"})

	doc, _ := documentFromConfigWith(t, root, builder.WithTopologyLoader(storeLoader(dns)))
	doc.CombineIncludes()

	if doc.Source.IncludeTopologies != nil {
		t.Errorf("CombineIncludes() includes = %v, want none", doc.Source.IncludeTopologies)
	}

	if got := nodeByHostname(t, doc, "dns").Device.IncludedFrom; got != "" {
		t.Errorf("CombineIncludes() dns IncludedFrom = %q, want none", got)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the combined document = %v, want nil", err)
	}

	manual := builder.NewDocument("drawn")
	manual.CombineIncludes()

	if manual.Source != nil {
		t.Errorf("CombineIncludes() of a document without a source: source = %s, want none", asJSON(t, manual.Source))
	}
}

// TestDetach unlinks a generated document from its config: it takes the new name and its ID, and
// its source keeps only what does not name the config.
func TestDetach(t *testing.T) {
	doc := keptDocument(t)

	source := *doc.Source
	if source.Name == "" || source.APIVersion == "" || source.Digest == "" || source.UpdatedAt == "" ||
		len(source.Annotations) == 0 || len(source.Warnings) == 0 {
		t.Fatalf("source = %s, want every field a detached document loses set", asJSON(t, source))
	}

	wantMetadata := doc.Metadata
	wantMetadata.Name = "site-copy"
	wantMetadata.ID = builder.DocumentID("site-copy")

	doc.Detach("site-copy")

	if diff := cmp.Diff(wantMetadata, doc.Metadata); diff != "" {
		t.Errorf("Detach(site-copy) metadata mismatch (-want +got):\n%s", diff)
	}

	want := &builder.Source{
		Kind:               builder.SourceKindManual,
		ImportedAt:         "2026-03-05T00:00:00Z",
		IncludeTopologies:  []string{"corp-services", "site-b", "/srv/extra.yml"},
		UnresolvedIncludes: []string{"site-b", "/srv/extra.yml"},
		Warnings:           source.Warnings,
	}
	if diff := cmp.Diff(want, doc.Source); diff != "" {
		t.Errorf("Detach(site-copy) source mismatch (-want +got):\n%s", diff)
	}

	// A copy keeps its included nodes read only.
	if diff := cmp.Diff(keptDocument(t).Nodes, doc.Nodes); diff != "" {
		t.Errorf("Detach(site-copy) nodes mismatch (-want +got):\n%s", diff)
	}

	if got := nodeByHostname(t, doc, "dns").Device.IncludedFrom; got != "lab-dns" {
		t.Errorf("Detach(site-copy) dns IncludedFrom = %q, want lab-dns", got)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the detached document = %v, want nil", err)
	}

	// It publishes as a topology of its own, with the includes it had.
	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 1 {
		t.Errorf("ToTopology() = %d nodes, want the one own node: %s", len(nodes), asJSON(t, topology.Spec))
	}

	if diff := cmp.Diff(want.IncludeTopologies, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("ToTopology() includeTopologies mismatch (-want +got):\n%s", diff)
	}
}

// TestDetachCombined checks that a detached combined document has the source "Combine into one new
// topology" makes: manual, with the kept includes and no unresolved ones.
func TestDetachCombined(t *testing.T) {
	root, load := combineFixture()

	doc, warnings := documentFromConfigWith(t, root, builder.WithTopologyLoader(load), builder.WithCombinedIncludes())
	doc.Detach("site-combined")

	want := &builder.Source{
		Kind:              builder.SourceKindManual,
		IncludeTopologies: []string{"site-b", "/srv/extra.yml"},
		Warnings:          warnings,
	}
	if diff := cmp.Diff(want, doc.Source); diff != "" {
		t.Errorf("Detach(site-combined) source mismatch (-want +got):\n%s", diff)
	}

	if got, want := doc.Metadata.ID, builder.DocumentID("site-combined"); got != want {
		t.Errorf("Detach(site-combined) ID = %q, want %q", got, want)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the detached document = %v, want nil", err)
	}
}

// TestNoteCopiedFrom asserts the note that names the config of a copy comes
// after the other diagram notes, once, and only when it is a valid note.
func TestNoteCopiedFrom(t *testing.T) {
	doc := builder.NewDocument("drawn")
	doc.Metadata.Notes = []string{"Owned by the water team."}

	if !doc.NoteCopiedFrom("riverside-water") {
		t.Error("NoteCopiedFrom(riverside-water) = false, want true")
	}

	if doc.NoteCopiedFrom("riverside-water") {
		t.Error("NoteCopiedFrom(riverside-water) a second time = true, want false")
	}

	want := []string{"Owned by the water team.", "Copied from riverside-water"}
	if diff := cmp.Diff(want, doc.Metadata.Notes); diff != "" {
		t.Errorf("NoteCopiedFrom(riverside-water) notes mismatch (-want +got):\n%s", diff)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of the document with the note = %v, want nil", err)
	}

	full := builder.NewDocument("full")
	for i := range builder.MaxDiagramNotes {
		full.Metadata.Notes = append(full.Metadata.Notes, fmt.Sprintf("note %d", i))
	}

	// Each document is named for the case.
	for _, test := range []struct {
		name string
		doc  *builder.Document
	}{
		{name: "", doc: builder.NewDocument("blank")},
		{name: " ", doc: builder.NewDocument("space")},
		{name: "bad\x00name", doc: builder.NewDocument("control")},
		{name: strings.Repeat("n", builder.MaxDiagramNoteBytes), doc: builder.NewDocument("long")},
		{name: "site", doc: full},
	} {
		t.Run(test.doc.Metadata.Name, func(t *testing.T) {
			before := slices.Clone(test.doc.Metadata.Notes)

			if test.doc.NoteCopiedFrom(test.name) {
				t.Errorf("NoteCopiedFrom(%.20q) = true, want false", test.name)
			}

			if diff := cmp.Diff(before, test.doc.Metadata.Notes); diff != "" {
				t.Errorf("NoteCopiedFrom(%.20q) notes mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}
}

// TestDetachWithoutSource renames a document that names no config and gives
// it no source.
func TestDetachWithoutSource(t *testing.T) {
	doc := builder.NewDocument("drawn")
	doc.Detach("redrawn")

	if diff := cmp.Diff(builder.NewDocument("redrawn"), doc); diff != "" {
		t.Errorf("Detach(redrawn) mismatch (-want +got):\n%s", diff)
	}
}

// TestFromLegacyTopologyCombinesIncludes checks that a topology with a legacy diagram combines its
// includes like any other, and the diagram places the included nodes.
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
		t.Fatalf("FromLegacyTopology(site): %v", err)
	}

	if err := checkLegacyDocument(doc, warnings); err != nil {
		t.Errorf("FromLegacyTopology(site): %v", err)
	}

	for _, hostname := range []string{"dns", "mail"} {
		if got := nodeByHostname(t, doc, hostname).Device.IncludedFrom; got != "" {
			t.Errorf("FromLegacyTopology() %s IncludedFrom = %q, want none", hostname, got)
		}
	}

	if got, want := nodeByHostname(t, doc, "dns").Position, (builder.Position{X: 880, Y: 112}); got != want {
		t.Errorf("FromLegacyTopology() dns position = %+v, want %+v where the diagram has it", got, want)
	}

	if diff := cmp.Diff([]string{"gone"}, doc.Source.IncludeTopologies); diff != "" {
		t.Errorf("FromLegacyTopology() includes mismatch (-want +got):\n%s", diff)
	}

	if doc.Source.UnresolvedIncludes != nil {
		t.Errorf("FromLegacyTopology() unresolved includes = %v, want none", doc.Source.UnresolvedIncludes)
	}

	for _, want := range []string{
		"The Builder copied 2 nodes from included topology shared (2 nodes).",
		"The Builder did not combine included topology gone. It stays in includeTopologies, and publishing keeps the reference.",
		"1 node of the topology was not in the legacy diagram. The Builder placed it below the diagram: mail.",
	} {
		if !containsSubstring(warnings, want) {
			t.Errorf("FromLegacyTopology() warnings = %q, want one containing %q", warnings, want)
		}
	}
}
