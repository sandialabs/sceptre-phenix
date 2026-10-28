package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderDraftRequest creates a draft for the authenticated user. ForkOf names a draft to fork (see [builderAPI.forkOrigin]).
// SourceFile is only for display. A fork records none.
type builderDraftRequest struct {
	Title       string          `json:"title"`
	SourceToken string          `json:"sourceToken"`
	SourceFile  string          `json:"sourceFile"`
	ForkOf      string          `json:"forkOf"`
	Summary     string          `json:"summary"`
	Document    json.RawMessage `json:"document"`
}

func builderSnapshotHistory(meta *bapi.DraftMetadata) []builderSnapshotResponse {
	history := make([]builderSnapshotResponse, 0, len(meta.History))

	for i := range meta.History {
		history = append(history, newBuilderSnapshotResponse(meta.History[i], i == meta.Cursor))
	}

	return history
}

// builderSnapshotRequest appends a snapshot to a draft. OpID is the
// client's id for the save, recorded with the snapshot so a client that
// never saw the response can tell it was stored.
type builderSnapshotRequest struct {
	Summary  string          `json:"summary"`
	Document json.RawMessage `json:"document"`
	OpID     string          `json:"opId"`
}

// builderCursorRequest moves a draft's cursor. Exactly one of Index and
// SnapshotID must be set.
type builderCursorRequest struct {
	Index      *int   `json:"index"`
	SnapshotID string `json:"snapshotId"`
}

// builderDraftResponse is the JSON view of a draft. Storage details are
// deliberately not exposed.
type builderDraftResponse struct {
	ID string `json:"id"`
	// CreatedBy is the user who created the draft, for provenance only.
	CreatedBy   string `json:"createdBy"`
	Title       string `json:"title,omitempty"`
	SourceToken string `json:"sourceToken,omitempty"`
	// SourceFile is the name of the uploaded file the draft was made from,
	// when it was made from one.
	SourceFile     string    `json:"sourceFile,omitempty"`
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
	ETag         string                    `json:"etag"`
	Document     json.RawMessage           `json:"document,omitempty"`
	History      []builderSnapshotResponse `json:"history,omitempty"`
	// Stamp is the creator, creation time, last editor and last edit time of
	// the document that the request stored, as its metadata names them. Only a
	// draft create and a draft save store a document, and neither returns it.
	// The editor copies the stamp into its own copy instead. A field that the
	// stored document does not have is left out.
	Stamp *bdoc.Provenance `json:"stamp,omitempty"`

	Publication *builderPublicationResponse `json:"publication,omitempty"`
	// Forked is what the draft this one forks had published when it was
	// forked, which this draft may update too.
	Forked *bapi.ForkedPublication `json:"forked,omitempty"`

	// Experiment names the experiment that the draft's publication made, if it exists and the caller may get it.
	// It is calculated for each response, never stored. The listing never reports it, because it would list experiments.
	Experiment string `json:"experiment,omitempty"`
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
	OpID      string    `json:"opId,omitempty"`
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
		CreatedBy:      meta.CreatedBy,
		Title:          meta.Title,
		SourceToken:    meta.SourceToken,
		SourceFile:     meta.SourceFile,
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
		// Set only on the metadata a create or a save returns.
		Stamp: meta.Stamp,
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

	if meta.Forked != nil {
		forked := *meta.Forked
		response.Forked = &forked
	}

	return response
}

// draftResponse is [newBuilderDraftResponse] for a response that describes
// one draft to the caller: it also names the experiment the draft's
// publication made (see [builderAPI.draftExperiment]).
func (b *builderAPI) draftResponse(actor builderActor, meta *bapi.DraftMetadata) builderDraftResponse {
	response := newBuilderDraftResponse(meta)
	response.Experiment = b.draftExperiment(actor, meta)

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
		OpID:      manifest.OpID,
		Current:   current,
	}
}

// builderDraftLimits is the most drafts the server holds, damaged drafts
// included.
type builderDraftLimits struct {
	Drafts int `json:"drafts"`
}

