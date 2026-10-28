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
	"phenix/util/plog"
)

// publishedIDSeparator cannot appear in a config name or a digest, so
// ("ab", "c") and ("a", "bc") never derive the same published document ID.
const publishedIDSeparator = "\x1f"

// OrphanGracePeriod is how old unreferenced content must be before a cleanup
// removes it. A publication stores its document before its config, and a
// cleanup reads metadata before content, so younger content may be in use.
const OrphanGracePeriod = time.Hour

// EncodeDocument returns the canonical JSON encoding of a validated builder
// document, checked against [MaxDocumentBytes]. It validates the document
// semantically first, so an invalid document can never be encoded and given to
// this package. Every draft snapshot and published document is stored with
// this encoding.
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

// ParseDocument decodes and validates untrusted document bytes, within
// [MaxDocumentBytes], as saving a draft snapshot does. It is for a caller that
// uses the document and does not store it.
func ParseDocument(data []byte) (*builder.Document, error) {
	_, doc, err := canonicalDocument(data)

	return doc, err
}

// PublishedDocumentID returns the deterministic, content addressed ID of a
// document published to a target. The same content published to the same
// target always gives the same ID, which makes publishing idempotent. The same
// content published to different targets is stored separately, so removing the
// documents of one target never affects another target.
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

// DecodeReference strictly decodes a [DocumentAnnotation] value: one JSON
// object with at least one of digest, id and path, no other key, and each value
// of the right shape. Nothing is read through a reference that does not decode.
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
// published to, names doc: the document [DocumentReference.StoredID] finds,
// with the digest of the reference if it has one. A reference to the document
// of another topology names nothing.
func (r DocumentReference) Names(doc *PublishedDocument) bool {
	if doc == nil {
		return false
	}

	return r.StoredID(doc.Target) == doc.ID && (r.Digest == "" || r.Digest == doc.Digest)
}

// Publishes reports whether the reference names the document that a
// publication of the content with this digest stores for the topology named
// topology. The reference was read from that topology. Publishes uses
// whichever of the digest and the ID the reference holds. It reads no record.
func (r DocumentReference) Publishes(topology, digest string) bool {
	return r.StoredID(topology) == PublishedDocumentID(topology, digest) && (r.Digest == "" || r.Digest == digest)
}

// PutPublishedDocumentRequest stores an immutable copy of a published document.
type PutPublishedDocumentRequest struct {
	// Target and Kind identify the config the document was published to.
	Target string
	Kind   string
	Actor  string
	// Document holds the canonical JSON encoding of the document, as
	// [EncodeDocument] returns it.
	Document []byte
	// DraftID and SnapshotID optionally record where the document came from.
	DraftID    string
	SnapshotID string
}

// PutPublishedDocument stores an immutable, content-addressed copy of a
// published document in one transaction and returns it. The same content
// published again keeps the intact document but writes its metadata again, so
// the cleanup grace period starts again. A fresh copy repairs a corrupt one.
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

	load, err := buildPayload(canonical)
	if err != nil {
		return nil, err
	}

	documentID := PublishedDocumentID(req.Target, load.digest)

	doc := &PublishedDocument{
		ID:             documentID,
		Digest:         load.digest,
		Size:           load.size,
		CompressedSize: int64(len(load.compressed)),
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

	// Encode the metadata before the transaction, so an encoding failure
	// writes nothing.
	value, err := encodePublished(doc)
	if err != nil {
		return nil, err
	}

	var stored *PublishedDocument

	err = s.store.RecordTxn(func(tx store.RecordTx) error {
		var err error

		stored, err = putPublished(tx, doc, value, load)

		return err
	})
	if err != nil {
		return nil, txnError(kindPublished, documentID, store.AnyRevision, err)
	}

	return stored, nil
}

