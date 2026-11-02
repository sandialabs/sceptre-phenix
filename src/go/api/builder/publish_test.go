package builder

import (
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types/builder"
)

// TestTopologyHoldsDocument compares a stored topology with what a document
// publishes. A document that does not decode or project vouches for nothing.
func TestTopologyHoldsDocument(t *testing.T) {
	t.Parallel()

	data := testDocument(t, "topo", 0)

	published := func(t *testing.T, name string) *store.Config {
		t.Helper()

		doc, err := builder.Decode(data)
		if err != nil {
			t.Fatalf("decoding the document returned error: %v", err)
		}

		config, _, err := doc.ToTopologyConfig(name)
		if err != nil {
			t.Fatalf("ToTopologyConfig(%s) returned error: %v", name, err)
		}

		return config
	}

	// A note node without its note decodes, but it is not a valid document.
	invalid := builder.NewDocument("topo")
	invalid.Nodes = append(invalid.Nodes, builder.Node{ID: builder.NoteNodeID("topo"), Kind: builder.NodeKindNote})

	unprojected, err := builder.Encode(invalid)
	if err != nil {
		t.Fatalf("encoding the invalid document returned error: %v", err)
	}

	for name, tt := range map[string]struct {
		data     []byte
		topology func(t *testing.T) *store.Config
		want     bool
	}{
		"the topology the document publishes": {
			data: data, topology: func(t *testing.T) *store.Config { t.Helper(); return published(t, "topo") }, want: true,
		},
		"a topology another writer changed": {
			data: data,
			topology: func(t *testing.T) *store.Config {
				t.Helper()

				config := published(t, "topo")
				config.Spec["nodes"] = []any{map[string]any{"general": map[string]any{"hostname": "other"}}}

				return config
			},
		},
		"a document that does not decode": {
			data: []byte("{"), topology: func(t *testing.T) *store.Config { t.Helper(); return published(t, "topo") },
		},
		"a document that does not project": {
			data: unprojected, topology: func(t *testing.T) *store.Config { t.Helper(); return published(t, "topo") },
		},
		"a topology name that names no config": {
			data: data,
			topology: func(t *testing.T) *store.Config {
				t.Helper()

				config := published(t, "topo")
				config.Metadata.Name = "site/topo"

				return config
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := TopologyHoldsDocument(tt.data, tt.topology(t))
			if err != nil {
				t.Fatalf("TopologyHoldsDocument returned error: %v", err)
			}

			if got != tt.want {
				t.Errorf("TopologyHoldsDocument = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestReplaceLegacyDiagram removes the legacy diagram of a topology and says
// whether the server could read it. A topology without one is left as it is.
func TestReplaceLegacyDiagram(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		annotations store.Annotations
		want        builder.Issue
		replaced    bool
	}{
		"no legacy diagram": {
			annotations: store.Annotations{"other": "kept"},
			want:        builder.NewIssue("", "", ""),
			replaced:    false,
		},
		"a legacy diagram the server reads": {
			annotations: store.Annotations{builder.LegacyXMLAnnotation: "<mxGraphModel/>", "other": "kept"},
			want: builder.NewIssue(builder.CodePublishLegacyReplaced, "",
				"This diagram replaced the legacy Builder diagram of topology topo."),
			replaced: true,
		},
		"a legacy diagram the server cannot read": {
			annotations: store.Annotations{builder.LegacyXMLAnnotation: "<mxGraphModel", "other": "kept"},
			want: builder.NewIssue(builder.CodePublishLegacyRemoved, "",
				"The server could not read the legacy Builder diagram of topology topo. The server removed it."),
			replaced: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			topology := &store.Config{
				Version: "phenix.sandia.gov/v1", Kind: "Topology", Spec: nil, Status: nil,
				Metadata: store.ConfigMetadata{Name: "topo", Created: "", Updated: "", Annotations: maps.Clone(tt.annotations)},
			}

			got, replaced := ReplaceLegacyDiagram(topology)
			if replaced != tt.replaced {
				t.Errorf("ReplaceLegacyDiagram() replaced = %t, want %t", replaced, tt.replaced)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ReplaceLegacyDiagram() issue mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(store.Annotations{"other": "kept"}, topology.Metadata.Annotations); diff != "" {
				t.Errorf("ReplaceLegacyDiagram() annotations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
