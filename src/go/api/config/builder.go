package config

import (
	"phenix/store"
)

// BuilderXMLAnnotation is the topology metadata annotation holding the mxGraph
// XML document for a diagram created with the graphical topology Builder.
const BuilderXMLAnnotation = "builder-xml"

// HasBuilderXML reports whether a config carries a Builder diagram.
func HasBuilderXML(c store.Config) bool {
	return c.HasAnnotation(BuilderXMLAnnotation)
}

// BuilderXML returns the Builder diagram stored on a config, reporting whether
// one is present.
func BuilderXML(c store.Config) ([]byte, bool) {
	xml, ok := c.Metadata.Annotations[BuilderXMLAnnotation]
	if !ok {
		return nil, false
	}

	return []byte(xml), true
}
