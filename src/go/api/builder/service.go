package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/gofrs/uuid/v5"

	"phenix/store"
	"phenix/types/builder"
	"phenix/util/plog"
)

// Kinds named in typed errors.
const (
	kindDraft     = "draft"
	kindSnapshot  = "snapshot"
	kindPublished = "published document"
)

// KindDraft is the Kind of a [ConflictError] or a [NotFoundError] about a
// draft.
const KindDraft = kindDraft

// Clock returns the current time. Tests inject it to control the timestamps
// written to metadata.
type Clock func() time.Time

// IDSource returns a new unique identifier. Tests inject it to produce
// deterministic draft and snapshot IDs.
type IDSource func() (string, error)

// Options configures a [Service].
type Options struct {
	Store store.RecordStore
	Clock Clock
	IDs   IDSource
}

// Option configures a [Service].
type Option func(*Options)

// Service persists builder drafts and published documents. It is safe for
// concurrent use. All mutations are optimistic compare-and-swap operations
// against the injected record store.
type Service struct {
	store store.RecordStore
	clock Clock
	newID IDSource
	// serverTemplates holds the collections the server read from its
	// template files, which it keeps in memory only (see
	// [Service.LoadServerTemplates]).
	serverTemplates atomic.Pointer[[]ServerCollection]
}

// DocumentOrigin is a document that the caller read itself, from the store or
// from a Builder file, and opens a new draft from. It is a trusted input, as
// Actor is. A caller must never take it from a request.
type DocumentOrigin struct {
	// Digest is the digest of the canonical JSON of the origin. A request
	// whose document has this digest is that document, byte for byte. Thus the
	// caller does not need to give anything else of the origin.
	Digest string
}

// CreateDraftRequest describes a new draft. Actor is a trusted input from the
// caller, which does the authorization. The draft records it as its creator.
type CreateDraftRequest struct {
	Actor string
	// Title is used only when the document carries no name of its own.
	Title string
	// SourceToken optionally records where the document came from (see
	// [DraftMetadata.SourceToken]).
	SourceToken string
	// SourceFile optionally records the name of the uploaded file the
	// document came from (see [DraftMetadata.SourceFile]).
	SourceFile string
	// Forked optionally records what the draft this one forks had published.
	Forked *ForkedPublication
	// Origin is the document the draft is opened from, when the caller read
	// one itself. A draft whose document is that document unchanged stores it
	// as it is (see [Service.CreateDraft]).
	Origin *DocumentOrigin
	// Document holds a JSON encoded builder document. It is decoded, validated,
	// and canonicalized before anything is stored.
	Document []byte
	Summary  string
}

// AppendSnapshotRequest appends a new snapshot to a draft, truncating any redo
// branch after the cursor.
type AppendSnapshotRequest struct {
	DraftID string
	Actor   string
	// ExpectedRevision is the draft record revision the caller observed. Pass
	// [phenix/store.AnyRevision] to skip the check (not recommended for
	// interactive editing).
	ExpectedRevision int64
	// Document holds a JSON encoded builder document. It is decoded, validated,
	// and canonicalized before anything is stored.
	Document []byte
	Summary  string
	// OpID optionally records the client's id for this save in the snapshot
	// manifest (see [SnapshotManifest.OpID]).
	OpID string
}

// MoveCursorRequest moves a draft's cursor within its history (undo/redo).
// Exactly one of Index or SnapshotID must be set.
type MoveCursorRequest struct {
	DraftID          string
	Actor            string
	ExpectedRevision int64
	Index            int
	SnapshotID       string
}

// DeleteSnapshotRequest removes one snapshot from a draft's history.
type DeleteSnapshotRequest struct {
	DraftID          string
	Actor            string
	ExpectedRevision int64
	SnapshotID       string
}

// MarkPublishedRequest records the publication operation a caller did for a
// draft snapshot. This package never creates configs or experiments. It only
// records what the caller reports.
type MarkPublishedRequest struct {
	DraftID          string
	Actor            string
	ExpectedRevision int64
	// SnapshotID must name the snapshot the cursor currently points at.
	SnapshotID string
	// Mode is the publication operation that was performed.
	Mode PublishMode
	// TopologyTarget is the topology config the draft was published to.
	TopologyTarget string
	// TopologyAction records whether the topology was created or updated.
	TopologyAction TopologyAction
	// ExperimentTarget is required for [PublishModeTopologyExperiment] and must
	// be empty otherwise.
	ExperimentTarget string
	// ScenarioTarget optionally names the scenario an experiment was created
	// with. It is only meaningful for [PublishModeTopologyExperiment].
	ScenarioTarget string
	// DocumentID optionally links to an immutable published document.
	DocumentID string
}