// listDrafts - GET /builder/drafts.
// It lists drafts newest first, the drafts with unreadable metadata apart, and the draft limit.
func (b *builderAPI) listDrafts(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListDrafts")

	if _, err := builderAuthorize(r, "listing Builder drafts"); err != nil {
		return err
	}

	drafts, damaged, err := b.drafts.ListDraftsWithDamaged(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list Builder drafts")
	}

	slices.SortStableFunc(drafts, func(a, b bapi.DraftMetadata) int {
		return b.Updated.Compare(a.Updated)
	})

	responses := make([]builderDraftResponse, 0, len(drafts))
	for i := range drafts {
		responses = append(responses, newBuilderDraftResponse(&drafts[i]))
	}

	return builderWriteJSON(w, http.StatusOK, "", map[string]any{
		"drafts":  responses,
		"damaged": builderDamagedDrafts(damaged),
		"limits":  builderDraftLimits{Drafts: bapi.MaxDrafts},
	})
}

// builderDamagedDraftResponse is the JSON view of a draft whose metadata this
// server can no longer read. It holds what could still be read of the draft.
// Delete is the only operation left for such a draft.
type builderDamagedDraftResponse struct {
	ID        string     `json:"id"`
	CreatedBy string     `json:"createdBy,omitempty"`
	Title     string     `json:"title,omitempty"`
	Updated   *time.Time `json:"updated,omitempty"`
	ETag      string     `json:"etag"`
}

// builderDamagedDrafts returns the damaged drafts as [builderAPI.listDrafts]
// lists them. A draft whose ID no request can name is left out.
func builderDamagedDrafts(damaged []bapi.DamagedDraft) []builderDamagedDraftResponse {
	responses := []builderDamagedDraftResponse{}

	for i := range damaged {
		draft := &damaged[i]

		if !bapi.ValidID(draft.ID) {
			continue
		}

		response := builderDamagedDraftResponse{
			ID:        draft.ID,
			CreatedBy: draft.CreatedBy,
			Title:     draft.Title,
			Updated:   nil,
			ETag:      draft.ETag(),
		}

		if !draft.Updated.IsZero() {
			response.Updated = &draft.Updated
		}

		responses = append(responses, response)
	}

	return responses
}

// createDraft - POST /builder/drafts.
func (b *builderAPI) createDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateDraft")

	actor, err := builderAuthorize(r, "creating a Builder draft")
	if err != nil {
		return err
	}

	var request builderDraftRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	document, err := builderDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	// Check the name here too, so a name that is not valid also refuses a
	// fork. Otherwise the fork would drop it without a message.
	if err := bapi.ValidateSourceFile(request.SourceFile); err != nil {
		return builderWebError(err, "unable to create the Builder draft")
	}

	source, err := b.draftSource(r, actor, request)
	if err != nil {
		return err
	}

	meta, err := b.drafts.CreateDraft(r.Context(), bapi.CreateDraftRequest{
		Actor:       actor.user,
		Title:       request.Title,
		SourceToken: source.token,
		SourceFile:  source.file,
		Forked:      source.forked,
		Origin:      source.origin,
		Document:    document,
		Summary:     request.Summary,
	})

	if err != nil {
		return builderDraftCreateError(err)
	}

	plog.Info(plog.TypeAction, "created builder draft", "user", actor.user, "draft", meta.ID)

	w.Header().Set("Location", "/api/v1/builder/drafts/"+meta.ID)

	return builderWriteJSON(w, http.StatusCreated, meta.ETag(), b.draftResponse(actor, meta))
}

// builderDraftCreateError maps a failed draft create to its answer. A server
// that holds [bapi.MaxDrafts] drafts gets 413 and the code draft.limit.
func builderDraftCreateError(err error) error {
	var full *bapi.DraftLimitError

	if errors.As(err, &full) {
		return weberror.NewWebError(
			nil, "the server holds %d drafts, the most it can hold. Delete drafts, or run Clean up in the Builder settings",
			full.Limit,
		).SetStatus(http.StatusRequestEntityTooLarge).WithCode(string(bdoc.CodeDraftLimit))
	}

	return builderWebError(err, "unable to create the Builder draft")
}

