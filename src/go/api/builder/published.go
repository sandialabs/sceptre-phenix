package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"phenix/store"
	"phenix/types/builder"
	"phenix/util"
)

// publishedIDSeparator cannot appear in a config name or a digest, so
// ("ab", "c") and ("a", "bc") never derive the same published document ID.
const publishedIDSeparator = "\x1f"

// OrphanGracePeriod is how old unreferenced content must be before
// [Service.CleanupOrphanedChunks], [Service.CleanupOrphanedDocuments] and
// [Service.DeleteSupersededDocuments] remove it. Every write stores its chunks
// before the metadata that references them, and a published document is stored
// before the config that references it, so younger content may belong to a
// write still in flight, in this process or in another one sharing the store.
const OrphanGracePeriod = time.Hour

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

// ParseDocument decodes and validates untrusted document bytes as saving a
// draft snapshot does, within the same [MaxDocumentBytes], for a caller that
// uses the document without storing it.
func ParseDocument(data []byte) (*builder.Document, error) {
	_, doc, err := canonicalDocument(data)

	return doc, err
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

// EncodeReference returns the compact JSON encoding of a document reference:
// the value of a config's [DocumentAnnotation] annotation. A sub-key that is
// empty is left out.
func (r DocumentReference) EncodeReference() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding builder document reference: %w", err)
	}

	return string(data), nil
}

// The sub-keys of a document reference.
const (
	referenceDigest = "digest"
	referenceID     = "id"
	referencePath   = "path"
)

// DecodeReference decodes the value of a config's [DocumentAnnotation]
// annotation, as [DocumentReference.EncodeReference] writes it. Decoding is
// strict: the value must be a single JSON object with no trailing content,
// holding at least one of the sub-keys digest, id and path and no other, and
// each sub-key it holds must be text of the right shape (a sha256 digest, a
// document identifier, see [ValidateDocumentPath]). Nothing is read through
// a reference that does not decode.
func DecodeReference(value string) (DocumentReference, error) {
	var fields map[string]*string

	if err := util.DecodeJSONStrict(strings.NewReader(value), &fields); err != nil {
		if errors.Is(err, util.ErrTrailingJSON) {
			return DocumentReference{}, newValidationError(DocumentAnnotation, "carries trailing content")
		}

		return DocumentReference{}, newValidationCause(DocumentAnnotation, "is not a valid builder document reference", err)
	}

	if fields == nil {
		return DocumentReference{}, newValidationError(DocumentAnnotation, "must be a map of digest, id and path")
	}

	var ref DocumentReference

	// In key order, so that the same value always fails the same way.
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		var field *string

		switch key {
		case referenceDigest:
			field = &ref.Digest
		case referenceID:
			field = &ref.ID
		case referencePath:
			field = &ref.Path
		default:
			return DocumentReference{}, newValidationError(
				DocumentAnnotation, fmt.Sprintf("has the unknown sub-key %q: its sub-keys are digest, id and path", key),
			)
		}

		text := fields[key]
		if text == nil || *text == "" {
			return DocumentReference{}, newValidationError(DocumentAnnotation, key+" must be text that is not empty")
		}

		*field = *text
	}

	if err := validateReference(ref); err != nil {
		return DocumentReference{}, err
	}

	return ref, nil
}

// StoredID is the ID of the stored published document the reference names
// for the topology it was read from: ID when set, else the ID its digest
// derives for that topology (see [PublishedDocumentID]), else "". A reference
// with only a path names no stored document.
func (r DocumentReference) StoredID(topology string) string {
	switch {
	case r.ID != "":
		return r.ID
	case r.Digest != "":
		return PublishedDocumentID(topology, r.Digest)
	}

	return ""
}

// Names reports whether the reference, read from the topology doc was
// published to, names the stored document doc: doc is the document
// [DocumentReference.StoredID] finds for that topology, and it has the
// reference's digest when the reference has one. A reference that names
// another topology's document by its ID therefore names nothing, since that
// document is never doc.
func (r DocumentReference) Names(doc *PublishedDocument) bool {
	if doc == nil {
		return false
	}

	return r.StoredID(doc.Target) == doc.ID && (r.Digest == "" || r.Digest == doc.Digest)
}

