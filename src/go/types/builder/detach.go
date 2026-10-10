package builder

import (
	"slices"
	"strings"
)

// CopiedFromPrefix is the start of the diagram note that names the config a
// copy was made from (see [Document.NoteCopiedFrom]).
const CopiedFromPrefix = "Copied from "

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

// Detach removes the link from the document to the config it was generated
// from, and renames the document. The document takes name and the ID of a
// document of that name (see [DocumentID]). Its source becomes manual and
// names no config. The source keeps [Source.ImportedAt],
// [Source.IncludeTopologies], [Source.UnresolvedIncludes] and
// [Source.Warnings]. Detach removes its name, apiVersion, topology, digest,
// updatedAt and annotations. Thus a publish of a detached document never
// changes the config it came from. Detach only renames a document without a
// source. Detach does not change [Metadata.Notes]. For a note that names the
// config, a caller calls [Document.NoteCopiedFrom].
func (d *Document) Detach(name string) {
	d.Metadata.Name = name
	d.Metadata.ID = DocumentID(name)

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

// NoteCopiedFrom adds the diagram note "Copied from <name>" after the
// other notes in [Metadata.Notes]. The note names the config that a
// detached document was made from, because [Document.Detach] removes that
// name from the source. It adds no note when name is blank, when the
// document already holds the same note or [MaxDiagramNotes] notes, or when
// the note breaks a rule of [Metadata.Notes]. It reports whether it added
// the note.
func (d *Document) NoteCopiedFrom(name string) bool {
	note := CopiedFromPrefix + name

	switch {
	case strings.TrimSpace(name) == "",
		len(d.Metadata.Notes) >= MaxDiagramNotes,
		len(note) > MaxDiagramNoteBytes,
		strings.ContainsFunc(note, isNoteControl),
		slices.Contains(d.Metadata.Notes, note):
		return false
	}

	d.Metadata.Notes = append(d.Metadata.Notes, note)

	return true
}
