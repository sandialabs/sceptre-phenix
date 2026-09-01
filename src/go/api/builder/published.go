package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"phenix/store"
	"phenix/types/builder"
)

// publishedIDSeparator cannot appear in a config name or a digest, so
// ("ab", "c") and ("a", "bc") never derive the same published document ID.
const publishedIDSeparator = "\x1f"

// EncodeDocument returns the canonical JSON encoding of a validated builder
// document, checked against [MaxDocumentBytes]. The document is validated
// semantically first, so an invalid document can never be encoded and handed to
// this package. It is the encoding every draft snapshot and published document
// is stored with.
func EncodeDocument(doc *builder.Document) ([]byte, error) {
	if doc == nil {
		return nil, newValidationError("document", "must not be nil")
	}

	if err := doc.Validate(); err != nil {
		return nil, newValidationCause("document", "is not a valid builder document", err)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding builder document: %w", err)
	}

	if int64(len(data)) > MaxDocumentBytes {
		return nil, newTooLargeError("document", int64(len(data)), MaxDocumentBytes)
	}

	return data, nil
}

// PublishedDocumentID returns the deterministic, content addressed ID of a
// document published to a target. The same content published to the same target
// always yields the same ID, which makes publishing idempotent; the same
// content published to different targets is stored independently, so removing
// one target's documents never affects another's.
func PublishedDocumentID(target, digest string) string {
	sum := sha256.Sum256([]byte(target + publishedIDSeparator + digest))

	return hex.EncodeToString(sum[:])
}

// EncodeReference returns the compact JSON encoding of a document reference,
// suitable for a config annotation value.
func (r DocumentReference) EncodeReference() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding builder document reference: %w", err)
	}

	return string(data), nil
}

// DecodeReference decodes a compact document reference produced by
// [DocumentReference.EncodeReference]. Decoding is strict: the value must be a
// single JSON object with no unknown fields and no trailing content, and every
// field is validated (identifier shape, sha256 digest syntax, builder schema
// URI, and size, chunk count, and chunk size bounds) before the reference is
// used to read anything.
func DecodeReference(value string) (DocumentReference, error) {
	var ref DocumentReference

	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&ref); err != nil {
		return DocumentReference{}, newValidationCause(DocumentAnnotation, "is not a valid builder document reference", err)
	}

	var trailing json.RawMessage

	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return DocumentReference{}, newValidationError(DocumentAnnotation, "carries trailing content")
	}

	if err := validateReference(ref); err != nil {
		return DocumentReference{}, err
	}

	return ref, nil
}

// PutPublishedDocumentRequest stores an immutable copy of a published document.
type PutPublishedDocumentRequest struct {
	// Target and Kind identify the config the document was published to.
	Target string
	Kind   string
	Actor  string
	// Document holds the canonical JSON encoding of the document, as produced by
	// [EncodeDocument].
	Document []byte
	// DraftID and SnapshotID optionally record where the document came from.
	DraftID    string
	SnapshotID string
}

// PutPublishedDocument stores an immutable copy of a published document and
// returns it together with the compact reference the caller stores in the
// config's [DocumentAnnotation] annotation. Published documents are content
// addressed and never mutated: publishing identical content to the same target
// twice returns the existing document.
//
// Every attempt writes its content to a private chunk scope named by a
// generated payload ID, so concurrent attempts at the same document never
// share chunks: the attempt that loses the race removes only the scope it
// wrote and the winner's content is always intact. An attempt that stored
// chunks but could not remove them after losing returns the winning document
// together with an error matching [ErrCleanup]; the returned document is still
// usable.
func (s *Service) PutPublishedDocument(ctx context.Context, req PutPublishedDocumentRequest) (*PublishedDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("putting published document: %w", err)
	}

	if err := validatePutRequest(req); err != nil {
		return nil, err
	}

	canonical, _, err := canonicalDocument("document", req.Document)
	if err != nil {
		return nil, err
	}

	load, err := buildPayload(canonical, s.chunkSize)
	if err != nil {
		return nil, err
	}

	documentID := PublishedDocumentID(req.Target, load.digest)

	if existing, err := s.GetPublishedDocument(ctx, documentID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	payloadID, err := s.payloadID()
	if err != nil {
		return nil, err
	}

	doc := &PublishedDocument{
		ID:             documentID,
		Digest:         load.digest,
		Size:           load.size,
		CompressedSize: load.compressedSize,
		ChunkDigests:   load.chunkDigests,
		ChunkSize:      s.chunkSize,
		PayloadID:      payloadID,
		Target:         req.Target,
		Kind:           req.Kind,
		DraftID:        req.DraftID,
		SnapshotID:     req.SnapshotID,
		CreatedAt:      s.clock().UTC(),
		CreatedBy:      req.Actor,
		Revision:       store.AnyRevision,
	}

	// Metadata is encoded before any chunk is written, so an encoding failure
	// can never leave content behind.
	value, err := encodePublished(doc)
	if err != nil {
		return nil, err
	}

	scope := publishedPayloadScope(documentID, payloadID)

	if _, err := s.writeChunks(scope, load); err != nil {
		return nil, err
	}

	record, err := s.store.CreateRecord(NamespacePublished, documentID, value)
	if err != nil {
		return s.putFailed(ctx, documentID, load.digest, req.Target, scope, err)
	}

	doc.Revision = record.Revision

	return doc, nil
}