// Publishes reports whether the reference, read from the topology named
// topology, names the stored document a publication of the content with
// this digest stores for that topology, whichever of the digest and the ID
// it holds. No record is read.
func (r DocumentReference) Publishes(topology, digest string) bool {
	return r.StoredID(topology) == PublishedDocumentID(topology, digest) && (r.Digest == "" || r.Digest == digest)
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
// returns it. The caller stores its reference (see
// [PublishedDocument.Reference]) in the config's [DocumentAnnotation]
// annotation. Published documents are content
// addressed and never mutated: publishing identical content to the same target
// twice returns the existing document, once its stored content has been
// verified. An existing document whose content is corrupt or missing is
// replaced by a fresh copy, so publishing again repairs it.
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

	canonical, parsed, err := canonicalDocument(req.Document)
	if err != nil {
		return nil, err
	}

	load, err := buildPayload(canonical, s.chunkSize)
	if err != nil {
		return nil, err
	}

	documentID := PublishedDocumentID(req.Target, load.digest)

	existing, err := s.GetPublishedDocument(ctx, documentID)

	switch {
	case err == nil:
		intact, err := s.publishedContentIntact(existing)
		if err != nil {
			return nil, err
		}

		if intact {
			touched, err := s.touchPublished(ctx, existing)
			if !errors.Is(err, ErrNotFound) {
				return touched, err
			}

			// Removed since it was read: it is stored afresh.
			existing = nil
		}
	case errors.Is(err, ErrNotFound):
		existing = nil
	default:
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
		Schema:         parsed.Schema,
		Revision:       store.AnyRevision,
		published:      time.Time{},
	}

	// Metadata is encoded before any chunk is written, so an encoding failure
	// can never leave content behind.
	value, err := encodePublished(doc)
	if err != nil {
		return nil, err
	}

	if _, err := s.writeChunks(publishedPayloadScope(documentID, payloadID), load); err != nil {
		return nil, err
	}

	record, err := s.storePublished(doc, value, existing)
	if err != nil {
		return s.putFailed(ctx, doc, existing, err)
	}

	doc.Revision = record.Revision
	doc.published = record.Updated

	return doc, s.dropReplaced(existing)
}

// touchPublished stores an intact document's metadata again, unchanged, when
// the same content is published again, so its store record says it was just
// published: [Service.CleanupOrphanedDocuments] leaves a document published
// within the [OrphanGracePeriod] alone, and an old one no config references
// any more may be published again before the config that will reference it
// is written. A cleanup that listed the document before it is stored again
// fails its revision-checked delete instead. A document stored again since
// it was read is returned as it is now; one removed since is not found.
func (s *Service) touchPublished(ctx context.Context, doc *PublishedDocument) (*PublishedDocument, error) {
	value, err := encodePublished(doc)
	if err != nil {
		return nil, err
	}

	record, err := s.store.UpdateRecord(NamespacePublished, doc.ID, value, doc.Revision)

	switch {
	case err == nil:
		touched := *doc
		touched.Revision = record.Revision
		touched.published = record.Updated

		return &touched, nil
	case errors.Is(err, store.ErrRecordConflict):
		return s.GetPublishedDocument(ctx, doc.ID)
	}

	return nil, storeError(kindPublished, doc.ID, doc.Revision, err)
}

// publishedContentIntact reports whether a published document's content can
// still be read back and matches its metadata. It fails only when the content
// could not be read at all.
func (s *Service) publishedContentIntact(doc *PublishedDocument) (bool, error) {
	_, err := s.readPayload(kindPublished, doc.ID, publishedPayloadScope(doc.ID, doc.PayloadID), doc.manifest())

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrCorrupt):
		return false, nil
	}

	return false, err
}

// storePublished creates the metadata of a new published document, or replaces
// the metadata of the corrupt document replaced at the revision it was read at.
func (s *Service) storePublished(doc *PublishedDocument, value []byte, replaced *PublishedDocument) (store.Record, error) {
	if replaced == nil {
		return s.store.CreateRecord(NamespacePublished, doc.ID, value)
	}

	return s.store.UpdateRecord(NamespacePublished, doc.ID, value, replaced.Revision)
}

// dropReplaced removes the content of the corrupt document a put replaced. A
// failure is reported as an error matching [ErrCleanup]; the content belongs to
// no document any more, so [Service.CleanupOrphanedChunks] removes it later.
func (s *Service) dropReplaced(replaced *PublishedDocument) error {
	if replaced == nil {
		return nil
	}

	err := s.deleteChunkScope(publishedPayloadScope(replaced.ID, replaced.PayloadID))

	return newCleanupError("replacing a corrupt published document", cleanupErrors(err))
}

