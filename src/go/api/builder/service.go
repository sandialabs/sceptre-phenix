package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gofrs/uuid/v5"

	"phenix/store"
)

// Kinds named in typed errors.
const (
	kindDraft     = "draft"
	kindSnapshot  = "snapshot"
	kindPublished = "published document"
)

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
}

// CreateDraftRequest describes a new draft. Owner and Actor are trusted inputs
// supplied by the caller, which is responsible for authorization.
type CreateDraftRequest struct {
	Owner string
	Actor string
	// Title is used only when the document carries no name of its own.
	Title string
	// SourceToken optionally records the config the document was imported from.
	SourceToken string
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
		store:     options.Store,
		clock:     options.Clock,
		newID:     options.IDs,
		chunkSize: options.ChunkSize,
	}, nil
}

func uuidSource() (string, error) {
	id, err := uuid.NewV4()
	if err != nil {
		return "", fmt.Errorf("generating identifier: %w", err)
	}

	return id.String(), nil
}

// ListDrafts returns the metadata of every draft, ordered by draft ID. It is
// intended for administrative callers; use [Service.ListDraftsByOwner] for
// per-user views.
func (s *Service) ListDrafts(ctx context.Context) ([]DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing drafts: %w", err)
	}

	records, err := s.store.ListRecords(NamespaceDrafts, "")
	if err != nil {
		return nil, fmt.Errorf("listing drafts: %w", err)
	}

	drafts := make([]DraftMetadata, 0, len(records))

	for _, record := range records {
		meta, err := decodeDraft(record)
		if err != nil {
			return nil, err
		}

		drafts = append(drafts, *meta)
	}

	return drafts, nil
}

// ListDraftsByOwner returns the metadata of every draft owned by the given
// owner. Ownership is matched exactly; the caller remains responsible for
// authorizing the request.
func (s *Service) ListDraftsByOwner(ctx context.Context, owner string) ([]DraftMetadata, error) {
	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	drafts, err := s.ListDrafts(ctx)
	if err != nil {
		return nil, err
	}

	owned := make([]DraftMetadata, 0, len(drafts))

	for _, draft := range drafts {
		if draft.Owner == owner {
			owned = append(owned, draft)
		}
	}

	return owned, nil
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

	canonical, doc, err := canonicalDocument("document", req.Document)
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

	manifest, err := s.manifest(load, req.Actor, req.Summary)
	if err != nil {
		return nil, err
	}

	now := s.clock().UTC()

	meta := &DraftMetadata{
		ID:             draftID,
		Owner:          req.Owner,
		Title:          title,
		SourceToken:    req.SourceToken,
		Created:        now,
		Updated:        now,
		LastModifiedBy: req.Actor,
		History:        []SnapshotManifest{manifest},
		Cursor:         0,
		Publication:    nil,
		Revision:       store.AnyRevision,
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
		cleanupErrs := s.deleteChunkKeys(created)

		return nil, errors.Join(
			storeError(kindDraft, draftID, store.AnyRevision, err),
			newCleanupError("creating draft", cleanupErrs),
		)
	}

	meta.Revision = record.Revision

	return meta, nil
}

