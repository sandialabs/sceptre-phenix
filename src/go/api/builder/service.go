package builder

import (
	"context"
	"encoding/json"
	"errors"
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

// Clock returns the current time. It is injected so tests can control the
// timestamps written to metadata.
type Clock func() time.Time

// IDSource returns a new unique identifier. It is injected so tests can produce
// deterministic draft and snapshot IDs.
type IDSource func() (string, error)

// Options configures a [Service].
type Options struct {
	Store     store.RecordStore
	Clock     Clock
	IDs       IDSource
	ChunkSize int
}

// Option configures a [Service].
type Option func(*Options)

// Service persists builder drafts and published documents. It is safe for
// concurrent use: all mutations are optimistic, compare-and-swap operations
// against the injected record store.
type Service struct {
	store     store.RecordStore
	clock     Clock
	newID     IDSource
	chunkSize int
	// serverTemplates holds the collections the server read from its
	// template files, which it keeps in memory only (see
	// [Service.LoadServerTemplates]).
	serverTemplates atomic.Pointer[[]ServerCollection]
}

// DocumentOrigin is a document the caller read itself, from the store or from
// a Builder file, that a new draft is opened from. It is a trusted input, as
// Owner and Actor are: a caller must never take it from a request.
type DocumentOrigin struct {
	// Digest is the digest of the origin's canonical JSON. A request whose
	// document has this digest is that document, byte for byte, so nothing
	// else of the origin needs to be given.
	Digest string
}

// CreateDraftRequest describes a new draft. Owner and Actor are trusted inputs
// supplied by the caller, which is responsible for authorization.
type CreateDraftRequest struct {
	Owner string
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
	// ID optionally sets the draft ID. When empty an ID is generated.
	ID string
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
	// UseIndex selects Index even when SnapshotID is empty and Index is zero.
	UseIndex bool
}

// DeleteSnapshotRequest removes one snapshot from a draft's history.
type DeleteSnapshotRequest struct {
	DraftID          string
	Actor            string
	ExpectedRevision int64
	SnapshotID       string
}

// MarkPublishedRequest records the publication operation a caller performed for
// a draft snapshot. This package never creates configs or experiments; it only
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

// WithChunkSize overrides the content chunk size. It exists for tests; the
// default is [ChunkBytes].
func WithChunkSize(size int) Option {
	return func(o *Options) { o.ChunkSize = size }
}

// New returns a service using the given options. The record store defaults to
// [phenix/store.DefaultStore], the clock to [time.Now], and identifiers to
// random UUIDs.
func New(opts ...Option) (*Service, error) {
	options := Options{Store: nil, Clock: nil, IDs: nil, ChunkSize: ChunkBytes}

	for _, opt := range opts {
		opt(&options)
	}

	if options.Store == nil {
		options.Store = store.DefaultStore
	}

	if options.Clock == nil {
		options.Clock = time.Now
	}

	if options.IDs == nil {
		options.IDs = uuidSource
	}

	if options.ChunkSize <= 0 || options.ChunkSize > ChunkBytes {
		return nil, newValidationError("chunkSize", fmt.Sprintf("must be between 1 and %d bytes", ChunkBytes))
	}

	return &Service{
		store:           options.Store,
		clock:           options.Clock,
		newID:           options.IDs,
		chunkSize:       options.ChunkSize,
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
// decodes or validates, for example one written by a newer phenix. Its ID
// and revision always; its owner, title and last update when the record
// still holds them in a usable form. Such a draft can only be deleted, and
// only when its owner can be read (see [Service.GetDraftOwner]).
type DamagedDraft struct {
	ID       string
	Owner    string
	Title    string
	Updated  time.Time
	Revision int64
}

// ETag returns the entity tag that deletes the draft, as [DraftMetadata.ETag]
// does for a readable one.
func (d *DamagedDraft) ETag() string {
	return RevisionETag(d.Revision)
}

// ListDraftsWithDamaged returns the metadata of every draft and, apart, what
// can still be read of every draft whose metadata does not decode or
// validate, both ordered by draft ID. Each such draft is logged, so one
// damaged record, or one written by a newer version, never hides every other
// draft, and its owner can still delete it.
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
// listed draft references must not mistake such a draft's content for
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
// decode or validate. Each field is read on its own, so one of the wrong type
// does not hide the others, and a field that would not pass validation is
// left empty.
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
		owner, title string
		updated      time.Time
	)

	if json.Unmarshal(fields["owner"], &owner) == nil &&
		validateText("owner", owner, MaxOwnerLength, true) == nil {
		damaged.Owner = owner
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

// GetDraftOwner returns the ID, owner, and record revision of a draft, and no
// other metadata. Unlike [Service.GetDraft] it reads the record leniently, so
// it also succeeds for a draft whose metadata fails validation, as long as the
// record still names a usable owner. It exists so that the owner of such a
// draft can be authorized to delete it.
func (s *Service) GetDraftOwner(ctx context.Context, draftID string) (*DraftMetadata, error) {
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

	// Read as a damaged draft is listed, so every one listed with an owner
	// can be deleted.
	damaged := readDamagedDraft(record)

	if damaged.Owner == "" {
		return nil, newCorruptError(kindDraft, draftID, "metadata has no usable owner")
	}

	return &DraftMetadata{ //nolint:exhaustruct // only the fields an owner check needs
		ID:       draftID,
		Owner:    damaged.Owner,
		Revision: record.Revision,
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

// CreateDraft stores a new draft with a single snapshot and returns its
// metadata. The document is decoded, semantically validated, and canonicalized
// before it is hashed or stored; invalid documents are rejected with an error
// matching [ErrInvalid].
//
// The document is validated as it was sent, then stamped: its createdBy and
// createdAt are kept when it has them, and are otherwise the actor and now,
// and its updatedBy and updatedAt are always the actor and now. The createdBy
// and createdAt it is stored with are recorded in the draft, and every later
// snapshot carries them (see [Service.AppendSnapshot]). One document is not
// stamped: the unchanged copy of req.Origin, the document the draft is opened
// from. It is stored as it is, so the draft holds exactly the content that
// was opened, under the same digest. The returned metadata's Stamp holds the
// four values the stored document has.
func (s *Service) CreateDraft(ctx context.Context, req CreateDraftRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("creating draft: %w", err)
	}

	if err := validateCreateRequest(req); err != nil {
		return nil, err
	}

	draftID, err := s.draftID(req.ID)
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

	load, err := buildPayload(canonical, s.chunkSize)
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
		Owner:             req.Owner,
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
		Sharing:           nil,
		DocumentCreatedBy: stamp.CreatedBy,
		DocumentCreatedAt: stamp.CreatedAt,
		Revision:          store.AnyRevision,
		Stamp:             &stamp,
	}

	if req.Forked != nil {
		forked := *req.Forked
		meta.Forked = &forked
	}

	// Metadata is encoded (and size checked) before anything durable is
	// written, so an encoding failure can never leave chunks behind.
	value, err := encodeDraft(meta)
	if err != nil {
		return nil, err
	}

	scope := snapshotScope(draftID, manifest.ID)

	created, err := s.writeChunks(scope, load)
	if err != nil {
		return nil, err
	}

	record, err := s.store.CreateRecord(NamespaceDrafts, draftID, value)
	if err != nil {
		err = storeError(kindDraft, draftID, store.AnyRevision, err)

		if !writeRejected(err) {
			return s.settleAmbiguousWrite(draftID, manifest.ID, store.AnyRevision, stamp, err)
		}

		cleanupErrs := s.deleteChunkKeys(created)

		return nil, errors.Join(err, newCleanupError("creating draft", cleanupErrs))
	}

	meta.Revision = record.Revision

	return meta, nil
}

// firstDocument returns the canonical encoding of the document a new draft
// is stored with, and leaves doc as that document.
//
// The unchanged copy of the request's origin is returned as it is: its digest
// says it is the origin, byte for byte, and the origin was not this caller's
// edit. Any other document is stamped. It keeps the creator and the creation
// time it names, since a document made elsewhere was not made by the actor
// now, and takes the actor and now for whichever it lacks. Its last editor
// and last edit time are always the actor and now: nothing a request says of
// them is kept.
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

// AppendSnapshot appends a new snapshot to a draft.
//
// The redo branch (every snapshot after the cursor) is discarded, the oldest
// snapshots are pruned until at most [MaxSnapshots] snapshots of at most
// [MaxDraftHistoryBytes] in total remain, and the cursor is moved to the new
// snapshot. All limits are checked before anything durable is written.
//
// Content chunks are written to a scope private to the new snapshot before the
// metadata compare-and-swap; if the swap fails, exactly the chunks this attempt
// wrote are removed (never a concurrent winner's) and any failure to remove them
// is reported alongside the conflict. A store error that does not prove the
// swap failed is settled by reading the draft back (see
// [Service.settleAmbiguousWrite]).
//
// When the metadata write succeeded but removing chunks of discarded snapshots
// failed, the updated metadata is returned together with an error matching
// [ErrCleanup].
//
// The document is validated as it was sent, then stamped: its createdBy and
// createdAt become the ones the draft records (see
// [DraftMetadata.DocumentCreatedBy]), or are removed when the draft records
// none, and its updatedBy and updatedAt become the actor and now. Nothing a
// request says of the four is kept, so every snapshot names the user who
// really saved it. The same time is the snapshot's and the draft's, cut to
// whole seconds in the document. The returned metadata's Stamp holds the four
// values written.
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

	// A rename is rejected here, before anything durable is written, rather
	// than stored under a silently shortened title.
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

	scope := snapshotScope(req.DraftID, manifest.ID)

	created, err := s.writeChunks(scope, load)
	if err != nil {
		return nil, err
	}

	if err := s.saveDraft(updated, value, meta.Revision); err != nil {
		if !writeRejected(err) {
			stored, settleErr := s.settleAmbiguousWrite(req.DraftID, manifest.ID, meta.Revision, stamp, err)
			if settleErr != nil {
				return nil, settleErr
			}

			return stored, newCleanupError("appending snapshot", s.deleteSnapshotScopes(req.DraftID, dropped))
		}

		cleanupErrs := s.deleteChunkKeys(created)

		return nil, errors.Join(err, newCleanupError("appending snapshot", cleanupErrs))
	}

	// Chunks of discarded snapshots are only removed after the metadata is
	// durable. Every snapshot owns a private scope, so removing them can never
	// affect a retained or concurrently written snapshot.
	cleanupErrs := s.deleteSnapshotScopes(req.DraftID, dropped)

	return updated, newCleanupError("appending snapshot", cleanupErrs)
}

// MoveCursor moves a draft's cursor to another snapshot in its history without
// discarding any snapshot. The move is recorded as an edit by the actor.
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

// DeleteSnapshot removes a snapshot from a draft's history, then its chunks.
// The snapshot the cursor points at cannot be removed, and the cursor keeps
// pointing at the same snapshot. A publication naming the removed snapshot is
// kept: the draft stays dirty, since the cursor can never point at that
// snapshot again. The removal is recorded as an edit by the actor.
//
// Every snapshot owns a private chunk scope, so removing its chunks never
// affects another snapshot or a published document. When the metadata write
// succeeded but removing the chunks failed, the updated metadata is returned
// together with an error matching [ErrCleanup].
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

	// A write that failed may still be applied later, and until it is the
	// draft references the chunks, so they are left to
	// [Service.CleanupOrphanedChunks].
	if err := s.writeDraft(updated, meta.Revision); err != nil {
		return nil, err
	}

	cleanupErrs := s.deleteSnapshotScopes(req.DraftID, []SnapshotManifest{removed})

	return updated, newCleanupError("deleting snapshot", cleanupErrs)
}

// MarkPublished records that the named snapshot of a draft was published with
// the requested operation. The snapshot must be the one the cursor points at,
// so a draft can never be marked clean against content the user is no longer
// editing, and the draft is clean only for exactly that operation and snapshot.
//
// Repeating an identical request is idempotent: when the recorded publication
// already matches the request, the draft is returned unchanged even if the
// caller still holds the revision it observed before the first attempt.
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

// DeleteDraft removes a draft and every content chunk it owns. The metadata
// record is deleted with a compare-and-swap against the expected revision, so a
// concurrently modified draft is never deleted by accident. A failure to remove
// chunks after the metadata is gone is reported as an error matching
// [ErrCleanup].
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

	if err := s.store.DeleteRecord(NamespaceDrafts, draftID, expectedRevision); err != nil {
		return storeError(kindDraft, draftID, expectedRevision, err)
	}

	if err := s.deleteChunkScope(draftScope(draftID)); err != nil {
		return newCleanupError("deleting draft", []error{err})
	}

	return nil
}