// putFailed resolves a failed published document write by reading back what is
// stored, because a store error does not always mean nothing was written: an
// etcd request can time out after its proposal was applied.
//
//   - When the stored document names this attempt's payload, the write was
//     applied and that document is returned with its content intact.
//   - When a concurrent attempt stored the same document, the winner is
//     returned. This attempt removes its own chunk scope, which no other writer
//     ever reads or writes, so the winner's content is never touched.
//   - When a different document is stored under the ID, a conflict is returned.
//   - Otherwise the write error is returned. The attempt's chunks are removed
//     only when the store proved the write was not applied (see
//     writeRejected): a write still pending may yet be applied, so they are
//     otherwise left to [Service.CleanupOrphanedChunks].
func (s *Service) putFailed(
	ctx context.Context, doc, replaced *PublishedDocument, cause error,
) (*PublishedDocument, error) {
	expected := store.AnyRevision
	if replaced != nil {
		expected = replaced.Revision
	}

	cause = storeError(kindPublished, doc.ID, expected, cause)
	scope := publishedPayloadScope(doc.ID, doc.PayloadID)

	stored, getErr := s.GetPublishedDocument(ctx, doc.ID)

	switch {
	case getErr == nil && stored.PayloadID == doc.PayloadID:
		return stored, s.dropReplaced(replaced)
	case getErr == nil && replaced != nil && stored.PayloadID == replaced.PayloadID:
		// The corrupt document is still stored, so the replacement is not.
	case getErr == nil && stored.Target == doc.Target && stored.Digest == doc.Digest:
		return stored, newCleanupError("putting published document", cleanupErrors(s.deleteChunkScope(scope)))
	case getErr == nil:
		return nil, errors.Join(&ConflictError{
			Kind:     kindPublished,
			ID:       doc.ID,
			Expected: expected,
			Actual:   stored.Revision,
			Reason:   "a different document is already stored under this identifier",
		}, newCleanupError("putting published document", cleanupErrors(s.deleteChunkScope(scope))))
	case !errors.Is(getErr, ErrNotFound):
		return nil, errors.Join(cause, getErr)
	}

	if !writeRejected(cause) {
		return nil, cause
	}

	return nil, errors.Join(cause, newCleanupError("putting published document", cleanupErrors(s.deleteChunkScope(scope))))
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

// deletePublished removes a published document at the revision it was read at,
// then the payload it names.
func (s *Service) deletePublished(doc *PublishedDocument) error {
	if err := s.store.DeleteRecord(NamespacePublished, doc.ID, doc.Revision); err != nil {
		return storeError(kindPublished, doc.ID, doc.Revision, err)
	}

	if err := s.deleteChunkScope(publishedPayloadScope(doc.ID, doc.PayloadID)); err != nil {
		return newCleanupError("deleting published document", []error{err})
	}

	return nil
}

// deleteListed removes a document a listing returned and reports whether its
// metadata was removed. A document removed or stored again since the listing
// is skipped without an error: it is no longer the document the listing saw.
func (s *Service) deleteListed(doc *PublishedDocument) (bool, error) {
	err := s.deletePublished(doc)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrCleanup):
		return true, err
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound):
		return false, nil
	}

	return false, err
}

// DeleteSupersededDocuments removes every published document of a target except
// the one named by keepID, which is normally the document the config currently
// references. A document stored within the [OrphanGracePeriod] is kept too: it
// may belong to a publication still in flight, in this process or in another
// one sharing the store, whose config is not written yet. It returns the
// number of documents removed; a cleanup failure is reported as an error
// matching [ErrCleanup] alongside the count of documents whose metadata was
// removed.
func (s *Service) DeleteSupersededDocuments(ctx context.Context, target, keepID string) (int, error) {
	if err := validateText("target", target, MaxTargetLength, true); err != nil {
		return 0, err
	}

	if err := validateOptionalID("keepID", keepID); err != nil {
		return 0, err
	}

	now := s.clock()

	return s.deleteDocumentsWhere(ctx, "deleting superseded documents", func(doc *PublishedDocument) bool {
		return doc.Target == target && doc.ID != keepID && now.Sub(doc.lastPublished()) >= OrphanGracePeriod
	})
}

// DeleteTargetDocuments removes every published document of a target, however
// recently it was stored, as deleting the config they were published to does.
// Unlike [Service.DeleteSupersededDocuments] it keeps nothing for a
// publication in flight, so the caller must make sure there is none. It
// returns the number of documents removed; a cleanup failure is reported as
// an error matching [ErrCleanup] alongside the count of documents whose
// metadata was removed.
func (s *Service) DeleteTargetDocuments(ctx context.Context, target string) (int, error) {
	if err := validateText("target", target, MaxTargetLength, true); err != nil {
		return 0, err
	}

	return s.deleteDocumentsWhere(ctx, "deleting target documents", func(doc *PublishedDocument) bool {
		return doc.Target == target
	})
}