// WithStore sets the record store the service persists to.
func WithStore(recordStore store.RecordStore) Option {
	return func(o *Options) { o.Store = recordStore }
}

// WithClock sets the clock used for metadata timestamps.
func WithClock(clock Clock) Option {
	return func(o *Options) { o.Clock = clock }
}

// WithIDSource sets the source of generated draft and snapshot IDs.
func WithIDSource(ids IDSource) Option {
	return func(o *Options) { o.IDs = ids }
}

// New returns a service using the given options. The record store defaults to
// [phenix/store.DefaultStore], the clock to [time.Now], and identifiers to
// random UUIDs. It fails when there is no record store.
func New(opts ...Option) (*Service, error) {
	options := Options{Store: nil, Clock: nil, IDs: nil}

	for _, opt := range opts {
		opt(&options)
	}

	if options.Store == nil {
		options.Store = store.DefaultStore
	}

	if options.Store == nil {
		return nil, newValidationError("store", "the Builder service needs a record store")
	}

	if options.Clock == nil {
		options.Clock = time.Now
	}

	if options.IDs == nil {
		options.IDs = uuidSource
	}

	return &Service{
		store:           options.Store,
		clock:           options.Clock,
		newID:           options.IDs,
		serverTemplates: atomic.Pointer[[]ServerCollection]{},
	}, nil
}

func uuidSource() (string, error) {
	id, err := uuid.NewV4()
	if err != nil {
		return "", fmt.Errorf("generating identifier: %w", err)
	}

	return id.String(), nil
}

// DamagedDraft is what can still be read of a draft whose metadata no longer
// decodes or validates, for example a draft that a newer phenix wrote. It
// always has the ID and revision. It has the creator, title and last update
// when the record still holds them in a usable form. Such a draft can only be
// deleted (see [Service.GetDamagedDraft]).
type DamagedDraft struct {
	ID        string
	CreatedBy string
	Title     string
	Updated   time.Time
	Revision  int64
}

// ETag returns the entity tag that deletes the draft, as [DraftMetadata.ETag]
// does for a readable one.
func (d *DamagedDraft) ETag() string {
	return RevisionETag(d.Revision)
}

// ListDraftsWithDamaged returns the metadata of every draft. Separately, it
// returns what can still be read of every draft whose metadata does not decode
// or validate. Both lists are ordered by draft ID. It logs each such draft.
// Thus one damaged record, or one that a newer version wrote, never hides all
// other drafts, and it can still be deleted.
func (s *Service) ListDraftsWithDamaged(ctx context.Context) ([]DraftMetadata, []DamagedDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("listing drafts: %w", err)
	}

	records, err := s.store.ListRecords(NamespaceDrafts, "")
	if err != nil {
		return nil, nil, fmt.Errorf("listing drafts: %w", err)
	}

	var (
		drafts  = make([]DraftMetadata, 0, len(records))
		damaged []DamagedDraft
	)

	for _, record := range records {
		meta, err := decodeDraft(record)
		if err != nil {
			plog.Warn(
				plog.TypeSystem,
				"skipping unreadable builder draft",
				"draft", record.Key,
				"revision", record.Revision,
				"err", err,
			)

			damaged = append(damaged, readDamagedDraft(record))

			continue
		}

		drafts = append(drafts, *meta)
	}

	return drafts, damaged, nil
}

// listDrafts lists every draft whose metadata decodes and validates, and the
// IDs of the drafts whose metadata does not. A caller that removes content no
// listed draft references must not mistake the content of such a draft for
// orphaned content.
func (s *Service) listDrafts(ctx context.Context) ([]DraftMetadata, []string, error) {
	drafts, damaged, err := s.ListDraftsWithDamaged(ctx)
	if err != nil {
		return nil, nil, err
	}

	unreadable := make([]string, 0, len(damaged))

	for i := range damaged {
		unreadable = append(unreadable, damaged[i].ID)
	}

	return drafts, unreadable, nil
}

