package builder

// Storage limits enforced by this package. They are intentionally hard limits:
// they protect the store (and the memory of the process reassembling a
// document) from unbounded growth, and are checked before any durable metadata
// update.
const (
	// MaxDocumentBytes is the largest canonical JSON encoding of a single
	// builder document that may be stored (5 MiB).
	MaxDocumentBytes = 5 << 20

	// MaxDraftHistoryBytes is the largest total (uncompressed) size of the
	// snapshots retained in one draft's history (50 MiB). An edit that would
	// push a draft past the limit is rejected with an error matching
	// [ErrTooLarge]; history is never silently discarded to make room.
	MaxDraftHistoryBytes = 50 << 20

	// MaxSnapshots is the largest number of snapshots retained in one draft's
	// history. It is the only limit that prunes: appending past it drops the
	// oldest snapshots.
	MaxSnapshots = 100

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

	// MaxOwnerLength bounds the owner and actor of a draft.
	MaxOwnerLength = 256

	// MaxTitleLength bounds a draft title, which is derived from the document
	// name when the document carries one.
	MaxTitleLength = 512

	// MaxSourceTokenLength bounds the opaque "<kind>/<name>" token recording
	// where a draft was imported from.
	MaxSourceTokenLength = 512

	// MaxSummaryLength bounds the per-snapshot summary shown in history.
	MaxSummaryLength = 1024

	// MaxTargetLength bounds a publication target (a config name).
	MaxTargetLength = 256

	// MaxKindLength bounds a config kind (for example "Topology").
	MaxKindLength = 64

	// MaxMetadataBytes bounds the encoded size of one draft or published
	// document metadata record (512 KiB). It keeps records well below the
	// default etcd request limit (1.5 MiB) even at [MaxSnapshots] snapshots.
	MaxMetadataBytes = 512 << 10
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
)

// DocumentAnnotation is the topology config annotation the caller stores a
// [DocumentReference] under. This package never writes configs; the constant is
// exported so the web layer and this package agree on the key.
const DocumentAnnotation = "builder-doc"
