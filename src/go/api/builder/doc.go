// Package builder implements persistence for the phenix topology builder.
//
// Two kinds of data are persisted, both through the generic
// [phenix/store.RecordStore] primitives (no phenix configs and no broker events
// are created by this package):
//
//   - Drafts: mutable, per-user working documents. A draft is a metadata record
//     (owner, title, provenance, publication state, and an ordered history of
//     snapshot manifests) plus immutable content chunks holding the compressed
//     document bytes of every snapshot.
//   - Published documents: immutable, content addressed copies of the document
//     a config was published from. A compact [DocumentReference] is produced for
//     storage in a topology's "builder-doc" annotation by the caller; this
//     package never writes configs itself.
//
// Concurrency is handled with optimistic concurrency control: every draft
// mutation takes the record revision the caller observed and performs a
// compare-and-swap against the store. Content chunks are always written before
// the metadata compare-and-swap so a failed swap never leaves metadata pointing
// at missing content; chunks written by a failed attempt are cleaned up and any
// cleanup failure is reported to the caller rather than being swallowed.
//
// Authorization is deliberately *not* implemented here. Owner and actor are
// explicit, trusted arguments supplied by the web layer, which is responsible
// for authenticating and authorizing them. The service records the actor of
// every mutation (including cross-user actors) for audit purposes.
package builder