// putFailed resolves a failed published document create. The failed attempt
// always owns its whole chunk scope, so removing it is unconditionally safe:
// no other writer, past or concurrent, ever writes or reads those keys. When
// the create failed because a concurrent writer already stored the same
// document, the winner is returned; the winner's own, separate chunks are never
// touched.
func (s *Service) putFailed(
	ctx context.Context, documentID, digest, target, scope string, cause error,
) (*PublishedDocument, error) {
	cleanupErr := s.deleteChunkScope(scope)

	existing, getErr := s.GetPublishedDocument(ctx, documentID)

	switch {
	case getErr == nil && existing.Target == target && existing.Digest == digest:
		if cleanupErr != nil {
			return existing, newCleanupError("putting published document", cleanupErrors(cleanupErr))
		}

		return existing, nil
	case getErr == nil:
		return nil, errors.Join(&ConflictError{
			Kind:     kindPublished,
			ID:       documentID,
			Expected: store.AnyRevision,
			Actual:   existing.Revision,
			Reason:   "a different document is already stored under this identifier",
		}, newCleanupError("putting published document", cleanupErrors(cleanupErr)))
	case !errors.Is(getErr, ErrNotFound):
		return nil, errors.Join(
			storeError(kindPublished, documentID, store.AnyRevision, cause), getErr,
			newCleanupError("putting published document", cleanupErrors(cleanupErr)),
		)
	}

	return nil, errors.Join(
		storeError(kindPublished, documentID, store.AnyRevision, cause),
		newCleanupError("putting published document", cleanupErrors(cleanupErr)),
	)
}

// payloadID returns a validated identifier for one attempt's private chunk
// scope.
func (s *Service) payloadID() (string, error) {
	payloadID, err := s.newID()
	if err != nil {
		return "", err
	}

	if err := validateID("payloadID", payloadID); err != nil {
		return "", err
	}

	return payloadID, nil
}

func validatePutRequest(req PutPublishedDocumentRequest) error {
	if err := validateText("target", req.Target, MaxTargetLength, true); err != nil {
		return err
	}

	if err := validateText("kind", req.Kind, MaxKindLength, true); err != nil {
		return err
	}

	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return err
	}

	if err := validateOptionalID("draftID", req.DraftID); err != nil {
		return err
	}

	return validateOptionalID("snapshotID", req.SnapshotID)
}

// encodePublished encodes published document metadata and enforces
// [MaxMetadataBytes].
func encodePublished(doc *PublishedDocument) ([]byte, error) {
	value, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding published document %s: %w", doc.ID, err)
	}

	if len(value) > MaxMetadataBytes {
		return nil, newTooLargeError("published document metadata", int64(len(value)), MaxMetadataBytes)
	}

	return value, nil
}

// GetPublishedDocument returns the metadata of a published document.
func (s *Service) GetPublishedDocument(ctx context.Context, documentID string) (*PublishedDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting published document %s: %w", documentID, err)
	}

	if err := validateID("documentID", documentID); err != nil {
		return nil, err
	}

	record, err := s.store.GetRecord(NamespacePublished, documentID)
	if err != nil {
		return nil, storeError(kindPublished, documentID, store.AnyRevision, err)
	}

	return decodePublished(record)
}