// putPublished stores doc, whose encoding is value and whose content is
// load, in a transaction (see [Service.PutPublishedDocument]). It returns
// the document as stored: doc, or the intact document already stored under
// its ID.
func putPublished(tx store.RecordTx, doc *PublishedDocument, value []byte, load *payload) (*PublishedDocument, error) {
	record, err := tx.GetRecord(NamespacePublished, doc.ID)

	switch {
	case errors.Is(err, store.ErrRecordNotExist):
		if err := putContent(tx, publishedKey(doc.ID), load); err != nil {
			return nil, err
		}

		record, err = tx.CreateRecord(NamespacePublished, doc.ID, value)
		if err != nil {
			return nil, storeError(kindPublished, doc.ID, store.AnyRevision, err)
		}

		return doc.stored(record), nil
	case err != nil:
		return nil, storeError(kindPublished, doc.ID, store.AnyRevision, err)
	}

	existing, err := decodePublished(record)
	if err != nil {
		return nil, err
	}

	_, err = readPayload(tx, kindPublished, existing.ID, publishedKey(existing.ID), existing.manifest())

	switch {
	case err == nil:
		// Store it again unchanged, so its record says when it was published.
		record, err = tx.UpdateRecord(NamespacePublished, doc.ID, record.Value, record.Revision)
		if err != nil {
			return nil, storeError(kindPublished, doc.ID, record.Revision, err)
		}

		return existing.stored(record), nil
	case !errors.Is(err, ErrCorrupt):
		return nil, err
	}

	// The content is corrupt or missing, so a fresh copy replaces it.
	if err := putContent(tx, publishedKey(doc.ID), load); err != nil {
		return nil, err
	}

	replaced, err := tx.UpdateRecord(NamespacePublished, doc.ID, value, record.Revision)
	if err != nil {
		return nil, storeError(kindPublished, doc.ID, record.Revision, err)
	}

	return doc.stored(replaced), nil
}

// stored returns the document with the revision and the time of publication
// of the record it was stored in.
func (p *PublishedDocument) stored(record store.Record) *PublishedDocument {
	stored := *p
	stored.Revision = record.Revision
	stored.published = record.Updated

	return &stored
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

	data, err := readPayload(s.store, kindPublished, doc.ID, publishedKey(doc.ID), doc.manifest())
	if err != nil {
		return nil, nil, err
	}

	if _, err := parseStored(kindPublished, doc.ID, data); err != nil {
		return nil, nil, err
	}

	return doc, data, nil
}

// ListPublishedDocuments returns every published document whose metadata
// decodes and validates, ordered by ID. It logs and skips each other record,
// so one damaged record does not hide or block the others (see
// [Service.DeleteDamagedDocument]).
func (s *Service) ListPublishedDocuments(ctx context.Context) ([]PublishedDocument, error) {
	docs, damaged, errs, err := s.listReadablePublished(ctx)
	if err != nil {
		return nil, err
	}

	for i, id := range damaged {
		plog.Warn(plog.TypeSystem, "skipping unreadable builder published document", "document", id, "err", errs[i])
	}

	return docs, nil
}

// DamagedDocument is what can still be read of a published document whose
// metadata does not decode or validate. It always has the ID and revision. It
// has the kind and target when the record still holds them as text.
type DamagedDocument struct {
	ID       string
	Kind     string
	Target   string
	Revision int64
}

// ETag returns the entity tag that deletes the damaged document.
func (d *DamagedDocument) ETag() string {
	return RevisionETag(d.Revision)
}

// GetDamagedDocument returns what can still be read of a published document
// whose metadata does not decode or validate. A document that decodes gives
// an error that matches [ErrInvalid].
func (s *Service) GetDamagedDocument(ctx context.Context, documentID string) (*DamagedDocument, error) {
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

	if err := checkDamaged(record); err != nil {
		return nil, err
	}

	damaged := &DamagedDocument{ID: documentID, Kind: "", Target: "", Revision: record.Revision}

	var fields struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
	}

	if json.Unmarshal(record.Value, &fields) == nil &&
		validateText("target", fields.Target, MaxTargetLength, true) == nil &&
		validateText("kind", fields.Kind, MaxKindLength, true) == nil {
		damaged.Kind, damaged.Target = fields.Kind, fields.Target
	}

	return damaged, nil
}

