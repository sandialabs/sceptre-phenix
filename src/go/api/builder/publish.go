package builder

import (
	"fmt"

	"phenix/store"
	"phenix/types/builder"
)

// ReplaceLegacyDiagram removes the diagram that the legacy Builder kept on a
// topology (see [builder.LegacyXMLAnnotation]). The topology is about to be
// written with a Builder document reference, so it is a Builder topology from
// then on. ReplaceLegacyDiagram reports whether there was a diagram, with a
// warning that says what became of it:
//   - [builder.CodePublishLegacyReplaced]: this diagram replaces it.
//   - [builder.CodePublishLegacyRemoved]: it was removed, because it cannot be
//     read (see [builder.DecodeLegacy]) and so an import converted nothing of
//     it.
//
// It does not change any other annotation.
func ReplaceLegacyDiagram(topology *store.Config) (builder.Issue, bool) {
	diagram, legacy := topology.Metadata.Annotations[builder.LegacyXMLAnnotation]
	if !legacy {
		return builder.NewIssue("", "", ""), false
	}

	delete(topology.Metadata.Annotations, builder.LegacyXMLAnnotation)

	if _, err := builder.DecodeLegacy([]byte(diagram)); err != nil {
		return builder.NewIssue(builder.CodePublishLegacyRemoved, "", fmt.Sprintf(
			"The legacy Builder diagram of topology %s could not be read and was removed.", topology.Metadata.Name,
		)), true
	}

	return builder.NewIssue(builder.CodePublishLegacyReplaced, "", fmt.Sprintf(
		"The legacy Builder diagram of topology %s was replaced by this diagram.", topology.Metadata.Name,
	)), true
}

// TopologyHoldsDocument reports whether the spec of a stored topology is
// exactly what the Builder document data publishes as that topology. Then
// nothing else wrote the topology after the document was published to it. For
// a document read from a file, the topology is the topology of the file. A
// document that can no longer be decoded, projected or digested vouches for
// nothing.
func TopologyHoldsDocument(data []byte, topology *store.Config) (bool, error) {
	name := topology.Metadata.Name

	document, err := builder.Decode(data)
	if err != nil {
		return false, nil //nolint:nilerr // an undecodable document cannot vouch for the topology
	}

	published, _, err := document.ToTopologyConfig(name)
	if err != nil {
		return false, nil //nolint:nilerr // nor can one that no longer projects
	}

	want, err := builder.SourceDigest(*published)
	if err != nil {
		return false, nil //nolint:nilerr // nor can one that cannot be digested
	}

	got, err := builder.SourceDigest(*topology)
	if err != nil {
		return false, fmt.Errorf("digesting topology %s: %w", name, err)
	}

	return got == want, nil
}