// GetPublishedDocumentData returns a published document together with its
// verified canonical JSON bytes.
func (s *Service) GetPublishedDocumentData(ctx context.Context, documentID string) (*PublishedDocument, []byte, error) {
	doc, err := s.GetPublishedDocument(ctx, documentID)
	if err != nil {
		return nil, nil, err
	}

	data, err := s.readPayload(kindPublished, doc.ID, publishedPayloadScope(doc.ID, doc.PayloadID), doc.manifest())
	if err != nil {
		return nil, nil, err
	}

	if _, err := parseStored(kindPublished, doc.ID, data); err != nil {
		return nil, nil, err
	}

	return doc, data, nil
}

// ListPublishedDocuments returns every published document, ordered by ID.
func (s *Service) ListPublishedDocuments(ctx context.Context) ([]PublishedDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing published documents: %w", err)
	}

	records, err := s.store.ListRecords(NamespacePublished, "")
	if err != nil {
		return nil, fmt.Errorf("listing published documents: %w", err)
	}

	docs := make([]PublishedDocument, 0, len(records))

	for _, record := range records {
		doc, err := decodePublished(record)
		if err != nil {
			return nil, err
		}

		docs = append(docs, *doc)
	}

	return docs, nil
}

// VerifyPublishedDocument reassembles the document a reference points at and
// checks it against the reference. It returns the verified document bytes, or
// an error matching [ErrNotFound] when the document is gone and [ErrCorrupt]
// when the stored content does not match the reference.
func (s *Service) VerifyPublishedDocument(ctx context.Context, ref DocumentReference) ([]byte, error) {
	if err := validateReference(ref); err != nil {
		return nil, err
	}

	doc, data, err := s.GetPublishedDocumentData(ctx, ref.ID)
	if err != nil {
		return nil, err
	}

	switch {
	case doc.Digest != ref.Digest:
		return nil, newCorruptError(kindPublished, ref.ID, fmt.Sprintf("digest is %s, reference expects %s", doc.Digest, ref.Digest))
	case doc.Size != ref.Size:
		return nil, newCorruptError(kindPublished, ref.ID, fmt.Sprintf("size is %d, reference expects %d", doc.Size, ref.Size))
	case len(doc.ChunkDigests) != ref.Chunks:
		return nil, newCorruptError(
			kindPublished, ref.ID,
			fmt.Sprintf("chunk count is %d, reference expects %d", len(doc.ChunkDigests), ref.Chunks),
		)
	case doc.ChunkSize != ref.ChunkSize:
		return nil, newCorruptError(
			kindPublished, ref.ID,
			fmt.Sprintf("chunk size is %d, reference expects %d", doc.ChunkSize, ref.ChunkSize),
		)
	}

	parsed, err := parseStored(kindPublished, ref.ID, data)
	if err != nil {
		return nil, err
	}

	if parsed.Schema != ref.Schema {
		return nil, newCorruptError(kindPublished, ref.ID, fmt.Sprintf("schema is %q, reference expects %q", parsed.Schema, ref.Schema))
	}

	return data, nil
}

// DeletePublishedDocument removes a published document and its chunks. Removal
// of a document that is still referenced by a config is the caller's decision;
// this package does not read configs.
func (s *Service) DeletePublishedDocument(ctx context.Context, documentID string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting published document %s: %w", documentID, err)
	}

	if err := validateID("documentID", documentID); err != nil {
		return err
	}

	if err := s.store.DeleteRecord(NamespacePublished, documentID, store.AnyRevision); err != nil {
		return storeError(kindPublished, documentID, store.AnyRevision, err)
	}

	if err := s.deleteChunkScope(publishedScope(documentID)); err != nil {
		return newCleanupError("deleting published document", []error{err})
	}

	return nil
}