// readDamagedDraft reads what it can of a draft record whose metadata does not
// decode or validate. It reads each field separately, so a field of the wrong
// type does not hide the others. It leaves empty a field that would not pass
// validation.
func readDamagedDraft(record store.Record) DamagedDraft {
	damaged := DamagedDraft{ //nolint:exhaustruct // the rest is read below, if it can be
		ID:       record.Key,
		Revision: record.Revision,
	}

	var fields map[string]json.RawMessage

	if json.Unmarshal(record.Value, &fields) != nil {
		return damaged
	}

	var (
		createdBy, title string
		updated          time.Time
	)

	if json.Unmarshal(fields["createdBy"], &createdBy) == nil &&
		validateText("createdBy", createdBy, MaxOwnerLength, true) == nil {
		damaged.CreatedBy = createdBy
	}

	if json.Unmarshal(fields["title"], &title) == nil &&
		validateText("title", title, MaxTitleLength, false) == nil {
		damaged.Title = title
	}

	if json.Unmarshal(fields["updated"], &updated) == nil {
		damaged.Updated = updated
	}

	return damaged
}

// GetDraft returns the current metadata of a draft, including its ordered
// history manifests, cursor, publication state, and record revision.
func (s *Service) GetDraft(ctx context.Context, draftID string) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting draft %s: %w", draftID, err)
	}

	if err := validateID("draftID", draftID); err != nil {
		return nil, err
	}

	record, err := s.store.GetRecord(NamespaceDrafts, draftID)
	if err != nil {
		return nil, storeError(kindDraft, draftID, store.AnyRevision, err)
	}

	return decodeDraft(record)
}

// GetDamagedDraft returns the ID, creator, and record revision of a draft, and
// no other metadata. Unlike [Service.GetDraft], it reads the record leniently.
// Thus it also succeeds for a draft whose metadata fails validation, so the
// caller can delete such a draft. The creator is empty when the record holds
// no usable one.
func (s *Service) GetDamagedDraft(ctx context.Context, draftID string) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting draft %s: %w", draftID, err)
	}

	if err := validateID("draftID", draftID); err != nil {
		return nil, err
	}

	record, err := s.store.GetRecord(NamespaceDrafts, draftID)
	if err != nil {
		return nil, storeError(kindDraft, draftID, store.AnyRevision, err)
	}

	// Read the record as a listing reads a damaged draft.
	damaged := readDamagedDraft(record)

	return &DraftMetadata{ //nolint:exhaustruct // only the fields a delete needs
		ID:        draftID,
		CreatedBy: damaged.CreatedBy,
		Revision:  record.Revision,
	}, nil
}

// GetCurrentDocument returns the snapshot the cursor points at, with verified
// document bytes.
func (s *Service) GetCurrentDocument(ctx context.Context, draftID string) (*Snapshot, error) {
	meta, err := s.GetDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}

	current := meta.Current()
	if current == nil {
		return nil, newCorruptError(kindDraft, draftID, "history is empty")
	}

	return s.snapshot(draftID, *current)
}

// GetSnapshot returns a specific snapshot of a draft, with verified document
// bytes.
func (s *Service) GetSnapshot(ctx context.Context, draftID, snapshotID string) (*Snapshot, error) {
	meta, err := s.GetDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}

	manifest := meta.Snapshot(snapshotID)
	if manifest == nil {
		return nil, newNotFoundError(kindSnapshot, snapshotID)
	}

	return s.snapshot(draftID, *manifest)
}