// AppendSnapshot appends a new snapshot to a draft.
//
// The redo branch (every snapshot after the cursor) is discarded, history is
// pruned to at most [MaxSnapshots] snapshots, and the cursor is moved to the new
// snapshot. An edit whose resulting history would exceed [MaxDraftHistoryBytes]
// is rejected with an error matching [ErrTooLarge]; retained history is never
// discarded to make room for it. All limits are checked before anything durable
// is written.
//
// Content chunks are written to a scope private to the new snapshot before the
// metadata compare-and-swap; if the swap fails, exactly the chunks this attempt
// wrote are removed (never a concurrent winner's) and any failure to remove them
// is reported alongside the conflict.
//
// When the metadata write succeeded but removing chunks of discarded snapshots
// failed, the updated metadata is returned together with an error matching
// [ErrCleanup].
func (s *Service) AppendSnapshot(ctx context.Context, req AppendSnapshotRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("appending snapshot: %w", err)
	}

	if err := validateAppendRequest(req); err != nil {
		return nil, err
	}

	canonical, doc, err := canonicalDocument("document", req.Document)
	if err != nil {
		return nil, err
	}

	meta, err := s.GetDraft(ctx, req.DraftID)
	if err != nil {
		return nil, err
	}

	if err := checkRevision(kindDraft, req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	// A rename is rejected here, before anything durable is written, rather
	// than stored under a silently shortened title.
	title, err := documentTitle(doc, meta.Title)
	if err != nil {
		return nil, err
	}

	load, err := buildPayload(canonical, s.chunkSize)
	if err != nil {
		return nil, err
	}

	manifest, err := s.manifest(load, req.Actor, req.Summary)
	if err != nil {
		return nil, err
	}

	updated := meta.Clone()
	truncated := slices.Clone(updated.History[updated.Cursor+1:])
	updated.History = append(updated.History[:updated.Cursor+1:updated.Cursor+1], manifest)
	updated.Cursor = len(updated.History) - 1
	updated.Updated = s.clock().UTC()
	updated.LastModifiedBy = req.Actor
	updated.Title = title

	dropped := slices.Concat(truncated, pruneHistory(updated))

	// The byte limit rejects the edit; it never prunes retained history.
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

	if err := checkRevision(kindDraft, req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
		return nil, err
	}

	index, err := resolveCursor(meta, req)
	if err != nil {
		return nil, err
	}

	updated := meta.Clone()
	updated.Cursor = index
	updated.Updated = s.clock().UTC()
	updated.LastModifiedBy = req.Actor

	value, err := encodeDraft(updated)
	if err != nil {
		return nil, err
	}

	if err := s.saveDraft(updated, value, meta.Revision); err != nil {
		return nil, err
	}

	return updated, nil
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

	if err := checkRevision(kindDraft, req.DraftID, req.ExpectedRevision, meta.Revision); err != nil {
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
	updated.Updated = s.clock().UTC()
	updated.LastModifiedBy = req.Actor
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

	value, err := encodeDraft(updated)
	if err != nil {
		return nil, err
	}

	if err := s.saveDraft(updated, value, meta.Revision); err != nil {
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

	if _, err := parseStored(kindSnapshot, manifest.ID, data); err != nil {
		return nil, err
	}

	return &Snapshot{Manifest: manifest, Data: data}, nil
}

func (s *Service) manifest(load *payload, actor, summary string) (SnapshotManifest, error) {
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
		CreatedAt:      s.clock().UTC(),
		CreatedBy:      actor,
		Summary:        summary,
	}, nil
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
// [MaxSnapshots] snapshots, never dropping the snapshot the cursor points at.
// It returns the dropped manifests and adjusts the cursor. The byte limit is
// never enforced by pruning: an edit that would exceed it is rejected instead.
func pruneHistory(meta *DraftMetadata) []SnapshotManifest {
	var dropped []SnapshotManifest

	for len(meta.History) > MaxSnapshots && meta.Cursor > 0 {
		dropped = append(dropped, meta.History[0])
		meta.History = meta.History[1:]
		meta.Cursor--
	}

	// Publication state must always point at a snapshot the draft still holds.
	// A publication whose snapshot aged out of the history is forgotten; the
	// draft was already dirty, because the cursor is never pruned and therefore
	// had moved past the published snapshot.
	if meta.Publication != nil && !meta.hasSnapshot(meta.Publication.SnapshotID) {
		meta.Publication = nil
	}

	return dropped
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

func checkRevision(kind, id string, expected, actual int64) error {
	if expected == store.AnyRevision || expected == actual {
		return nil
	}

	return &ConflictError{Kind: kind, ID: id, Expected: expected, Actual: actual, Reason: ""}
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

	return nil
}

func validateAppendRequest(req AppendSnapshotRequest) error {
	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
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
