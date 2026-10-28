package builder

import (
	"fmt"

	"phenix/store"
	"phenix/types/builder"
)

// ReplaceLegacyDiagram removes the legacy Builder diagram of a topology about
// to be written with a Builder document reference. It reports whether there
// was one, with a warning that says if this diagram replaced it or it was
// unreadable.
func ReplaceLegacyDiagram(topology *store.Config) (builder.Issue, bool) {
	diagram, legacy := topology.Metadata.Annotations[builder.LegacyXMLAnnotation]
	if !legacy {
		return builder.NewIssue("", "", ""), false
	}

	delete(topology.Metadata.Annotations, builder.LegacyXMLAnnotation)

	if _, err := builder.DecodeLegacy([]byte(diagram)); err != nil {
		return builder.NewIssue(builder.CodePublishLegacyRemoved, "", fmt.Sprintf(
			"The server could not read the legacy Builder diagram of topology %s. The server removed it.", topology.Metadata.Name,
		)), true
	}

	return builder.NewIssue(builder.CodePublishLegacyReplaced, "", fmt.Sprintf(
		"This diagram replaced the legacy Builder diagram of topology %s.", topology.Metadata.Name,
	)), true
}

// TopologyHoldsDocument reports whether the spec of a stored topology is
// exactly what the document data publishes, so nothing else wrote it since.
// A document that no longer decodes vouches for nothing.
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