// checkDamaged returns an error that matches [ErrInvalid] when the published
// record decodes and validates.
func checkDamaged(record store.Record) error {
	if _, err := decodePublished(record); err == nil {
		return newValidationError("documentID", "the published document is not damaged")
	}

	return nil
}

// DeleteDamagedDocument removes a published document whose metadata does not
// decode or validate, and its content, in one transaction. The record must
// still be at revision. Thus it never removes a record that was stored again
// after it was read. A document that decodes gives an error that matches
// [ErrInvalid].
func (s *Service) DeleteDamagedDocument(ctx context.Context, documentID string, revision int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting published document %s: %w", documentID, err)
	}

	if err := validateID("documentID", documentID); err != nil {
		return err
	}

	err := s.store.RecordTxn(func(tx store.RecordTx) error {
		record, err := tx.GetRecord(NamespacePublished, documentID)
		if err != nil {
			return storeError(kindPublished, documentID, revision, err)
		}

		if record.Revision != revision {
			return &ConflictError{Kind: kindPublished, ID: documentID, Expected: revision, Actual: record.Revision, Reason: ""}
		}

		if err := checkDamaged(record); err != nil {
			return err
		}

		if err := tx.DeleteRecord(NamespacePublished, documentID, revision); err != nil {
			return storeError(kindPublished, documentID, revision, err)
		}

		return dropContent(tx, publishedKey(documentID))
	})

	return txnError(kindPublished, documentID, revision, err)
}

// listReadablePublished returns the published documents that decode, ordered
// by ID, and the keys of the records that do not, each with why. A cleanup
// uses it, so one damaged record does not stop the cleanup of the others.
func (s *Service) listReadablePublished(ctx context.Context) ([]PublishedDocument, []string, []error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("listing published documents: %w", err)
	}

	records, err := s.store.ListRecords(NamespacePublished, "")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing published documents: %w", err)
	}

	var (
		docs       = make([]PublishedDocument, 0, len(records))
		unreadable []string
		errs       []error
	)

	for _, record := range records {
		doc, err := decodePublished(record)
		if err != nil {
			unreadable = append(unreadable, record.Key)
			errs = append(errs, err)

			continue
		}

		docs = append(docs, *doc)
	}

	return docs, unreadable, errs, nil
}

// deletePublished removes a published document at the revision it was read
// at, and its content, in one transaction.
func (s *Service) deletePublished(documentID string, revision int64) error {
	err := s.store.RecordTxn(func(tx store.RecordTx) error {
		if err := tx.DeleteRecord(NamespacePublished, documentID, revision); err != nil {
			return storeError(kindPublished, documentID, revision, err)
		}

		return dropContent(tx, publishedKey(documentID))
	})

	return txnError(kindPublished, documentID, revision, err)
}

// deleteListed removes a document that a listing returned, and reports whether
// it was removed. It skips, without an error, a document that was removed or
// stored again after the listing. That document is no longer the document the
// listing saw.
func (s *Service) deleteListed(doc *PublishedDocument) (bool, error) {
	err := s.deletePublished(doc.ID, doc.Revision)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound):
		return false, nil
	}

	return false, err
}

// DeleteSupersededDocuments removes every published document of a target
// except keepID and those within the [OrphanGracePeriod], which may belong to
// a publication in progress. It returns the count removed, also with an error
// that matches [ErrCleanup].
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
// recent, so the caller must make sure no publication is in progress. It
// returns the count removed, also with an error that matches [ErrCleanup].
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
	// Updated is the metadata.updated time of the config. The store keeps it
	// to the second, so it is the start of the second the config was last
	// written in. It is zero when the time is not known.
	Updated time.Time
}

// DeleteConfigDocuments removes the documents of a deleted config that were
// stored before its last write, known to the second, and those older than the
// [OrphanGracePeriod]. It keeps a younger document, which may belong to a
// publication in progress. It returns the count removed, also with an error
// that matches [ErrCleanup].
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
		// named is when the document the config named was stored, if that was
		// no later than the second the config was last written in.
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

// deleteDocumentsWhere removes every published document that remove selects.
// It returns the number of documents removed. It reports a failure as an error
// that matches [ErrCleanup] for the operation, with the count of documents
// that were removed.
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