// DeletedConfig names a config that was just deleted, or renamed, to
// [Service.DeleteConfigDocuments].
type DeletedConfig struct {
	Kind string
	Name string
	// DocumentID is the stored published document the config named in its
	// [DocumentAnnotation] annotation (see [DocumentReference.StoredID]), or
	// empty when it named none.
	DocumentID string
	// Updated is the config's metadata.updated time, which the store keeps to
	// the second: the start of the second the config was last written in. It
	// is zero when the time is not known.
	Updated time.Time
}

// DeleteConfigDocuments removes the published documents of a deleted config
// that were stored before the config was last written, and every one older
// than the [OrphanGracePeriod]. A publication stores its document before the
// config that references it, so a younger document stored since that write may
// belong to a publication still in flight, in this process or in another one
// sharing the store, which is about to write the config again. It is kept, and
// a later publication or [Service.CleanupOrphanedDocuments] removes it once it
// is past the grace period.
//
// A config's time of writing is only known to the second, so the documents
// stored before the write are: those stored before that second; the document
// the config named, unless it was stored again in a later second (the same
// content is being published again); and those stored no later than that
// document, which a publication stored before the config that named it. A
// document stored in that second, after the document the config named, is
// kept: it may have been stored after the delete. The same content published
// again within that second cannot be told from the config's own publication,
// so that document is removed; the caller keeps it by holding off
// publications, as the REST API does in its own process. Without an Updated
// time every document within the grace period is kept.
//
// It returns the number of documents removed; a cleanup failure is reported
// as an error matching [ErrCleanup] alongside the count of documents whose
// metadata was removed.
func (s *Service) DeleteConfigDocuments(ctx context.Context, deleted DeletedConfig) (int, error) {
	if err := validateText("name", deleted.Name, MaxTargetLength, true); err != nil {
		return 0, err
	}

	if err := validateOptionalID("documentID", deleted.DocumentID); err != nil {
		return 0, err
	}

	config := store.ConfigFullName(deleted.Kind, deleted.Name)
	if config == "" {
		return 0, newValidationError("kind", "must be a config kind")
	}

	docs, err := s.ListPublishedDocuments(ctx)
	if err != nil {
		return 0, err
	}

	var (
		now     = s.clock()
		written = deleted.Updated.Truncate(time.Second)
		// named is when the document the config named was stored, when that
		// was no later than the second the config was last written in.
		named time.Time
	)

	for i := range docs {
		doc := &docs[i]

		if deleted.Updated.IsZero() || doc.ID != deleted.DocumentID ||
			store.ConfigFullName(doc.Kind, doc.Target) != config {
			continue
		}

		if published := doc.lastPublished(); published.Before(written.Add(time.Second)) {
			named = published
		}
	}

	return s.deleteDocuments("deleting documents of a deleted config", docs, func(doc *PublishedDocument) bool {
		if store.ConfigFullName(doc.Kind, doc.Target) != config {
			return false
		}

		published := doc.lastPublished()

		return published.Before(written) ||
			(!named.IsZero() && !published.After(named)) ||
			now.Sub(published) >= OrphanGracePeriod
	})
}

// LiveDocument names the stored published document a topology's reference
// names (see [DocumentReference.StoredID]) to
// [Service.CleanupOrphanedDocuments].
type LiveDocument struct {
	// Target is the name of the topology the reference was read from.
	Target string
	// ID is the document the reference names for that topology.
	ID string
}

// CleanupOrphanedDocuments removes every published document that is not in
// the given set of live documents, which the caller collects from the
// topologies it owns. A document is live only when the topology it was
// published to names it: a reference on another topology keeps nothing.
// Callers must pass a complete set: any document missing from it is treated
// as an orphan and removed, unless it was stored within the
// [OrphanGracePeriod] and so may be about to be referenced.
func (s *Service) CleanupOrphanedDocuments(ctx context.Context, referenced []LiveDocument) (int, error) {
	live := make(map[LiveDocument]bool, len(referenced))
	for _, document := range referenced {
		live[document] = true
	}

	now := s.clock()

	return s.deleteDocumentsWhere(ctx, "cleaning up orphaned documents", func(doc *PublishedDocument) bool {
		return !live[LiveDocument{Target: doc.Target, ID: doc.ID}] && now.Sub(doc.lastPublished()) >= OrphanGracePeriod
	})
}