// draftID returns the requested draft ID, or a validated generated one.
func (s *Service) draftID(requested string) (string, error) {
	draftID := requested

	if draftID == "" {
		generated, err := s.newID()
		if err != nil {
			return "", err
		}

		draftID = generated
	}

	if err := validateID("draftID", draftID); err != nil {
		return "", err
	}

	return draftID, nil
}

// snapshot reassembles, verifies, and re-parses the document of a manifest.
func (s *Service) snapshot(draftID string, manifest SnapshotManifest) (*Snapshot, error) {
	scope := snapshotScope(draftID, manifest.ID)

	data, err := s.readPayload(kindSnapshot, manifest.ID, scope, manifest)
	if err != nil {
		return nil, err
	}

	parsed, err := parseStored(kindSnapshot, manifest.ID, data)
	if err != nil {
		return nil, err
	}

	return &Snapshot{Manifest: manifest, Data: data, parsed: parsed}, nil
}

// stampedSnapshot writes stamp into doc and returns the payload of the
// stamped document and the manifest of the snapshot that stores it, by the
// user and at the time the stamp names as its last edit: now. The stamp
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

	load, err := buildPayload(canonical, s.chunkSize)
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
		CompressedSize: load.compressedSize,
		ChunkDigests:   load.chunkDigests,
		ChunkSize:      s.chunkSize,
		CreatedAt:      now,
		CreatedBy:      actor,
		Summary:        summary,
		// Set by the caller: only an appended snapshot records one.
		OpID: "",
	}, nil
}