// CreateDraft validates, stamps and stores a new draft with one snapshot, in
// one transaction, and returns its metadata. It keeps the creator of the
// document, and stores an unchanged copy of req.Origin as it is, so its digest
// stays. It refuses a draft past [MaxDrafts] with a [DraftLimitError].
func (s *Service) CreateDraft(ctx context.Context, req CreateDraftRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("creating draft: %w", err)
	}

	if err := validateCreateRequest(req); err != nil {
		return nil, err
	}

	if err := s.checkDraftLimit(); err != nil {
		return nil, err
	}

	draftID, err := s.draftID()
	if err != nil {
		return nil, err
	}

	doc, err := parseDocument(req.Document)
	if err != nil {
		return nil, err
	}

	// The one clock read of this call: the document, its manifest and the
	// draft record all hold the same time.
	now := s.clock().UTC()

	canonical, err := firstDocument(doc, req, now)
	if err != nil {
		return nil, err
	}

	title, err := documentTitle(doc, req.Title)
	if err != nil {
		return nil, err
	}

	load, err := buildPayload(canonical)
	if err != nil {
		return nil, err
	}

	manifest, err := s.manifest(load, req.Actor, req.Summary, now)
	if err != nil {
		return nil, err
	}

	stamp := doc.Provenance()

	meta := &DraftMetadata{
		ID:                draftID,
		CreatedBy:         req.Actor,
		Title:             title,
		SourceToken:       req.SourceToken,
		SourceFile:        req.SourceFile,
		Created:           now,
		Updated:           now,
		LastModifiedBy:    req.Actor,
		History:           []SnapshotManifest{manifest},
		Cursor:            0,
		Publication:       nil,
		Forked:            nil,
		DocumentCreatedBy: stamp.CreatedBy,
		DocumentCreatedAt: stamp.CreatedAt,
		Revision:          store.AnyRevision,
		Stamp:             &stamp,
	}

	if req.Forked != nil {
		forked := *req.Forked
		meta.Forked = &forked
	}

	value, err := encodeDraft(meta)
	if err != nil {
		return nil, err
	}

	err = s.store.RecordTxn(func(tx store.RecordTx) error {
		if err := putContent(tx, snapshotKey(draftID, manifest.ID), load); err != nil {
			return err
		}

		record, err := tx.CreateRecord(NamespaceDrafts, draftID, value)
		if err != nil {
			return storeError(kindDraft, draftID, store.AnyRevision, err)
		}

		meta.Revision = record.Revision

		return nil
	})
	if err != nil {
		return nil, txnError(kindDraft, draftID, store.AnyRevision, err)
	}

	return meta, nil
}

// DraftLimitError reports that the server holds [MaxDrafts] drafts, and
// refuses one more. It unwraps to [ErrTooLarge].
type DraftLimitError struct {
	Limit int
}

func (e *DraftLimitError) Error() string {
	return fmt.Sprintf("%v: the server holds at most %d drafts", ErrTooLarge, e.Limit)
}

// Unwrap allows [errors.Is](err, ErrTooLarge) to succeed.
func (e *DraftLimitError) Unwrap() error { return ErrTooLarge }

// checkDraftLimit refuses a new draft when the server holds [MaxDrafts]
// drafts. A damaged draft counts, because it holds storage until it is
// deleted.
func (s *Service) checkDraftLimit() error {
	keys, err := s.store.ListRecordKeys(NamespaceDrafts, "")
	if err != nil {
		return fmt.Errorf("counting drafts: %w", err)
	}

	if len(keys) >= MaxDrafts {
		return &DraftLimitError{Limit: MaxDrafts}
	}

	return nil
}

// firstDocument returns the canonical bytes a new draft is stored with. It
// returns the origin unchanged, and stamps any other document: it keeps the
// creator the document names, and sets the last editor to the actor and now.
func firstDocument(doc *builder.Document, req CreateDraftRequest, now time.Time) ([]byte, error) {
	if req.Origin != nil {
		// A copy too large to store is refused below, as any document is.
		if canonical, err := encodeDocument(doc); err == nil && digestOf(canonical) == req.Origin.Digest {
			return canonical, nil
		}
	}

	stamp := doc.Provenance()

	if stamp.CreatedBy == "" {
		stamp.CreatedBy = req.Actor
	}

	if stamp.CreatedAt == "" {
		stamp.CreatedAt = builder.FormatTime(now)
	}

	stamp.UpdatedBy = req.Actor
	stamp.UpdatedAt = builder.FormatTime(now)

	doc.SetProvenance(stamp)

	return encodeDocument(doc)
}

