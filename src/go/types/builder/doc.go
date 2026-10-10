// Package builder implements the versioned document model of the phenix Vue
// Flow topology builder. The model does not depend on a canvas library.
//
// The document model stores plain positions, sizes and identifiers, not Vue
// Flow structures. Thus a different front end, or no front end, can render
// the same document without a migration.
//
// A [Document] holds four kinds of information:
//
//   - Metadata ([Metadata]): the identifier, name and description of the
//     document, and notes about the diagram as a whole.
//   - Canvas presentation: node positions and sizes, parent groups, free
//     notes, the notes of switches, the viewport and the grid settings. The
//     colors of nodes, the line style of networks and edges, and the
//     description, border and icon of groups. Custom icons, which nodes name
//     and the icon library of a phenix server holds. A document that must
//     stand on its own, as a downloaded file does, holds them as PNG data
//     under their names ([Icon]). Device templates ([Template]) saved with
//     the diagram.
//   - phenix semantics: the complete node spec of every device, canonical
//     networks (VLANs) with optional integer aliases, the edges that bind
//     device interfaces to networks, and the names of the Scenario configs
//     for the diagram ([Document.Scenarios]). The document names these
//     configs but does not hold them.
//   - Provenance: who made the document and who saved it last, and when
//     ([Provenance], in the metadata), and where the document came from
//     ([Source]).
//
// No part of the metadata or of the canvas presentation goes into a config.
//
// The package has two authoritative transformations:
//
//   - [Document.ToTopology] maps a document to a phenix topology spec and
//     the experiment VLAN alias map. This is the publish direction.
//   - [FromConfig] generates a document from a validated phenix Topology or
//     Experiment [store.Config]. This is the import direction.
//
// Both directions are deterministic. Identifiers and initial positions come
// from stable semantic keys (hostnames, interface names, VLAN names). Thus
// two imports of the same config give identical documents, and a config ->
// document -> topology -> document round trip is stable. Every identifier is
// an RFC 4122 UUID. Generated identifiers are name-based version 5 UUIDs in
// [NamespaceUUID]. The front end makes new identifiers as random (version 4)
// UUIDs.
//
// [FromConfig] also records [ImportDigest] and the metadata.updated of the
// source config on the [Source] of the document. A publish path can use them
// to find a working copy built from a stale source config.
//
// With a [TopologyLoader] ([WithTopologyLoader]), [FromConfig] resolves the
// includeTopologies of a topology and shows the included devices, marked with
// [Device.IncludedFrom]. The topology of an experiment already holds these
// devices, because phenix merged them. Thus [FromConfig] identifies and marks
// them:
//
//   - by the included topology that defines them, or
//   - for an included topology that it cannot read now, as the devices that
//     neither the source topology of the experiment nor a readable included
//     topology defines. The source topology is the topology that phenix
//     created the experiment from.
//
// [Document.ToTopology] does not write marked devices. It writes
// includeTopologies back, so phenix merges these devices in again and a round
// trip does not make duplicates of them. A publish writes an included device
// that an experiment import cannot identify as a device of the topology
// itself. phenix then makes a duplicate of it when it merges the include
// again. This occurs for:
//
//   - every included device, when there is no loader.
//   - the devices of an unreadable included topology, when the source
//     topology of the experiment is also unreadable.
//
// The import gives a warning for both. [CheckIncludes] resolves includes in
// the same way for a publish. It reports included topologies that it cannot
// read, hostnames that the including topology also defines, and hostnames
// that phenix refuses in an experiment.
//
// A document generated from a topology lists on [Source.UnresolvedIncludes]
// the included topologies whose devices it does not hold. With
// [WithCombinedIncludes], [FromConfig] makes the included devices the
// devices of the document ([Document.CombineIncludes]) and keeps only the
// unresolved includes. Thus the published topology holds every node that
// [FromConfig] could read. [Document.Detach] removes the link from a document
// to its source config and gives the document a new name, for an import that
// makes a copy or combines. Its source is then manual, and a publish of it
// never changes that config.
//
// The previous Builder kept each diagram as mxGraph XML in the "builder-xml"
// annotation of its topology ([LegacyXMLAnnotation]). [DecodeLegacy] reads
// such a diagram. The diagram is untrusted input, so [DecodeLegacy] accepts
// only plain XML in fixed bounds, with no DOCTYPE and no entity.
// [FromLegacyTopology] generates the document of a topology that holds such
// a diagram ([HasLegacyDiagram]). The result is the document that
// [FromConfig] generates, with the positions, switches, VLAN IDs, groups and
// notes of the diagram applied to it. The spec of the topology is the truth
// for every node. [FromLegacy] converts a diagram that has no topology, from
// the node settings in the diagram. The source of its document is manual.
// Both functions are as deterministic as [FromConfig]. Both report the
// content of a diagram that a document cannot hold.
//
// Two levels of validation are available:
//
//   - [Document.Validate] validates the draft working copy. It accepts work
//     in progress, for example interfaces that do not connect to a network
//     yet. It validates the form of the scenario names. A publish of the
//     document validates that the Scenario configs exist.
//   - [Document.PublishTopologyConfig] and [Document.PublishTopology] run
//     the phenix topology schema validation on the projected config. Thus a
//     publish authoritatively validates complete node specs.
//     [Document.ExportTopologyConfig] runs the same validation, but returns
//     a config that only a publish refuses, with the reasons.
//
// [Schema] and [SchemaJSON] return a standalone JSON Schema bundle for the
// persisted document. The bundle holds the phenix v1 OpenAPI component
// schemas under $defs, so device spec forms resolve every field without a
// second fetch. Device specs refer to the v1 node schemas. Each definition and
// property of the Builder in the bundle has a title, a description and
// examples.
//
// This package does *not* enforce size limits (node counts, payload sizes
// and so on). The API and transport layer enforces them. This package
// enforces structural and semantic correctness only, and these bounds:
//
//   - the document name. The draft service records it as a title.
//   - the users that the metadata names. Their bound is the same as the bound
//     on the owner of a draft.
//   - the notes of the diagram and of switches ([MaxDiagramNotes],
//     [MaxDiagramNoteBytes]).
//   - the scenarios ([MaxScenarios], [MaxScenarioNameBytes]).
//   - the source config annotations that a document holds only to show them.
//   - the custom icons and the templates that the editor adds
//     ([MaxDocumentIcons], [MaxIconBytes], [MaxIconPixels], [MaxTemplates],
//     [MaxTemplateDeviceBytes]). The editor checks them with the same rules
//     before it saves.
//
// A custom icon is untrusted image data that the browsers of other users
// draw. The package accepts only a small PNG that holds only its pixels
// ([ValidateIconPNG]). [NormalizeIconPNG] makes such a PNG from any PNG: it
// decodes the PNG and encodes it again. This package reads no SVG and no
// other format.
//
// The createdBy, createdAt, updatedBy and updatedAt fields of the metadata
// are ordinary content. This package validates their form ([MaxUserBytes],
// [TimeLayout]) and never sets them. The draft service sets them when it
// stores a snapshot.
package builder