// builderDraftSource is where a new draft comes from, as the server
// established it from the request (see [builderAPI.draftSource]).
type builderDraftSource struct {
	// token and file are the source token and the source file name the
	// draft records.
	token string
	file  string
	// forked is what the draft it forks had published.
	forked *bapi.ForkedPublication
	// origin is the document the draft is opened from, when the server read
	// it itself for this request.
	origin *bapi.DocumentOrigin
}

// draftSource establishes where the draft a request creates comes from.
// A draft opened from a published document or file may update that topology, so only a caller who may read it may name it.
// The server supplies the origin digest, so an unchanged open is stored as is and is not an edit.
func (b *builderAPI) draftSource(
	r *http.Request,
	actor builderActor,
	request builderDraftRequest,
) (builderDraftSource, error) {
	source := builderDraftSource{token: request.SourceToken, file: request.SourceFile, forked: nil, origin: nil}

	if request.ForkOf != "" {
		token, forked, err := b.forkOrigin(r, request.ForkOf)
		if err != nil {
			return source, err
		}

		source.token, source.file, source.forked = token, "", forked

		return source, nil
	}

	if id, opened := strings.CutPrefix(source.token, builderDocTokenPrefix); opened {
		document, err := b.readableDocument(r, actor, id)
		if err != nil {
			return source, err
		}

		source.origin = &bapi.DocumentOrigin{Digest: document.Digest}
	} else if strings.HasPrefix(source.token, builderFileTokenPrefix) {
		// The token says what the file held: the file is read again here, so
		// the token never names content the caller was not given.
		file, err := b.openedTopologyFile(r.Context(), actor, source.token)
		if err != nil {
			return source, err
		}

		source.origin = &bapi.DocumentOrigin{Digest: file.digest}
	}

	return source, nil
}

// getDraft - GET /builder/drafts/{draft}.
func (b *builderAPI) getDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetDraft")

	actor, meta, err := b.draftFor(r, "getting a Builder draft")
	if err != nil {
		return err
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderWebError(err, "unable to get the current document of Builder draft %s", meta.ID)
	}

	response := b.draftResponse(actor, meta)
	response.Document = snapshot.Data
	response.History = builderSnapshotHistory(meta)

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), response)
}

// deleteDraft - DELETE /builder/drafts/{draft}.
func (b *builderAPI) deleteDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteDraft")

	actor, err := builderAuthorize(r, "deleting a Builder draft")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	// A publication holds this lock from its read of the draft until it marks
	// the draft published. Thus a delete waits for it, and then sees the new ETag.
	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	meta, err := b.pathDraft(r, true)
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(w, ifMatch, meta); err != nil {
		return err
	}

	if err := b.drafts.DeleteDraft(r.Context(), meta.ID, actor.user, meta.Revision); err != nil {
		return builderWebError(err, "unable to delete Builder draft %s", meta.ID)
	}

	plog.Info(plog.TypeAction, "deleted builder draft", "user", actor.user, "draft", meta.ID)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// listSnapshots - GET /builder/drafts/{draft}/snapshots.
func (b *builderAPI) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListSnapshots")

	_, meta, err := b.draftFor(r, "listing Builder draft snapshots")
	if err != nil {
		return err
	}

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), map[string]any{
		"snapshots": builderSnapshotHistory(meta),
		"cursor":    meta.Cursor,
	})
}

