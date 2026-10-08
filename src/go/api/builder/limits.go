package builder

import "phenix/types/builder"

// Storage limits enforced by this package. They are intentionally hard limits:
// they protect the store (and the memory of the process reassembling a
// document) from unbounded growth, and are checked before any durable metadata
// update.
const (
	// MaxDocumentBytes is the largest canonical JSON encoding of a single
	// builder document that may be stored (5 MiB).
	MaxDocumentBytes = 5 << 20

	// MaxDraftHistoryBytes is the largest total (uncompressed) size of the
	// snapshots retained in one draft's history (50 MiB). It prunes the same
	// way as [MaxSnapshots]: appending past it drops the oldest snapshots until
	// the history fits again.
	MaxDraftHistoryBytes = 50 << 20

	// MaxSnapshots is the largest number of snapshots retained in one draft's
	// history. Appending past it drops the oldest snapshots.
	MaxSnapshots = 50

	// maxStoredSnapshots is the largest history a stored draft may hold. It is
	// above [MaxSnapshots] because drafts written before that limit was lowered
	// hold up to 100 snapshots: such a draft stays readable and is pruned to
	// [MaxSnapshots] by its next append.
	maxStoredSnapshots = 100

	// MaxShares is the largest number of users one draft may be shared with.
	MaxShares = 25

	// maxStoredShares is the largest share list a stored draft may hold. It
	// follows [maxStoredSnapshots]: were [MaxShares] ever lowered, it would
	// stay at the old limit, so drafts shared before stay readable.
	maxStoredShares = 25

	// ChunkBytes is the size of the immutable content chunks a compressed
	// document payload is split into (512 KiB).
	ChunkBytes = 512 << 10

	// MaxCompressedBytes bounds the compressed size of a stored payload. gzip
	// adds a small amount of overhead for incompressible input, so the bound is
	// the document limit plus slack. It also bounds how much data reassembly
	// will read before giving up.
	MaxCompressedBytes = MaxDocumentBytes + compressionSlackBytes

	// compressionSlackBytes is the gzip overhead allowance included in
	// [MaxCompressedBytes] (1 MiB).
	compressionSlackBytes = 1 << 20

	// MaxChunks bounds the number of chunks a single payload may be split into,
	// so a corrupt manifest cannot drive an unbounded number of store reads.
	MaxChunks = (MaxCompressedBytes / ChunkBytes) + 1
)

// Bounds on the untrusted strings a caller may attach to a draft or published
// document. They are exported because handlers need to reject oversized input
// before it reaches this package, and because they are what keeps a metadata
// record comfortably below [MaxMetadataBytes].
const (
	// MaxIDLength bounds draft, snapshot, and document identifiers.
	MaxIDLength = 128

	// MaxOwnerLength bounds the owner and actor of a draft. It is the bound
	// the document validator puts on the users a document names, so the
	// actor of a save is always a valid updatedBy.
	MaxOwnerLength = builder.MaxUserBytes

	// MaxTitleLength bounds a draft title, which is derived from the document
	// name when the document carries one. It is the bound the document
	// validator puts on that name, so every valid document has a usable title.
	MaxTitleLength = builder.MaxNameBytes

	// MaxSourceTokenLength bounds the opaque token recording where a draft
	// came from ("<kind>/<name>", "uploaded/<kind>/<name>",
	// "uploaded/legacy-xml", "builder-doc/<document id>" or
	// "builder-file/<topology>/<digest>").
	MaxSourceTokenLength = 512

	// MaxSourceFileLength bounds the name of the uploaded file a draft was
	// made from (see [DraftMetadata.SourceFile]): the longest file name
	// common file systems hold.
	MaxSourceFileLength = 255

	// MaxSummaryLength bounds the per-snapshot summary shown in history.
	MaxSummaryLength = 1024

	// MaxTargetLength bounds a publication target (a config name).
	MaxTargetLength = 256

	// MaxKindLength bounds a config kind (for example "Topology").
	MaxKindLength = 64

	// MaxDocumentPathLength bounds the path of a Builder file a document
	// reference names (see [ValidateDocumentPath]).
	MaxDocumentPathLength = 1024

	// maxUserCreatedLength bounds the account creation time a share records
	// for its recipient (see [ShareEntry.UserCreated]).
	maxUserCreatedLength = 64

	// MaxMetadataBytes bounds the encoded size of one draft or published
	// document metadata record (512 KiB). It keeps records well below the
	// default etcd request limit (1.5 MiB) even at [maxStoredSnapshots]
	// snapshots.
	MaxMetadataBytes = 512 << 10
)

// Limits of a user's template library (see [TemplateLibrary]). The whole
// library is one record, so [MaxMetadataBytes] bounds it too: its templates,
// its collections and its custom icons together.
const (
	// MaxLibraryTemplates is the most templates one library holds.
	MaxLibraryTemplates = 200

	// MaxLibraryCollections is the most collections one library holds.
	MaxLibraryCollections = 50

	// MaxCollectionTemplates is the most templates one collection names.
	MaxCollectionTemplates = 200

	// MaxLibraryTemplateIcons is the most custom icons the templates of one
	// library use together. It is what a document may carry, so every
	// template of a library fits in one diagram with its icon.
	MaxLibraryTemplateIcons = builder.MaxDocumentIcons
)

// Record namespaces used by this package. They are separate namespaces so
// prefix scans and prefix deletions of one kind of data can never touch
// another.
const (
	// NamespaceDrafts holds one metadata record per draft, keyed by draft ID.
	NamespaceDrafts = "builder.drafts"

	// NamespaceChunks holds immutable content chunks. Draft chunks are keyed by
	// "drafts/<draft-id>/<snapshot-id>/<index>" and published document chunks by
	// "published/<document-id>/<payload-id>/<index>", so every snapshot and
	// every attempt at storing a published document owns a private, immutable
	// chunk scope that no other writer reads or removes.
	NamespaceChunks = "builder.chunks"

	// NamespacePublished holds one metadata record per published document,
	// keyed by published document ID.
	NamespacePublished = "builder.published"

	// NamespaceIcons holds one record per icon of a user's icon library,
	// keyed by "<owner scope>/<the 64 hex digits of the icon ID>" (see
	// [OwnerScope] and [LibraryIcon]). A record is never updated.
	NamespaceIcons = "builder.icons"

	// NamespaceTemplates holds one record per user's template library,
	// keyed by "lib/<owner scope>" (see [LibraryKey] and [TemplateLibrary]).
	// The whole library is that one record, of at most [MaxMetadataBytes].
	// The hints "in/<recipient scope>/<owner scope>" and "pub/<owner scope>",
	// whose value is "{}", say an owner shared something with a recipient
	// or published something (see [Service.LibrarySources]).
	NamespaceTemplates = "builder.templates"
)

// DocumentAnnotation is the topology config annotation that holds a
// [DocumentReference], as the string [DocumentReference.EncodeReference]
// returns. A config's JSON and YAML show it as a map of the reference's
// sub-keys, because phenix/store names it a structured annotation. The
// constant is exported so that the web layer, which writes the annotation
// when it publishes a draft, and this package agree on the key.
const DocumentAnnotation = "builder-doc"
