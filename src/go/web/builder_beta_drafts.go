package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderDraftRequest creates a draft. The owner is always the authenticated
// user: it may be sent for symmetry with the response, but never to create a
// draft on somebody else's behalf.
type builderDraftRequest struct {
	Title       string          `json:"title"`
	SourceToken string          `json:"sourceToken"`
	Summary     string          `json:"summary"`
	Owner       string          `json:"owner"`
	Document    json.RawMessage `json:"document"`
}

func builderSnapshotHistory(meta *bapi.DraftMetadata) []builderSnapshotResponse {
	history := make([]builderSnapshotResponse, 0, len(meta.History))

	for i := range meta.History {
		history = append(history, newBuilderSnapshotResponse(meta.History[i], i == meta.Cursor))
	}

	return history
}

// builderSnapshotRequest appends a snapshot to a draft.
type builderSnapshotRequest struct {
	Summary  string          `json:"summary"`
	Document json.RawMessage `json:"document"`
}

// builderCursorRequest moves a draft's cursor. Exactly one of Index and
// SnapshotID must be set.
type builderCursorRequest struct {
	Index      *int   `json:"index"`
	SnapshotID string `json:"snapshotId"`
}

// builderDraftResponse is the JSON view of a draft. Chunk digests and other
// storage details are deliberately not exposed.
type builderDraftResponse struct {
	ID             string    `json:"id"`
	Owner          string    `json:"owner"`
	Title          string    `json:"title,omitempty"`
	SourceToken    string    `json:"sourceToken,omitempty"`
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
	LastModifiedBy string    `json:"lastModifiedBy"`
	Cursor         int       `json:"cursor"`
	Snapshots      int       `json:"snapshots"`
	SnapshotID     string    `json:"snapshotId,omitempty"`
	Digest         string    `json:"digest,omitempty"`
	Size           int64     `json:"size"`
	// HistoryBytes is the total size of the retained snapshots. It is reported
	// so a client can see how close a draft is to [bapi.MaxDraftHistoryBytes].
	HistoryBytes int64                     `json:"historyBytes"`
	Dirty        bool                      `json:"dirty"`
	CanUndo      bool                      `json:"canUndo"`
	CanRedo      bool                      `json:"canRedo"`
	ReadOnly     bool                      `json:"readOnly"`
	ETag         string                    `json:"etag"`
	Document     json.RawMessage           `json:"document,omitempty"`
	History      []builderSnapshotResponse `json:"history,omitempty"`

	Publication *builderPublicationResponse `json:"publication,omitempty"`
}

// builderPublicationResponse is the JSON view of a draft's last publication.
type builderPublicationResponse struct {
	Mode             string    `json:"mode"`
	TopologyTarget   string    `json:"topologyTarget"`
	TopologyAction   string    `json:"topologyAction"`
	ExperimentTarget string    `json:"experimentTarget,omitempty"`
	ScenarioTarget   string    `json:"scenarioTarget,omitempty"`
	SnapshotID       string    `json:"snapshotId"`
	Digest           string    `json:"digest,omitempty"`
	DocumentID       string    `json:"documentId,omitempty"`
	PublishedAt      time.Time `json:"publishedAt"`
	PublishedBy      string    `json:"publishedBy"`
}

