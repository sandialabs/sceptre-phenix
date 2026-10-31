package builder

import (
	"slices"
	"strings"
)

// CopiedFromPrefix is the start of the diagram note that names the config a
// copy was made from (see [Document.NoteCopiedFrom]).
const CopiedFromPrefix = "Copied from "

// CombineIncludes makes every included device the document's own and keeps
// only [Source.UnresolvedIncludes] as includes. The published topology then no
// longer follows the included topologies, so an import that combines also
// detaches the document.
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

// Detach removes the link from the document to its source config and renames
// it to name, with the matching [DocumentID]. Its source becomes manual, so a
// publish never changes that config. It keeps the import time, includes and
// warnings. It does not change [Metadata.Notes] (see [Document.NoteCopiedFrom]).
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

// NoteCopiedFrom adds the diagram note "Copied from <name>" after the other
// notes, because [Document.Detach] drops that name from the source. It reports
// whether it added the note. It skips a blank name, a repeat, a full list and
// a note that breaks a rule.
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