// AppendSnapshot appends a snapshot to a draft and moves the cursor to it. It
// discards the redo branch, prunes the oldest snapshots to the limits, and
// checks all limits before it writes. It writes everything in one transaction.
// It stamps the document with the creator the draft records, the actor and now.
func (s *Service) AppendSnapshot(ctx context.Context, req AppendSnapshotRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("appending snapshot: %w", err)
	}

	if err := validateAppendRequest(req); err != nil {
		return nil, err
	}

	doc, err := parseDocument(req.Document)
	if err != nil {
		return nil, err
	}

	meta, err := s.GetDraft(ctx, req.DraftID)
	if err != nil {
		return nil, err
	}

	if err := checkRevision(req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	// Reject a rename here, before anything durable is written. Do not store
	// it under a silently shortened title.
	title, err := documentTitle(doc, meta.Title)
	if err != nil {
		return nil, err
	}

	// The one clock read of this call, made once the draft was read: the
	// document, its manifest and the draft record all hold the same time.
	now := s.clock().UTC()

	stamp := builder.Provenance{
		CreatedBy: meta.DocumentCreatedBy,
		CreatedAt: meta.DocumentCreatedAt,
		UpdatedBy: req.Actor,
		UpdatedAt: builder.FormatTime(now),
	}

	load, manifest, err := s.stampedSnapshot(doc, stamp, req.Summary, now)
	if err != nil {
		return nil, err
	}

	manifest.OpID = req.OpID

	updated := meta.Clone()
	truncated := slices.Clone(updated.History[updated.Cursor+1:])
	updated.History = append(updated.History[:updated.Cursor+1:updated.Cursor+1], manifest)
	updated.Cursor = len(updated.History) - 1
	stampDraft(updated, req.Actor, now)
	updated.Title = title
	updated.Stamp = &stamp

	dropped := slices.Concat(truncated, pruneHistory(updated))

	// Pruning never drops the new snapshot, so only a snapshot larger than the
	// whole budget can leave the history over it.
	if total := updated.HistoryBytes(); total > MaxDraftHistoryBytes {
		return nil, newTooLargeError("draft history", total, MaxDraftHistoryBytes)
	}

	value, err := encodeDraft(updated)
	if err != nil {
		return nil, err
	}

	err = s.writeDraftTxn(updated, value, meta.Revision, func(tx store.RecordTx) error {
		if err := putContent(tx, snapshotKey(req.DraftID, manifest.ID), load); err != nil {
			return err
		}

		return dropSnapshots(tx, req.DraftID, dropped)
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// MoveCursor moves the cursor of a draft to another snapshot in its history,
// and discards no snapshot. It records the move as an edit by the actor.
func (s *Service) MoveCursor(ctx context.Context, req MoveCursorRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("moving cursor: %w", err)
	}

	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	meta, err := s.GetDraft(ctx, req.DraftID)
	if err != nil {
		return nil, err
	}

	if err := checkRevision(req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	index, err := resolveCursor(meta, req)
	if err != nil {
		return nil, err
	}

	updated := meta.Clone()
	updated.Cursor = index
	stampDraft(updated, req.Actor, s.clock().UTC())

	if err := s.writeDraft(updated, meta.Revision); err != nil {
		return nil, err
	}

	return updated, nil
}

// DeleteSnapshot removes a snapshot that the cursor does not point at, with its
// content, in one transaction. It keeps a publication that names the snapshot,
// and the draft stays dirty.
func (s *Service) DeleteSnapshot(ctx context.Context, req DeleteSnapshotRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deleting snapshot: %w", err)
	}

	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	meta, err := s.GetDraft(ctx, req.DraftID)
	if err != nil {
		return nil, err
	}

	if err := checkRevision(req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	index := slices.IndexFunc(meta.History, func(manifest SnapshotManifest) bool {
		return manifest.ID == req.SnapshotID
	})

	switch index {
	case -1:
		return nil, newNotFoundError(kindSnapshot, req.SnapshotID)
	case meta.Cursor:
		return nil, &ConflictError{
			Kind:     kindDraft,
			ID:       req.DraftID,
			Expected: req.ExpectedRevision,
			Actual:   meta.Revision,
			Reason:   fmt.Sprintf("snapshot %q is the current snapshot", req.SnapshotID),
		}
	}

	updated := meta.Clone()
	removed := removeSnapshot(updated, index)
	stampDraft(updated, req.Actor, s.clock().UTC())

	value, err := encodeDraft(updated)
	if err != nil {
		return nil, err
	}

	err = s.writeDraftTxn(updated, value, meta.Revision, func(tx store.RecordTx) error {
		return dropSnapshots(tx, req.DraftID, []SnapshotManifest{removed})
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// MarkPublished records that the cursor's snapshot of a draft was published
// with the requested operation, so the draft is clean only for that snapshot
// and operation. An identical repeat returns the draft unchanged.
func (s *Service) MarkPublished(ctx context.Context, req MarkPublishedRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("marking draft published: %w", err)
	}

	if err := validatePublishRequest(req); err != nil {
		return nil, err
	}

	meta, err := s.GetDraft(ctx, req.DraftID)
	if err != nil {
		return nil, err
	}

	if isPublishRetry(meta, req) {
		return meta, nil
	}

	if err := checkRevision(req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	current := meta.Current()
	if current == nil {
		return nil, newCorruptError(kindDraft, req.DraftID, "history is empty")
	}

	if current.ID != req.SnapshotID {
		return nil, &ConflictError{
			Kind:     kindDraft,
			ID:       req.DraftID,
			Expected: req.ExpectedRevision,
			Actual:   meta.Revision,
			Reason:   fmt.Sprintf("snapshot %q is not the current snapshot %q", req.SnapshotID, current.ID),
		}
	}

	updated := meta.Clone()
	stampDraft(updated, req.Actor, s.clock().UTC())
	updated.Publication = &PublicationState{
		Mode:             req.Mode,
		TopologyTarget:   req.TopologyTarget,
		TopologyAction:   req.TopologyAction,
		ExperimentTarget: req.ExperimentTarget,
		ScenarioTarget:   req.ScenarioTarget,
		SnapshotID:       current.ID,
		Digest:           current.Digest,
		Revision:         meta.Revision,
		DocumentID:       req.DocumentID,
		PublishedAt:      updated.Updated,
		PublishedBy:      req.Actor,
	}

	if err := s.writeDraft(updated, meta.Revision); err != nil {
		return nil, err
	}

	return updated, nil
}

// DeleteDraft removes a draft and the content of every snapshot it holds, in
// one transaction. It deletes the metadata record with a compare-and-swap
// against the expected revision, so it never deletes a concurrently modified
// draft by accident.
func (s *Service) DeleteDraft(ctx context.Context, draftID, actor string, expectedRevision int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting draft %s: %w", draftID, err)
	}

	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return err
	}

	if err := validateID("draftID", draftID); err != nil {
		return err
	}

	err := s.store.RecordTxn(func(tx store.RecordTx) error {
		return deleteDraftTx(tx, draftID, expectedRevision)
	})

	return txnError(kindDraft, draftID, expectedRevision, err)
}

// deleteDraftTx removes a draft at the expected revision, and the content of
// every snapshot it holds, in a transaction.
func deleteDraftTx(tx store.RecordTx, draftID string, expectedRevision int64) error {
	if err := tx.DeleteRecord(NamespaceDrafts, draftID, expectedRevision); err != nil {
		return storeError(kindDraft, draftID, expectedRevision, err)
	}

	if _, err := tx.DeleteRecordPrefix(NamespaceContent, draftContentPrefix(draftID)); err != nil {
		return fmt.Errorf("deleting the content of draft %s: %w", draftID, err)
	}

	return nil
}

// draftID returns a validated generated draft ID.
func (s *Service) draftID() (string, error) {
	draftID, err := s.newID()
	if err != nil {
		return "", err
	}

	if err := validateID("draftID", draftID); err != nil {
		return "", err
	}

	return draftID, nil
}

// snapshot reads, verifies, and re-parses the document of a manifest.
func (s *Service) snapshot(draftID string, manifest SnapshotManifest) (*Snapshot, error) {
	data, err := readPayload(s.store, kindSnapshot, manifest.ID, snapshotKey(draftID, manifest.ID), manifest)
	if err != nil {
		return nil, err
	}

	parsed, err := parseStored(kindSnapshot, manifest.ID, data)
	if err != nil {
		return nil, err
	}

	return &Snapshot{Manifest: manifest, Data: data, parsed: parsed}, nil
}

// stampedSnapshot writes stamp into doc. It returns the payload of the stamped
// document and the manifest of the snapshot that stores it. The manifest names
// the user and the time that the stamp names as its last edit: now. The stamp
// counts towards [MaxDocumentBytes], like the rest of the document.
func (s *Service) stampedSnapshot(
	doc *builder.Document,
	stamp builder.Provenance,
	summary string,
	now time.Time,
) (*payload, SnapshotManifest, error) {
	doc.SetProvenance(stamp)

	canonical, err := encodeDocument(doc)
	if err != nil {
		return nil, SnapshotManifest{}, err
	}

	load, err := buildPayload(canonical)
	if err != nil {
		return nil, SnapshotManifest{}, err
	}

	manifest, err := s.manifest(load, stamp.UpdatedBy, summary, now)

	return load, manifest, err
}

// manifest describes a new snapshot of load, stored by actor at now.
func (s *Service) manifest(load *payload, actor, summary string, now time.Time) (SnapshotManifest, error) {
	snapshotID, err := s.newID()
	if err != nil {
		return SnapshotManifest{}, err
	}

	if err := validateID("snapshotID", snapshotID); err != nil {
		return SnapshotManifest{}, err
	}

	return SnapshotManifest{
		ID:             snapshotID,
		Digest:         load.digest,
		Size:           load.size,
		CompressedSize: int64(len(load.compressed)),
		CreatedAt:      now,
		CreatedBy:      actor,
		Summary:        summary,
		// The caller sets it. Only an appended snapshot records one.
		OpID: "",
	}, nil
}

// stampDraft records updated as changed at now, by actor. Every mutation
// stamps the draft record. Only a save stamps a document.
func stampDraft(updated *DraftMetadata, actor string, now time.Time) {
	updated.Updated = now
	updated.LastModifiedBy = actor
}

// writeDraft encodes updated and writes it with a compare-and-swap against
// the expected revision.
func (s *Service) writeDraft(updated *DraftMetadata, expected int64) error {
	value, err := encodeDraft(updated)
	if err != nil {
		return err
	}

	record, err := s.store.UpdateRecord(NamespaceDrafts, updated.ID, value, expected)
	if err != nil {
		return storeError(kindDraft, updated.ID, expected, err)
	}

	updated.Revision = record.Revision

	return nil
}

// writeDraftTxn runs also, then writes the pre-encoded metadata of updated
// with a compare-and-swap against the expected revision, all in one
// transaction. also writes the content that the change adds or removes.
func (s *Service) writeDraftTxn(
	updated *DraftMetadata,
	value []byte,
	expected int64,
	also func(store.RecordTx) error,
) error {
	err := s.store.RecordTxn(func(tx store.RecordTx) error {
		if err := also(tx); err != nil {
			return err
		}

		record, err := tx.UpdateRecord(NamespaceDrafts, updated.ID, value, expected)
		if err != nil {
			return storeError(kindDraft, updated.ID, expected, err)
		}

		updated.Revision = record.Revision

		return nil
	})

	return txnError(kindDraft, updated.ID, expected, err)
}

// dropSnapshots removes the content of every dropped snapshot of a draft in a
// transaction.
func dropSnapshots(tx store.RecordTx, draftID string, dropped []SnapshotManifest) error {
	for _, manifest := range dropped {
		if err := dropContent(tx, snapshotKey(draftID, manifest.ID)); err != nil {
			return err
		}
	}

	return nil
}

// pruneHistory drops the oldest snapshots, never the cursor's, down to
// [MaxSnapshots] and [MaxDraftHistoryBytes], and returns their manifests. It
// keeps a publication that names a dropped snapshot, as
// [Service.DeleteSnapshot] does.
func pruneHistory(meta *DraftMetadata) []SnapshotManifest {
	var dropped []SnapshotManifest

	total := meta.HistoryBytes()

	for (len(meta.History) > MaxSnapshots || total > MaxDraftHistoryBytes) && meta.Cursor > 0 {
		oldest := removeSnapshot(meta, 0)
		dropped = append(dropped, oldest)
		total -= oldest.Size
	}

	return dropped
}

// removeSnapshot removes the snapshot at index from the history and returns
// the removed manifest. That snapshot is never the one the cursor points at.
// The cursor stays on the snapshot it points at.
func removeSnapshot(meta *DraftMetadata, index int) SnapshotManifest {
	removed := meta.History[index]
	meta.History = slices.Delete(meta.History, index, index+1)

	if index < meta.Cursor {
		meta.Cursor--
	}

	return removed
}

func resolveCursor(meta *DraftMetadata, req MoveCursorRequest) (int, error) {
	if req.SnapshotID != "" {
		for i := range meta.History {
			if meta.History[i].ID == req.SnapshotID {
				return i, nil
			}
		}

		return 0, newNotFoundError(kindSnapshot, req.SnapshotID)
	}

	if req.Index < 0 || req.Index >= len(meta.History) {
		return 0, newValidationError("index", fmt.Sprintf("must be between 0 and %d", len(meta.History)-1))
	}

	return req.Index, nil
}

// checkRevision returns a conflict unless the draft is at the expected
// revision or any revision is expected.
func checkRevision(draftID string, expected, actual int64) error {
	if expected == store.AnyRevision || expected == actual {
		return nil
	}

	return &ConflictError{Kind: kindDraft, ID: draftID, Expected: expected, Actual: actual, Reason: ""}
}

// isPublishRetry reports whether the recorded publication is already exactly
// what the request asks to record. Then repeating the request is a no-op, not
// a conflict. A caller that retries after a lost response still holds the
// revision it observed before the first attempt, so that revision is accepted
// too.
func isPublishRetry(meta *DraftMetadata, req MarkPublishedRequest) bool {
	state := meta.Publication
	if state == nil {
		return false
	}

	current := meta.Current()
	if current == nil || current.ID != req.SnapshotID {
		return false
	}

	same := state.Mode == req.Mode &&
		state.TopologyTarget == req.TopologyTarget &&
		state.TopologyAction == req.TopologyAction &&
		state.ExperimentTarget == req.ExperimentTarget &&
		state.ScenarioTarget == req.ScenarioTarget &&
		state.SnapshotID == req.SnapshotID &&
		state.Digest == current.Digest &&
		state.DocumentID == req.DocumentID

	if !same {
		return false
	}

	return req.ExpectedRevision == store.AnyRevision ||
		req.ExpectedRevision == meta.Revision ||
		req.ExpectedRevision == state.Revision
}

func validateCreateRequest(req CreateDraftRequest) error {
	for _, field := range []struct {
		name     string
		value    string
		max      int
		required bool
	}{
		{name: "actor", value: req.Actor, max: MaxOwnerLength, required: true},
		{name: "title", value: req.Title, max: MaxTitleLength, required: false},
		{name: "sourceToken", value: req.SourceToken, max: MaxSourceTokenLength, required: false},
		{name: "summary", value: req.Summary, max: MaxSummaryLength, required: false},
	} {
		if err := validateText(field.name, field.value, field.max, field.required); err != nil {
			return err
		}
	}

	if req.Origin != nil && !builder.IsDigest(req.Origin.Digest) {
		return newValidationError("origin", "digest is not a sha256 digest")
	}

	return ValidateSourceFile(req.SourceFile)
}

func validateAppendRequest(req AppendSnapshotRequest) error {
	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return err
	}

	if err := validateOptionalID("opId", req.OpID); err != nil {
		return err
	}

	return validateText("summary", req.Summary, MaxSummaryLength, false)
}

func validatePublishRequest(req MarkPublishedRequest) error {
	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return err
	}

	if err := validateID("snapshotID", req.SnapshotID); err != nil {
		return err
	}

	if err := validateOptionalID("documentID", req.DocumentID); err != nil {
		return err
	}

	if err := validateText("topologyTarget", req.TopologyTarget, MaxTargetLength, true); err != nil {
		return err
	}

	if err := validateText("experimentTarget", req.ExperimentTarget, MaxTargetLength, false); err != nil {
		return err
	}

	if err := validateText("scenarioTarget", req.ScenarioTarget, MaxTargetLength, false); err != nil {
		return err
	}

	return validatePublishTargets(req)
}

func validatePublishTargets(req MarkPublishedRequest) error {
	switch {
	case !req.Mode.Valid():
		return newValidationError("mode", fmt.Sprintf("unknown publish mode %q", req.Mode))
	case !req.TopologyAction.Valid():
		return newValidationError("topologyAction", fmt.Sprintf("unknown topology action %q", req.TopologyAction))
	case req.Mode == PublishModeTopologyExperiment && req.ExperimentTarget == "":
		return newValidationError("experimentTarget", "must not be empty when you publish a topology and an experiment")
	case req.Mode == PublishModeTopology && req.ExperimentTarget != "":
		return newValidationError("experimentTarget", "must be empty when you publish only a topology")
	case req.Mode == PublishModeTopology && req.ScenarioTarget != "":
		return newValidationError("scenarioTarget", "must be empty when you publish only a topology")
	}

	return nil
}

// encodeDraft encodes draft metadata and enforces [MaxMetadataBytes], which
// keeps a record well below the request limits of the backing stores.
func encodeDraft(meta *DraftMetadata) ([]byte, error) {
	value, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encoding draft %s: %w", meta.ID, err)
	}

	if len(value) > MaxMetadataBytes {
		return nil, newTooLargeError("draft metadata", int64(len(value)), MaxMetadataBytes)
	}

	return value, nil
}

func decodeDraft(record store.Record) (*DraftMetadata, error) {
	var meta DraftMetadata

	if err := decodeMetadata(kindDraft, record.Value, &meta); err != nil {
		return nil, newCorruptError(kindDraft, record.Key, err.Error())
	}

	if err := validateDraftMetadata(record.Key, &meta); err != nil {
		return nil, err
	}

	meta.Revision = record.Revision

	return &meta, nil
}
