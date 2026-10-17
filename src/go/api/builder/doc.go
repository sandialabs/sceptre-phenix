// Package builder implements persistence for the phenix topology builder.
//
// The package keeps five kinds of data, all through the generic
// [phenix/store.RecordStore] primitives. It creates no broker events and
// stores no phenix config.
//
//   - Drafts: mutable working documents that every Builder user may read and
//     change. A draft is a metadata record (creator, title, provenance, publication state, and an ordered history of
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
//     Any user renames or deletes an uploaded icon, but no user changes an icon
//     of the template files of the server (see [Service.RenameIcon],
//     [Service.DeleteIcon]). [Service.CleanupOrphanedChunks] and
//     [Service.CleanupOrphanedDocuments] never list or delete in that
//     namespace.
//   - The template library: the device templates every user of the server
//     shares, with their collections, as one record in a namespace of its
//     own (see [TemplateLibrary]). Every user with Builder access changes
//     it. Nothing in it has an owner, and nothing is published. A
//     template names its custom icon, which the icon library resolves. The
//     built-in templates and the templates of the template files of the
//     server are read only, and are never in the record. Every change is one
//     compare-and-swap of that record (see [Service.UpdateLibrary]). The
//     cleanups never list or delete in that namespace either.
//   - The layer set library: the layer sets every user of the server shares,
//     one record per set in a namespace of its own, with the one default set
//     of the server (see [LibraryLayerSet]). Any user may add, change or delete
//     a set. The built-in sets have no record, and nobody changes them. A
//     document holds a full copy of the layers it uses, and names the set and
//     the version they came from. The cleanups never list or delete in that
//     namespace.
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
// else writes a document. Moving the cursor, deleting a snapshot,
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
// reports any cleanup failure to the caller. It never ignores it.
//
// This package deliberately does *not* implement authorization. The actor is
// an explicit, trusted argument from the caller: the web layer, which
// authenticates and authorizes it. The service records the actor of every
// mutation for audit purposes.
package builder