// getSnapshot - GET /builder/drafts/{draft}/snapshots/{snapshot}.
//
// The snapshot "current" names whichever snapshot the cursor points at.
func (b *builderAPI) getSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetSnapshot")

	_, meta, err := b.draftFor(r, "getting a Builder draft snapshot")
	if err != nil {
		return err
	}

	requested := mux.Vars(r)["snapshot"]
	manifest := meta.Snapshot(requested)

	if requested == builderCurrentSnapshot {
		manifest = meta.Current()
	}

	if manifest == nil {
		return builderNotFound("snapshot", requested)
	}

	snapshot, err := b.drafts.GetSnapshot(r.Context(), meta.ID, manifest.ID)
	if err != nil {
		return builderWebError(err, "unable to get snapshot %s of draft %s", manifest.ID, meta.ID)
	}

	current := meta.Current()

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), builderSnapshotDocumentResponse{
		Snapshot: newBuilderSnapshotResponse(snapshot.Manifest, current != nil && current.ID == manifest.ID),
		Document: snapshot.Data,
	})
}

// deleteSnapshot - DELETE /builder/drafts/{draft}/snapshots/{snapshot}.
//
// Removes a version from the history of the draft. The current version
// cannot be removed. The cursor stays on it. The If-Match of the caller must name the
// current revision of the draft.
func (b *builderAPI) deleteSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteSnapshot")

	actor, err := builderAuthorize(r, "deleting a Builder draft snapshot")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.pathDraft(r, false)
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(w, ifMatch, meta); err != nil {
		return err
	}

	requested := mux.Vars(r)["snapshot"]
	manifest := meta.Snapshot(requested)

	if requested == builderCurrentSnapshot {
		manifest = meta.Current()
	}

	switch {
	case manifest == nil:
		return builderNotFound("snapshot", requested)
	case manifest.ID == meta.Current().ID:
		return weberror.NewWebError(nil, "You cannot delete the current version.").
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodeDraftSnapshotCurrent))
	}

	updated, err := b.drafts.DeleteSnapshot(r.Context(), bapi.DeleteSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		SnapshotID:       manifest.ID,
	})

	if err != nil {
		return builderWebError(err, "unable to delete snapshot %s of Builder draft %s", manifest.ID, meta.ID)
	}

	plog.Info(
		plog.TypeAction, "deleted builder draft snapshot",
		"user", actor.user, "draft", meta.ID, "snapshot", manifest.ID,
	)

	return builderWriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}

// createSnapshot - POST /builder/drafts/{draft}/snapshots.
//
// Appending a snapshot is how a draft is saved: it discards the redo branch and
// moves the cursor to the new snapshot. It requires the caller's If-Match to
// name the revision the draft is currently at.
func (b *builderAPI) createSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateSnapshot")

	actor, err := builderAuthorize(r, "saving a Builder draft")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.pathDraft(r, false)
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(w, ifMatch, meta); err != nil {
		return err
	}

	var request builderSnapshotRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	document, err := builderDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	updated, err := b.drafts.AppendSnapshot(r.Context(), bapi.AppendSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		Document:         document,
		Summary:          request.Summary,
		OpID:             request.OpID,
	})

	if err != nil {
		return builderWebError(err, "unable to save Builder draft %s", meta.ID)
	}

	return builderWriteJSON(
		w,
		http.StatusCreated,
		updated.ETag(),
		newBuilderDraftResponse(updated),
	)
}

// updateCursor - PATCH /builder/drafts/{draft}/cursor.
//
// Moving the cursor is how undo and redo are performed. No snapshot is
// discarded, but it is still a mutation and requires an If-Match.
func (b *builderAPI) updateCursor(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderUpdateCursor")

	actor, err := builderAuthorize(r, "moving a Builder draft cursor")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.pathDraft(r, false)
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(w, ifMatch, meta); err != nil {
		return err
	}

	var request builderCursorRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	if (request.Index == nil) == (request.SnapshotID == "") {
		return weberror.NewWebError(nil, "the request needs exactly one of index and snapshotId").
			SetStatus(http.StatusBadRequest)
	}

	move := bapi.MoveCursorRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		Index:            0,
		SnapshotID:       request.SnapshotID,
	}

	if request.Index != nil {
		move.Index = *request.Index
	}

	updated, err := b.drafts.MoveCursor(r.Context(), move)

	if err != nil {
		return builderWebError(err, "unable to move the cursor of Builder draft %s", meta.ID)
	}

	return builderWriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}