// stampDraft records updated as changed at now, by actor. It is the draft
// record that is stamped, on every mutation: only a save stamps a document.
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

	return s.saveDraft(updated, value, expected)
}

// saveDraft writes pre-encoded metadata with a compare-and-swap.
func (s *Service) saveDraft(meta *DraftMetadata, value []byte, expectedRevision int64) error {
	record, err := s.store.UpdateRecord(NamespaceDrafts, meta.ID, value, expectedRevision)
	if err != nil {
		return storeError(kindDraft, meta.ID, expectedRevision, err)
	}

	meta.Revision = record.Revision

	return nil
}

// writeRejected reports whether a draft metadata write error proves the write
// was not applied: the store refused it as a conflict, because the record was
// missing or already existed, or because the key was invalid.
func writeRejected(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid)
}

// settleAmbiguousWrite settles a draft metadata write that failed with err, an
// error that does not prove the write was not applied (an etcd request can time
// out after its proposal was applied). The draft is read back. When its cursor
// points at snapshotID, the snapshot the write stored, the write was applied
// and the stored metadata is returned. When the draft holds the snapshot but
// has moved on since, a conflict is returned. Otherwise err is returned.
//
// The chunks of snapshotID are never removed here: a write that has not been
// applied yet may still be, so chunks that end up unreferenced are left to
// [Service.CleanupOrphanedChunks]. The stored metadata is returned with
// stamp, what the write put in the document of snapshotID.
func (s *Service) settleAmbiguousWrite(
	draftID, snapshotID string,
	expected int64,
	stamp builder.Provenance,
	err error,
) (*DraftMetadata, error) {
	record, getErr := s.store.GetRecord(NamespaceDrafts, draftID)
	if getErr != nil {
		return nil, err
	}

	stored, decodeErr := decodeDraft(record)
	if decodeErr != nil {
		return nil, err
	}

	if current := stored.Current(); current != nil && current.ID == snapshotID {
		stored.Stamp = &stamp

		return stored, nil
	}

	if stored.hasSnapshot(snapshotID) {
		return nil, &ConflictError{
			Kind:     kindDraft,
			ID:       draftID,
			Expected: expected,
			Actual:   stored.Revision,
			Reason:   fmt.Sprintf("the draft changed after snapshot %q was stored", snapshotID),
		}
	}

	return nil, err
}

