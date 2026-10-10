package builder

import (
	"fmt"

	"phenix/store"
	"phenix/types/builder"
)

// ReplaceLegacyDiagram removes from a topology that is about to be written
// with a Builder document reference the diagram the legacy Builder kept on
// it (see [builder.LegacyXMLAnnotation]): the topology is a Builder topology
// from then on. It reports whether there was one, with the warning that says
// what became of it: this diagram replaces it
// ([builder.CodePublishLegacyReplaced]), or, when it cannot be read (see
// [builder.DecodeLegacy]), so that an import converted nothing of it, it was
// removed ([builder.CodePublishLegacyRemoved]). Every other annotation is
// left as it is.
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

// TopologyHoldsDocument reports whether a stored topology's spec is exactly
// what the Builder document data publishes as that topology: nothing else
// has written the topology since the document was published to it, or, for a
// document read from a file, the topology is the file's. A document that can
// no longer be decoded, projected or digested vouches for nothing.
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