// deleteDocumentsWhere removes every published document that remove selects.
// It returns the number of documents removed; a failure is reported as an
// error matching [ErrCleanup] for the operation, alongside the count of
// documents whose metadata was removed.
func (s *Service) deleteDocumentsWhere(
	ctx context.Context,
	operation string,
	remove func(*PublishedDocument) bool,
) (int, error) {
	docs, err := s.ListPublishedDocuments(ctx)
	if err != nil {
		return 0, err
	}

	return s.deleteDocuments(operation, docs, remove)
}

// deleteDocuments removes the documents among docs, as a listing returned
// them, that remove selects. It returns what
// [Service.deleteDocumentsWhere] does.
func (s *Service) deleteDocuments(
	operation string,
	docs []PublishedDocument,
	remove func(*PublishedDocument) bool,
) (int, error) {
	var (
		removed int
		errs    []error
	)

	for i := range docs {
		if !remove(&docs[i]) {
			continue
		}

		deleted, err := s.deleteListed(&docs[i])
		if deleted {
			removed++
		}

		if err != nil {
			errs = append(errs, err)
		}
	}

	return removed, newCleanupError(operation, errs)
}

// CleanupOrphanedChunks removes content chunks that belong to no existing draft
// or published document. It is a repair helper for content left behind by a
// crash between writing chunks and writing metadata. A payload whose newest
// chunk was written within the [OrphanGracePeriod] is kept, because the
// metadata that references it may not have been written yet. So is all of the
// content of a draft whose metadata cannot be read, which may reference any of
// it; the rest is cleaned up regardless.
func (s *Service) CleanupOrphanedChunks(ctx context.Context) (int, error) {
	drafts, unreadable, err := s.listDrafts(ctx)
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

	keys, err := s.store.ListRecordKeys(NamespaceChunks, "")
	if err != nil {
		return 0, fmt.Errorf("listing chunk keys: %w", err)
	}

	scopes, unreferenced, errs := unreferencedChunks(keys, live)

	var (
		orphans []string
		now     = s.clock()
	)

	for _, scope := range scopes {
		if slices.ContainsFunc(unreadable, func(draftID string) bool {
			return strings.HasPrefix(scope, draftScope(draftID))
		}) {
			continue
		}

		scopeKeys := unreferenced[scope]

		settled, err := s.chunkSettled(scopeKeys[len(scopeKeys)-1], now)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if settled {
			orphans = append(orphans, scopeKeys...)
		}
	}

	errs = append(errs, s.deleteChunkKeys(orphans)...)

	return len(orphans), newCleanupError("cleaning up orphaned chunks", errs)
}

// unreferencedChunks groups the chunk keys of every payload scope that is not
// live by scope, returning the scopes in key order. A key that is not shaped
// like a chunk key is reported as corruption.
func unreferencedChunks(keys []string, live map[string]bool) ([]string, map[string][]string, []error) {
	var (
		scopes  []string
		errs    []error
		byScope = make(map[string][]string)
	)

	for _, key := range keys {
		scope := chunkPayloadScopeOf(key)
		if scope == "" {
			errs = append(errs, newCorruptError("chunk", key, "key is not shaped like a chunk key"))

			continue
		}

		if live[scope] {
			continue
		}

		if _, ok := byScope[scope]; !ok {
			scopes = append(scopes, scope)
		}

		byScope[scope] = append(byScope[scope], key)
	}

	return scopes, byScope, errs
}

// chunkSettled reports whether an unreferenced chunk was written longer than
// the [OrphanGracePeriod] before now. Chunks are written in key order, so the
// last key of a payload scope tells whether its write may still be in flight. A
// chunk that is already gone is not settled: there is nothing left to remove.
func (s *Service) chunkSettled(key string, now time.Time) (bool, error) {
	record, err := s.store.GetRecord(NamespaceChunks, key)

	switch {
	case errors.Is(err, store.ErrRecordNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading chunk %s: %w", key, err)
	}

	return now.Sub(record.Created) >= OrphanGracePeriod, nil
}

// lastPublished is when the document was stored or published again, as its
// metadata and its store record say.
func (p *PublishedDocument) lastPublished() time.Time {
	if p.published.After(p.CreatedAt) {
		return p.published
	}

	return p.CreatedAt
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
		OpID:           "",
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
	doc.published = record.Updated

	return &doc, nil
}