// deleteSnapshotScopes removes the private chunk scope of every dropped
// snapshot.
func (s *Service) deleteSnapshotScopes(draftID string, dropped []SnapshotManifest) []error {
	var errs []error

	for _, manifest := range dropped {
		if err := s.deleteChunkScope(snapshotScope(draftID, manifest.ID)); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// pruneHistory drops the oldest snapshots until the history holds at most
// [MaxSnapshots] snapshots of at most [MaxDraftHistoryBytes] in total, never
// dropping the snapshot the cursor points at. It returns the dropped manifests
// and adjusts the cursor.
//
// A publication naming a dropped snapshot is kept, as [Service.DeleteSnapshot]
// keeps it, so the draft can still update what it published. The draft stays
// dirty, since the cursor can never point at that snapshot again.
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

// removeSnapshot removes the snapshot at index, which is never the one the
// cursor points at, from the history, keeping the cursor on the snapshot it
// points at, and returns the removed manifest.
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

// isPublishRetry reports whether the recorded publication already is exactly
// what the request asks to record, which makes repeating the request a no-op
// rather than a conflict. A caller retrying after a lost response still holds
// the revision it observed before the first attempt, so that revision is
// accepted too.
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
	if err := validateOptionalID("draftID", req.ID); err != nil {
		return err
	}

	for _, field := range []struct {
		name     string
		value    string
		max      int
		required bool
	}{
		{name: "owner", value: req.Owner, max: MaxOwnerLength, required: true},
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
		return newValidationError("experimentTarget", "must be set when publishing a topology and experiment")
	case req.Mode == PublishModeTopology && req.ExperimentTarget != "":
		return newValidationError("experimentTarget", "must be empty when publishing a topology only")
	case req.Mode == PublishModeTopology && req.ScenarioTarget != "":
		return newValidationError("scenarioTarget", "must be empty when publishing a topology only")
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