// builderSnapshotResponse is the JSON view of one snapshot manifest.
type builderSnapshotResponse struct {
	ID        string    `json:"id"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
	Summary   string    `json:"summary,omitempty"`
	Current   bool      `json:"current"`
}

// builderSnapshotDocumentResponse is one snapshot together with its verified
// document bytes.
type builderSnapshotDocumentResponse struct {
	Snapshot builderSnapshotResponse `json:"snapshot"`
	Document json.RawMessage         `json:"document"`
}

// newBuilderDraftResponse converts draft metadata into its JSON view.
func newBuilderDraftResponse(meta *bapi.DraftMetadata) builderDraftResponse {
	response := builderDraftResponse{ //nolint:exhaustruct // snapshot fields depend on the cursor
		ID:             meta.ID,
		Owner:          meta.Owner,
		Title:          meta.Title,
		SourceToken:    meta.SourceToken,
		Created:        meta.Created,
		Updated:        meta.Updated,
		LastModifiedBy: meta.LastModifiedBy,
		Cursor:         meta.Cursor,
		Snapshots:      len(meta.History),
		HistoryBytes:   meta.HistoryBytes(),
		Dirty:          meta.Dirty(),
		CanUndo:        meta.CanUndo(),
		CanRedo:        meta.CanRedo(),
		ETag:           meta.ETag(),
	}

	if current := meta.Current(); current != nil {
		response.SnapshotID = current.ID
		response.Digest = current.Digest
		response.Size = current.Size
	}

	if meta.Publication != nil {
		response.Publication = &builderPublicationResponse{
			Mode:             string(meta.Publication.Mode),
			TopologyTarget:   meta.Publication.TopologyTarget,
			TopologyAction:   string(meta.Publication.TopologyAction),
			ExperimentTarget: meta.Publication.ExperimentTarget,
			ScenarioTarget:   meta.Publication.ScenarioTarget,
			SnapshotID:       meta.Publication.SnapshotID,
			Digest:           meta.Publication.Digest,
			DocumentID:       meta.Publication.DocumentID,
			PublishedAt:      meta.Publication.PublishedAt,
			PublishedBy:      meta.Publication.PublishedBy,
		}
	}

	return response
}

// newBuilderSnapshotResponse converts a snapshot manifest into its JSON view.
func newBuilderSnapshotResponse(
	manifest bapi.SnapshotManifest,
	current bool,
) builderSnapshotResponse {
	return builderSnapshotResponse{
		ID:        manifest.ID,
		Digest:    manifest.Digest,
		Size:      manifest.Size,
		CreatedAt: manifest.CreatedAt,
		CreatedBy: manifest.CreatedBy,
		Summary:   manifest.Summary,
		Current:   current,
	}
}

// listDrafts - GET /builder/drafts.
//
// The response separates the caller's own drafts from the drafts of other users
// the caller is explicitly allowed to see. Drafts the caller may not see are
// never counted, described, or otherwise hinted at.
func (b *builderBetaAPI) listDrafts(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaListDrafts")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "listing builder drafts")
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbList) {
		return builderBetaForbidden(actor, "listing builder drafts")
	}

	ctx := r.Context()

	owned, err := b.drafts.ListDraftsByOwner(ctx, actor.user)
	if err != nil {
		return builderBetaWebError(err, "unable to list builder drafts")
	}

	mine := make([]builderDraftResponse, 0, len(owned))

	for i := range owned {
		mine = append(mine, newBuilderDraftResponse(&owned[i]))
	}

	shared, err := b.sharedDrafts(r, actor)
	if err != nil {
		return err
	}

	return builderBetaWriteJSON(w, http.StatusOK, "", map[string]any{
		"drafts": mine,
		"shared": shared,
	})
}

// createDraft - POST /builder/drafts.
func (b *builderBetaAPI) createDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaCreateDraft")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "creating a builder draft")
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbCreate) {
		return builderBetaForbidden(actor, "creating a builder draft")
	}

	var request builderDraftRequest

	if err := builderBetaDecode(w, r, &request); err != nil {
		return err
	}

	// Drafts are always owned by the authenticated user. Creating one for
	// somebody else is not a permission that exists.
	if request.Owner != "" && request.Owner != actor.user {
		return builderBetaForbidden(actor, "creating a builder draft for "+request.Owner)
	}

	document, err := builderBetaDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	meta, err := b.drafts.CreateDraft(r.Context(), bapi.CreateDraftRequest{
		Owner:       actor.user,
		Actor:       actor.user,
		Title:       request.Title,
		SourceToken: request.SourceToken,
		Document:    document,
		Summary:     request.Summary,
		ID:          "",
	})

	meta, err = builderBetaMutation(
		w, meta, err, "create draft", actor.user, "unable to create the builder draft",
	)
	if err != nil {
		return err
	}

	plog.Info(plog.TypeAction, "created builder draft", "user", actor.user, "draft", meta.ID)

	w.Header().Set("Location", "/api/v1/builder/drafts/"+builderBetaDraftName(meta.Owner, meta.ID))

	return builderBetaWriteJSON(w, http.StatusCreated, meta.ETag(), newBuilderDraftResponse(meta))
}

// getDraft - GET /builder/drafts/{owner}/{draft}.
func (b *builderBetaAPI) getDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGetDraft")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "getting a builder draft")
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbGet, "getting a builder draft")
	if err != nil {
		return err
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderBetaWebError(err, "unable to get the current document of builder draft %s", meta.ID)
	}

	response := newBuilderDraftResponse(meta)
	response.ReadOnly = builderDraftReadOnly(actor, meta)
	response.Document = snapshot.Data
	response.History = builderSnapshotHistory(meta)

	return builderBetaWriteJSON(w, http.StatusOK, meta.ETag(), response)
}

func builderDraftReadOnly(actor builderBetaActor, meta *bapi.DraftMetadata) bool {
	if !builderBetaBaseAllowed(actor.role, builderBetaVerbUpdate) {
		return true
	}

	return meta.Owner != actor.user &&
		!builderBetaCrossUserAllowed(
			actor.role,
			builderBetaVerbUpdate,
			builderBetaDraftName(meta.Owner, meta.ID),
		)
}

// deleteDraft - DELETE /builder/drafts/{owner}/{draft}.
func (b *builderBetaAPI) deleteDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaDeleteDraft")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "deleting a builder draft")
	}

	ifMatch, err := builderBetaIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbDelete, "deleting a builder draft")
	if err != nil {
		return err
	}

	if err := builderBetaCheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	err = b.drafts.DeleteDraft(r.Context(), meta.ID, actor.user, meta.Revision)

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrCleanup):
		// The draft record is gone; only removing its content failed.
		builderBetaWarnCleanup(w, err, "delete draft", actor.user)
	default:
		return builderBetaWebError(err, "unable to delete builder draft %s", meta.ID)
	}

	plog.Info(plog.TypeAction, "deleted builder draft", "user", actor.user, "draft", meta.ID)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// listSnapshots - GET /builder/drafts/{owner}/{draft}/snapshots.
func (b *builderBetaAPI) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaListSnapshots")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "listing builder draft snapshots")
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbGet, "listing builder draft snapshots")
	if err != nil {
		return err
	}

	return builderBetaWriteJSON(w, http.StatusOK, meta.ETag(), map[string]any{
		"snapshots": builderSnapshotHistory(meta),
		"cursor":    meta.Cursor,
	})
}

// getSnapshot - GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot}.
//
// The snapshot "current" names whichever snapshot the cursor points at.
func (b *builderBetaAPI) getSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGetSnapshot")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "getting a builder draft snapshot")
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbGet, "getting a builder draft snapshot")
	if err != nil {
		return err
	}

	requested := mux.Vars(r)["snapshot"]
	manifest := meta.Snapshot(requested)

	if requested == builderBetaCurrentSnapshot {
		manifest = meta.Current()
	}

	if manifest == nil {
		return builderBetaNotFound("snapshot", requested)
	}

	snapshot, err := b.drafts.GetSnapshot(r.Context(), meta.ID, manifest.ID)
	if err != nil {
		return builderBetaWebError(err, "unable to get snapshot %s of draft %s", manifest.ID, meta.ID)
	}

	current := meta.Current()

	return builderBetaWriteJSON(w, http.StatusOK, meta.ETag(), builderSnapshotDocumentResponse{
		Snapshot: newBuilderSnapshotResponse(snapshot.Manifest, current != nil && current.ID == manifest.ID),
		Document: snapshot.Data,
	})
}

// createSnapshot - POST /builder/drafts/{owner}/{draft}/snapshots.
//
// Appending a snapshot is how a draft is saved: it discards the redo branch and
// moves the cursor to the new snapshot. It requires the caller's If-Match to
// name the revision the draft is currently at.
func (b *builderBetaAPI) createSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaCreateSnapshot")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "saving a builder draft")
	}

	ifMatch, err := builderBetaIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbUpdate, "saving a builder draft")
	if err != nil {
		return err
	}

	if err := builderBetaCheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	var request builderSnapshotRequest

	if err := builderBetaDecode(w, r, &request); err != nil {
		return err
	}

	document, err := builderBetaDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	updated, err := b.drafts.AppendSnapshot(r.Context(), bapi.AppendSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		Document:         document,
		Summary:          request.Summary,
	})

	updated, err = builderBetaMutation(
		w, updated, err, "append snapshot", actor.user,
		"unable to save builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderBetaWriteJSON(
		w,
		http.StatusCreated,
		updated.ETag(),
		newBuilderDraftResponse(updated),
	)
}

// updateCursor - PATCH /builder/drafts/{owner}/{draft}/cursor.
//
// Moving the cursor is how undo and redo are performed. No snapshot is
// discarded, but it is still a mutation and requires an If-Match.
func (b *builderBetaAPI) updateCursor(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaUpdateCursor")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "moving a builder draft cursor")
	}

	ifMatch, err := builderBetaIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderBetaVerbUpdate, "moving a builder draft cursor")
	if err != nil {
		return err
	}

	if err := builderBetaCheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	var request builderCursorRequest

	if err := builderBetaDecode(w, r, &request); err != nil {
		return err
	}

	if (request.Index == nil) == (request.SnapshotID == "") {
		return weberror.NewWebError(nil, "exactly one of index and snapshotId is required").
			SetStatus(http.StatusBadRequest)
	}

	move := bapi.MoveCursorRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		Index:            0,
		SnapshotID:       request.SnapshotID,
		UseIndex:         request.Index != nil,
	}

	if request.Index != nil {
		move.Index = *request.Index
	}

	updated, err := b.drafts.MoveCursor(r.Context(), move)

	updated, err = builderBetaMutation(
		w, updated, err, "move cursor", actor.user,
		"unable to move the cursor of builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderBetaWriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}

// sharedDrafts returns the drafts of other users the caller is explicitly
// allowed to list. The full listing is skipped entirely when the caller holds
// no cross-user list permission at all.
func (b *builderBetaAPI) sharedDrafts(
	r *http.Request,
	actor builderBetaActor,
) ([]builderDraftResponse, error) {
	shared := []builderDraftResponse{}

	if !builderBetaCrossUserAllowed(actor.role, builderBetaVerbList) {
		return shared, nil
	}

	drafts, err := b.drafts.ListDrafts(r.Context())
	if err != nil {
		return nil, builderBetaWebError(err, "unable to list builder drafts")
	}

	for i := range drafts {
		draft := &drafts[i]

		if draft.Owner == actor.user {
			continue
		}

		name := builderBetaDraftName(draft.Owner, draft.ID)

		if !builderBetaCrossUserAllowed(actor.role, builderBetaVerbList, name) {
			continue
		}

		response := newBuilderDraftResponse(draft)
		response.ReadOnly = builderDraftReadOnly(actor, draft)
		shared = append(shared, response)
	}

	return shared, nil
}
