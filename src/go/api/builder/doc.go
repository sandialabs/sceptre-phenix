// Package builder implements persistence for the phenix topology builder.
//
// The package keeps four kinds of data, all through the generic
// [phenix/store.RecordStore] primitives. It creates no broker events and
// stores no phenix config.
//
//   - Drafts: mutable, per-user working documents. A draft is a metadata record
//     (owner, title, provenance, publication state, and an ordered history of
//     snapshot manifests) plus immutable content chunks. The chunks hold the
//     compressed document bytes of every snapshot.
//   - Published documents: immutable, content addressed copies of the document
//     a config was published from. The caller stores a [DocumentReference],
//     the digest and ID of the document, in the "builder-doc" annotation of
//     the topology. The Topology config hook of this package checks that
//     reference in every topology about to be stored.
//   - The icon library: the custom icons every user of the server shares. Each
//     icon has a unique name its uploader chose. The library keeps one record
//     per icon name and per alias, in a namespace of its own (see
//     [Service.AddIcon]). Nodes and templates name icons. A document carries
//     copies only when it must stand on its own, as a downloaded file does.
//     Only the uploader of an icon, or a caller the web layer allows to act on
//     any icon, renames or deletes it (see [Service.RenameIcon],
//     [Service.DeleteIcon]). [Service.CleanupOrphanedChunks] and
//     [Service.CleanupOrphanedDocuments] never list or delete in that
//     namespace. [Service.CleanupLegacyIcons] removes records of the per-user
//     layout of earlier builds.
//   - Template libraries: the device templates a user keeps, with their
//     collections, as one record per user in a namespace of its own (see
//     [TemplateLibrary]). A template names its custom icon, which the icon
//     library resolves. A user who never changed the library has no record and
//     gets the built-in templates. The first change stores them with the
//     record, so a deleted built-in template stays deleted until the owner
//     restores it (see [TemplateLibrary.RestoreBuiltins]). Every change is one
//     compare-and-swap of that record (see [Service.UpdateLibrary]). An owner
//     may share items, read only, with other users, or publish them to every
//     user. Small hint records in the same namespace say whose libraries hold
//     items a user may see (see [Service.LibrarySources]). Thus no listing
//     reads every library. The cleanups never list or delete in that namespace
//     either.
//
// The web layer publishes a draft itself. It stores the document here and
// writes the configs, with its own locks, stages and broadcasts.
//
// The metadata of a document names who made it and who last saved it, and
// when (see [phenix/types/builder.Provenance]). This package sets those four
// fields only when it stores a draft snapshot:
//   - [Service.CreateDraft] keeps the creator and the creation time a document
//     already names. Otherwise it writes the actor and now.
//   - [Service.AppendSnapshot] writes the creator and the creation time the
//     draft records.
//   - Both write the actor and now as the last editor and the last edit time,
//     whatever the request says of them.
//
// Thus the last editor of every snapshot a save stored is the actor of that
// save, at the time its manifest records. A draft opened from a document the
// caller read itself, and sent back unchanged, is stored as it is. Nothing
// else writes a document. Moving the cursor, deleting a snapshot, sharing,
// recording a publication and storing a published document do not change the
// bytes they get.
//
// A reference may also name a Builder file by its path on the phenix server.
// [ReadDocumentFile] reads one for a caller that did not choose the path. It
// stores nothing it reads, and its errors say nothing of what a file holds.
// It and the reads of template files are the only times this package touches
// the file system.
//
// The package uses optimistic concurrency control. Every draft mutation takes
// the record revision the caller observed and does a compare-and-swap against
// the store. Content chunks are always written before the metadata
// compare-and-swap, so a failed swap never leaves metadata that points at
// missing content. The package removes the chunks a failed attempt wrote and
// reports any cleanup failure to the caller. It never ignores it. When the
// store cannot say whether the metadata write failed, the package reads the
// draft back instead and leaves unreferenced chunks to
// [Service.CleanupOrphanedChunks].
//
// This package deliberately does *not* implement authorization. Owner and
// actor are explicit, trusted arguments from the caller: the web layer, which
// authenticates and authorizes them. The service records the actor of every
// mutation (including cross-user actors) for audit purposes.
package builder
