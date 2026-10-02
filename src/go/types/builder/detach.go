package builder

// CombineIncludes makes every included device the document's own and
// replaces [Source.IncludeTopologies] with [Source.UnresolvedIncludes]: the
// document then includes only the topologies whose devices it does not hold,
// so [Document.ToTopology] writes every device and those references. A
// combined document has no unresolved includes left to list.
//
// The topology published from a combined document no longer follows the
// topologies its devices came from, which is why an import that combines
// also detaches the document (see [Document.Detach]).
func (d *Document) CombineIncludes() {
	for i := range d.Nodes {
		if device := d.Nodes[i].Device; device != nil {
			device.IncludedFrom = ""
		}
	}

	if d.Source == nil {
		return
	}

	d.Source.IncludeTopologies = d.Source.UnresolvedIncludes
	d.Source.UnresolvedIncludes = nil
}

// Detach unlinks the document from the config it was generated from and
// renames it: the document takes name and the ID of a document of that name
// (see [DocumentID]), and its source becomes manual, naming no config. The
// source keeps [Source.ImportedAt], [Source.IncludeTopologies],
// [Source.UnresolvedIncludes] and [Source.Warnings]; its name, apiVersion,
// topology, digest, updatedAt and annotations are removed. Publishing a
// detached document therefore never updates the config it came from. A
// document without a source is only renamed.
func (d *Document) Detach(name string) {
	d.Name = name
	d.ID = DocumentID(name)

	if d.Source == nil {
		return
	}

	d.Source = &Source{ //nolint:exhaustruct // a manual source names no config
		Kind:               SourceKindManual,
		ImportedAt:         d.Source.ImportedAt,
		IncludeTopologies:  d.Source.IncludeTopologies,
		UnresolvedIncludes: d.Source.UnresolvedIncludes,
		Warnings:           d.Source.Warnings,
	}
}