// DeleteSupersededDocuments removes every published document of a target except
// the one named by keepID, which is normally the document the config currently
// references. It returns the number of documents removed; a cleanup failure is
// reported as an error matching [ErrCleanup] alongside the count of documents
// whose metadata was removed.
func (s *Service) DeleteSupersededDocuments(ctx context.Context, target, keepID string) (int, error) {
	if err := validateText("target", target, MaxTargetLength, true); err != nil {
		return 0, err
	}

	if err := validateOptionalID("keepID", keepID); err != nil {
		return 0, err
	}

	docs, err := s.ListPublishedDocuments(ctx)
	if err != nil {
		return 0, err
	}

	var (
		removed int
		errs    []error
	)

	for i := range docs {
		if docs[i].Target != target || docs[i].ID == keepID {
			continue
		}

		if err := s.DeletePublishedDocument(ctx, docs[i].ID); err != nil {
			if errors.Is(err, ErrCleanup) {
				removed++
			}

			errs = append(errs, err)

			continue
		}

		removed++
	}

	return removed, newCleanupError("deleting superseded documents", errs)
}

// CleanupOrphanedDocuments removes every published document whose ID is not in
// the given set of live references, which the caller collects from the configs
// it owns. Callers must pass a complete set: any document missing from it is
// treated as an orphan and removed.
func (s *Service) CleanupOrphanedDocuments(ctx context.Context, referenced []DocumentReference) (int, error) {
	docs, err := s.ListPublishedDocuments(ctx)
	if err != nil {
		return 0, err
	}

	live := make(map[string]bool, len(referenced))
	for _, ref := range referenced {
		live[ref.ID] = true
	}

	var (
		removed int
		errs    []error
	)

	for i := range docs {
		if live[docs[i].ID] {
			continue
		}

		if err := s.DeletePublishedDocument(ctx, docs[i].ID); err != nil {
			if errors.Is(err, ErrCleanup) {
				removed++
			}

			errs = append(errs, err)

			continue
		}

		removed++
	}

	return removed, newCleanupError("cleaning up orphaned documents", errs)
}

// CleanupOrphanedChunks removes content chunks that belong to no existing draft
// or published document. It is a repair helper for content left behind by a
// crash between writing chunks and writing metadata.
func (s *Service) CleanupOrphanedChunks(ctx context.Context) (int, error) {
	drafts, err := s.ListDrafts(ctx)
	if err != nil {
		return 0, err
	}

	docs, err := s.ListPublishedDocuments(ctx)
	if err != nil {
		return 0, err
	}

	// Live scopes are per snapshot and per payload, not per draft or document:
	// a chunk scope of a snapshot that no longer exists is an orphan even when
	// its draft is still alive.
	live := make(map[string]bool, len(drafts)+len(docs))

	for i := range drafts {
		for j := range drafts[i].History {
			live[snapshotScope(drafts[i].ID, drafts[i].History[j].ID)] = true
		}
	}

	for i := range docs {
		live[publishedPayloadScope(docs[i].ID, docs[i].PayloadID)] = true
	}

	records, err := s.store.ListRecords(NamespaceChunks, "")
	if err != nil {
		return 0, fmt.Errorf("listing chunks: %w", err)
	}

	var (
		orphans []string
		errs    []error
	)

	for _, record := range records {
		scope := chunkPayloadScopeOf(record.Key)
		if scope == "" {
			errs = append(errs, newCorruptError("chunk", record.Key, "key is not shaped like a chunk key"))

			continue
		}

		if !live[scope] {
			orphans = append(orphans, record.Key)
		}
	}

	errs = append(errs, s.deleteChunkKeys(orphans)...)

	return len(orphans), newCleanupError("cleaning up orphaned chunks", errs)
}

func (p *PublishedDocument) manifest() SnapshotManifest {
	return SnapshotManifest{
		ID:             p.ID,
		Digest:         p.Digest,
		Size:           p.Size,
		CompressedSize: p.CompressedSize,
		ChunkDigests:   p.ChunkDigests,
		ChunkSize:      p.ChunkSize,
		CreatedAt:      p.CreatedAt,
		CreatedBy:      p.CreatedBy,
		Summary:        "",
	}
}

func decodePublished(record store.Record) (*PublishedDocument, error) {
	var doc PublishedDocument

	if err := decodeMetadata(kindPublished, record.Value, &doc); err != nil {
		return nil, newCorruptError(kindPublished, record.Key, err.Error())
	}

	if err := validatePublishedMetadata(record.Key, &doc); err != nil {
		return nil, err
	}

	doc.Revision = record.Revision

	return &doc, nil
}
