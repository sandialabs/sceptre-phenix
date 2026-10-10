// Package builder implements the versioned, library-independent document model
// used by the phenix Vue Flow topology builder.
//
// The document model is intentionally decoupled from any canvas library: it
// stores plain positions, sizes, and identifiers rather than Vue Flow specific
// structures, so the same document can be rendered by a different front end (or
// no front end at all) without migration.
//
// A [Document] carries four kinds of information:
//
//   - Metadata ([Metadata]): the document's identifier, name and
//     description, and notes about the diagram as a whole. None of it is
//     ever written to a config.
//   - Canvas presentation: node positions/sizes, parent groups, free notes,
//     the notes of switches, viewport, and grid settings; the colors of
//     nodes, the line style of
//     networks and edges, and the description, border and icon of groups;
//     custom icons, which nodes name and a phenix server's icon library
//     holds, and which a document carries as PNG data under their names
//     ([Icon]) when it is to stand on its own, as a downloaded file does;
//     and device templates ([Template]) saved with the diagram. None of it
//     is ever written to a config.
//   - phenix semantics: the complete node spec of every device, canonical
//     networks (VLANs) with optional integer aliases, the edges that bind
//     device interfaces to networks, and the names of the Scenario configs
//     the diagram is used with ([Document.Scenarios]), which the document
//     names but does not hold.
//   - Provenance: who made the document and who last saved it, and when
//     ([Provenance], kept in the metadata), and where the document came from
//     ([Source]).
//
// Two authoritative transformations are provided:
//
//   - [Document.ToTopology] maps a document to a phenix topology spec (plus the
//     experiment VLAN alias map). This is the publish direction.
//   - [FromConfig] generates a document from a validated phenix Topology or
//     Experiment [store.Config]. This is the import direction.
//
// Both directions are deterministic: identifiers and initial positions are
// derived from stable semantic keys (hostnames, interface names, VLAN names),
// so importing the same config twice yields identical documents and a
// config -> document -> topology -> document round trip is stable. Every
// identifier is an RFC 4122 UUID: generated identifiers are name based version
// 5 UUIDs inside [NamespaceUUID], while the front end mints new identifiers
// as random (version 4) UUIDs.
//
// [FromConfig] also records [ImportDigest] and the source config's
// metadata.updated on the document's [Source], so a publish path can detect a
// working copy built from a stale source config.
//
// Given a [TopologyLoader] ([WithTopologyLoader]), [FromConfig] resolves a
// topology's includeTopologies and shows the included devices, marked with
// [Device.IncludedFrom]. An experiment's topology already holds them, merged
// by phenix, so they are recognized and marked instead: by the included
// topology defining them, or, for an included topology that cannot be read
// now, as the devices that neither the topology the experiment was created
// from nor a readable included topology defines. [Document.ToTopology] leaves
// marked devices out and writes includeTopologies back, so phenix merges them
// in again and a round trip does not duplicate them. An included device an
// experiment import cannot recognize is published as the topology's own and
// duplicated when phenix merges the include again: every one without a
// loader, and those of an unreadable included topology when the topology the
// experiment was created from cannot be read either. The import warns about
// both. [CheckIncludes] resolves includes the same way for a publish, and
// reports included topologies that cannot be read, hostnames the including
// topology defines too, and hostnames phenix refuses in an experiment.
//
// A document generated from a topology lists the included topologies whose
// devices it does not hold on [Source.UnresolvedIncludes]. With
// [WithCombinedIncludes], [FromConfig] makes the included devices the
// document's own instead ([Document.CombineIncludes]) and keeps only those
// unresolved includes, so the published topology holds every node it could
// read. [Document.Detach] unlinks a document from the config it was
// generated from and renames it, for an import that makes a copy or
// combines: its source is then manual, and publishing it never updates
// that config.
//
// The Builder before this one kept each diagram as mxGraph XML in the
// "builder-xml" annotation of its topology ([LegacyXMLAnnotation]).
// [DecodeLegacy] reads such a diagram, which is untrusted input: plain XML
// only, within fixed bounds, with no DOCTYPE and no entity. [FromLegacyTopology]
// then generates the document of a topology that carries one ([HasLegacyDiagram]):
// the document [FromConfig] generates, with the diagram's positions, switches,
// VLAN IDs, groups and notes laid over it, the topology's spec being the
// truth for every node. [FromLegacy] converts a diagram that comes without a
// topology, from the node settings the diagram itself holds; its document's
// source is manual. Both are as deterministic as [FromConfig], and both
// report what a diagram held that a document has no place for.
//
// Two levels of validation are available:
//
//   - [Document.Validate] validates the draft working copy. It is intentionally
//     tolerant of work in progress, for example interfaces that are not yet
//     connected to a network. It checks the form of the scenario names; that
//     the Scenario configs exist is checked when the document is published.
//   - [Document.PublishTopologyConfig] (and [Document.PublishTopology])
//     run the existing phenix topology schema validation against the projected
//     config, so publishing authoritatively validates complete node specs.
//     [Document.ExportTopologyConfig] runs the same checks, but returns a
//     config that only publishing refuses, with the reasons.
//
// [Schema] and [SchemaJSON] return a standalone JSON Schema bundle describing
// the persisted document, with the phenix v1 OpenAPI component schemas
// embedded under $defs so device spec forms resolve every field without a
// second fetch. Device specs reference the v1 node schemas. Every definition
// and property the Builder owns in the bundle has a title, a description and
// examples.
//
// Size limits (node counts, payload sizes, etc.) are deliberately *not*
// enforced here; they belong to the API/transport layer. This package enforces
// structural and semantic correctness only, and the bounds on the document
// name, which the draft service records as a title, on the users the
// metadata names, which are bounded like the owner of a draft, on the
// notes of the diagram and of switches ([MaxDiagramNotes],
// [MaxDiagramNoteBytes]), on the scenarios ([MaxScenarios],
// [MaxScenarioNameBytes]), on the source
// config annotations a document carries only to show them, and on the custom
// icons and the templates the editor adds ([MaxDocumentIcons],
// [MaxIconBytes], [MaxIconPixels], [MaxTemplates],
// [MaxTemplateDeviceBytes]), which it checks by the same rules before it
// saves.
//
// A custom icon is untrusted image data that other users' browsers draw. The
// only form accepted is a small PNG holding nothing but its pixels
// ([ValidateIconPNG]); [NormalizeIconPNG] makes one from any PNG by decoding
// and encoding it again. Nothing here reads SVG or any other format.
//
// The metadata's createdBy, createdAt, updatedBy and updatedAt are ordinary
// content: this package checks their form ([MaxUserBytes], [TimeLayout]) and
// never sets them. The draft service does, when it stores a snapshot.
package builder
