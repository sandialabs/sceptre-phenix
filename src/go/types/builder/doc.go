// Package builder implements the versioned, library-independent document model
// used by the phenix Vue Flow topology builder.
//
// The document model is intentionally decoupled from any canvas library: it
// stores plain positions, sizes, and identifiers rather than Vue Flow specific
// structures, so the same document can be rendered by a different front end (or
// no front end at all) without migration.
//
// A [Document] carries three kinds of information:
//
//   - Canvas presentation: node positions/sizes, parent groups, free notes,
//     viewport, and grid settings.
//   - phenix semantics: the complete node spec of every device, canonical
//     networks (VLANs) with optional integer aliases, and the edges that bind
//     device interfaces to networks.
//   - Provenance: where the document came from ([Source]) and an optional
//     reference to a stored or uploaded scenario ([ScenarioRef]).
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
// with crypto.randomUUID.
//
// [FromConfig] also records [SourceDigest] and the source config's
// metadata.updated on the document's [Source], so a publish path can detect a
// working copy built from a stale source config.
//
// Two levels of validation are available:
//
//   - [Document.Validate] validates the draft working copy. It is intentionally
//     tolerant of work in progress, for example interfaces that are not yet
//     connected to a network. Scenario content, however, is complete by
//     construction, so cached or uploaded content is validated against the
//     existing phenix scenario schema for [ScenarioAPIVersion]. A stored
//     scenario reference without cached content is validated when the
//     referenced config is loaded at publish time.
//   - [Document.ValidateTopologyProjection] (and [Document.PublishTopologyConfig])
//     run the existing phenix topology schema validation against the projected
//     config, so publishing authoritatively validates complete node specs.
//
// [Schema] and [SchemaJSON] return a standalone JSON Schema bundle describing
// the persisted document, with the phenix v1 and v2 OpenAPI component schemas
// embedded under $defs so device spec forms and scenario content resolve every
// field without a second fetch. Device specs reference the v1 node schemas;
// scenario content references the v2 Scenario schema, matching
// [ScenarioAPIVersion].
//
// Size limits (node counts, payload sizes, etc.) are deliberately *not*
// enforced here; they belong to the API/transport layer. This package enforces
// structural and semantic correctness only.
package builder
