package builder

import "phenix/types/builder"

// Storage limits that this package enforces. They are intentionally hard
// limits. They protect the store (and the memory of the process that
// reassembles a document) from unbounded growth. The package checks them
// before any durable metadata update.
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

	// MaxCompressedBytes bounds the compressed size of a stored payload. gzip
	// adds a small amount of overhead for incompressible input, so the bound is
	// the document limit plus slack.
	MaxCompressedBytes = MaxDocumentBytes + compressionSlackBytes

	// compressionSlackBytes is the gzip overhead allowance included in
	// [MaxCompressedBytes] (1 MiB).
	compressionSlackBytes = 1 << 20

	// MaxDrafts is the most drafts the server holds. [Service.CreateDraft]
	// refuses one more with a [DraftLimitError].
	MaxDrafts = 2000
)

// Bounds on the untrusted strings a caller may attach to a draft or published
// document. They are exported because handlers must reject oversized input
// before it reaches this package. They also keep a metadata record well below
// [MaxMetadataBytes].
const (
	// MaxIDLength bounds draft, snapshot, and document identifiers.
	MaxIDLength = 128

	// MaxOwnerLength bounds the creator and actor of a draft. It is the bound
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

	// MaxMetadataBytes bounds the encoded size of one draft or published
	// document metadata record (512 KiB). It bounds the memory a record read
	// takes and the size of a response, even at [MaxSnapshots] snapshots.
	MaxMetadataBytes = 512 << 10
)

// Limits of the template library (see [TemplateLibrary]). The whole
// library is one record, so [MaxMetadataBytes] bounds it too: its templates
// and its collections together.
const (
	// MaxLibraryTemplates is the most templates one library holds.
	MaxLibraryTemplates = 200

	// MaxLibraryCollections is the most collections one library holds.
	MaxLibraryCollections = 50

	// MaxCollectionTemplates is the most templates one collection names. A
	// template file is one collection, so it holds as many.
	MaxCollectionTemplates = builder.MaxTemplateFileTemplates
)

// MaxLayerSets is the most layer sets the layer set library holds, apart
// from the built-in sets (see [LibraryLayerSet]). Each set is one record,
// of at most [MaxMetadataBytes].
const MaxLayerSets = 200

// Record namespaces used by this package. They are separate namespaces so
// prefix scans and prefix deletions of one kind of data can never touch
// another.
const (
	// NamespaceDrafts holds one metadata record per draft, keyed by draft ID.
	NamespaceDrafts = "builder.drafts"

	// NamespaceContent holds the compressed document of every draft snapshot
	// and published document, one record each. A snapshot is keyed by
	// "drafts/<draft-id>/<snapshot-id>" and a published document by
	// "published/<document-id>".
	NamespaceContent = "builder.content"

	// NamespacePublished holds one metadata record per published document,
	// keyed by published document ID.
	NamespacePublished = "builder.published"

	// NamespaceIcons holds the server-wide icon library: one record per icon
	// name and per alias, keyed by "name/" and the name in lower case (see
	// [LibraryIcon]). The record of an icon holds the icon. The record of an
	// alias points at the record of the name its icon was renamed to.
	NamespaceIcons = "builder.icons"

	// NamespaceTemplates holds the template library of the server: one
	// record, keyed by [LibraryKey] (see [TemplateLibrary]). The whole
	// library is that one record, of at most [MaxMetadataBytes].
	NamespaceTemplates = "builder.templates"

	// NamespaceLayerSets holds the server-wide layer set library: one record
	// per set, keyed by "set/<id>" (see [LibraryLayerSet]), and the record
	// "default" when a default set was chosen. The built-in sets have no
	// record.
	NamespaceLayerSets = "builder.layersets"
)

// DocumentAnnotation is the topology annotation that holds a
// [DocumentReference]. JSON and YAML show it as a map, because phenix/store
// names it a structured annotation.
const DocumentAnnotation = "builder-doc"
