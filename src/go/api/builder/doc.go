// Package builder implements persistence for the phenix topology builder.
//
// Four kinds of data are persisted, all through the generic
// [phenix/store.RecordStore] primitives. No broker events are created by this
// package, and it stores no phenix config:
//
//   - Drafts: mutable, per-user working documents. A draft is a metadata record
//     (owner, title, provenance, publication state, and an ordered history of
//     snapshot manifests) plus immutable content chunks holding the compressed
//     document bytes of every snapshot.
//   - Published documents: immutable, content addressed copies of the document
//     a config was published from. The caller stores a [DocumentReference],
//     the document's digest and ID, in the topology's "builder-doc" annotation.
//     The Topology config hook of this package checks that reference in every
//     topology about to be stored.
//   - The icon library: the custom icons every user of the server shares,
//     each under a unique name its uploader chose, with one record per icon
//     name and per alias in a namespace of its own (see [Service.AddIcon]).
//     Nodes and templates name icons; a document carries copies only when
//     it is to stand on its own, as a downloaded file does. Only an icon's
//     uploader, or a caller the web layer allows to act on any icon, renames
//     or deletes it (see [Service.RenameIcon], [Service.DeleteIcon]).
//     [Service.CleanupOrphanedChunks] and [Service.CleanupOrphanedDocuments]
//     never list or delete in that namespace; [Service.CleanupLegacyIcons]
//     removes records of the per-user layout of earlier builds.
//   - Template libraries: the device templates a user keeps, with their
//     collections, as one record per user in a namespace of its own (see
//     [TemplateLibrary]). A template names its custom icon, which the icon
//     library resolves. A user who never changed
//     the library has no record and is given the built-in templates; the
//     first change stores them with the record, so one that was deleted
//     stays deleted until the owner restores it (see
//     [TemplateLibrary.RestoreBuiltins]). Every change is one
//     compare-and-swap of that record (see [Service.UpdateLibrary]). An
//     owner may share items, read only,
//     with other users, or publish them to every user; small hint records
//     in the same namespace say whose libraries hold items a user may see
//     (see [Service.LibrarySources]), so no listing reads every library.
//     The cleanups never list or delete in that namespace either.
//
// The web layer publishes a draft itself: it stores the document here and
// writes the configs, with its own locks, stages and broadcasts.
//
// A document's metadata names who made it and who last saved it, and when
// (see [phenix/types/builder.Provenance]). This package sets those four
// fields, and only when it stores a draft snapshot: [Service.CreateDraft]
// keeps the creator and the creation time a document already names and
// otherwise writes the actor and now, [Service.AppendSnapshot] writes the
// ones the draft records, and both write the actor and now as the last editor and the last
// edit time, whatever the request says of them. So the last editor of every
// snapshot a save stored is the actor of that save, at the time its manifest
// records. A draft opened from a document the caller read itself, and sent
// back unchanged, is stored as it is. Nothing else writes a document: moving
// the cursor, deleting a snapshot, sharing, recording a publication and
// storing a published document leave the bytes they are given alone.
//
// A reference may also name a Builder file by its path on the phenix server.
// [ReadDocumentFile] reads one for a caller that did not choose the path:
// nothing it reads is stored, and its errors say nothing of what a file
// holds. It and the reads of template files are the only times this package
// touches the file system.
//
// Concurrency is handled with optimistic concurrency control: every draft
// mutation takes the record revision the caller observed and performs a
// compare-and-swap against the store. Content chunks are always written before
// the metadata compare-and-swap so a failed swap never leaves metadata pointing
// at missing content; chunks written by a failed attempt are cleaned up and any
// cleanup failure is reported to the caller rather than being swallowed. When
// the store cannot say whether the metadata write failed, the draft is read
// back instead, and chunks nothing references are left to
// [Service.CleanupOrphanedChunks].
//
// Authorization is deliberately *not* implemented here. Owner and actor are
// explicit, trusted arguments supplied by the caller: the web layer, which is
// responsible for authenticating and authorizing them. The service records the
// actor of every mutation (including cross-user actors) for audit purposes.
package builder
