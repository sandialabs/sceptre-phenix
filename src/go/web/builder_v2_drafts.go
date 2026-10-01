package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderDraftRequest creates a draft. The owner is always the authenticated
// user: it may be sent for symmetry with the response, but never to create a
// draft on somebody else's behalf. ForkOf, "<owner>/<draft id>", names a
// draft the new one forks, whose source and last publication it takes in
// place of SourceToken (see [builderV2API.forkOrigin]).
type builderDraftRequest struct {
	Title       string          `json:"title"`
	SourceToken string          `json:"sourceToken"`
	ForkOf      string          `json:"forkOf"`
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
	// Forked is what the draft this one forks had published when it was
	// forked, which this draft may update too.
	Forked *bapi.ForkedPublication `json:"forked,omitempty"`

	// Access is what the caller may do with the draft: "owner", "edit" or
	// "view". Via is where the caller's access to another user's draft comes
	// from: "share" or "role". Both are reported only by the listing, GET of
	// one draft, and the draft a PUT of its shares returns (see
	// [builderDraftAccessFields]).
	Access string `json:"access,omitempty"`
	Via    string `json:"via,omitempty"`
	// CanShare reports whether the caller, its owner, may change who the
	// draft is shared with, and Shares whom it is shared with. Only the owner
	// is told either.
	CanShare bool                `json:"canShare,omitempty"`
	Shares   []builderDraftShare `json:"shares,omitempty"`
}

// builderDraftShare is one user a draft is shared with, as its owner sees it
// in a draft response.
type builderDraftShare struct {
	User   string `json:"user"`
	Access string `json:"access"`
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

	if meta.Forked != nil {
		forked := *meta.Forked
		response.Forked = &forked
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
		OpID:      manifest.OpID,
		Current:   current,
	}
}

// builderDraftAccessFields fills in what the caller may do with a draft: its
// access, where access to another user's draft comes from, and whether the
// draft is read only for the caller; and, for its owner only, whether the
// caller may share it and whom it is shared with, stale shares included.
func builderDraftAccessFields(
	response *builderDraftResponse,
	actor builderV2Actor,
	access builderV2Access,
	meta *bapi.DraftMetadata,
	canShare bool,
) {
	response.Access = access.name()
	response.ReadOnly = builderDraftReadOnly(actor, access)

	if !access.owner() {
		response.Via = access.via

		return
	}

	response.CanShare = canShare

	if meta.Sharing == nil || len(meta.Sharing.Entries) == 0 {
		return
	}

	response.Shares = make([]builderDraftShare, 0, len(meta.Sharing.Entries))

	for _, entry := range meta.Sharing.Entries {
		response.Shares = append(response.Shares, builderDraftShare{User: entry.User, Access: string(entry.Access)})
	}
}

// listDrafts - GET /builder-v2/drafts.
//
// The response separates the caller's own drafts from the drafts of other users
// shared with the caller or the caller is explicitly allowed to list, and
// lists apart the drafts whose metadata this server can no longer read, which
// can only be deleted: the caller's own, and those of other users the caller
// may list, never because of a share. Drafts the caller may not see are never
// counted, described, or otherwise hinted at.
func (b *builderV2API) listDrafts(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2ListDrafts")

	actor, err := builderV2Authorize(r, builderV2VerbList, "listing builder drafts")
	if err != nil {
		return err
	}

	drafts, damaged, err := b.drafts.ListDraftsWithDamaged(r.Context())
	if err != nil {
		return builderV2WebError(err, "unable to list builder drafts")
	}

	mine, shared, err := b.partitionDrafts(actor, drafts)
	if err != nil {
		return weberror.NewWebError(err, "unable to list builder drafts").
			SetStatus(http.StatusInternalServerError)
	}

	return builderV2WriteJSON(w, http.StatusOK, "", map[string]any{
		"drafts":  mine,
		"shared":  shared,
		"damaged": builderDamagedDrafts(actor, damaged),
	})
}

// partitionDrafts returns the responses of the caller's own drafts, and of the
// drafts of other users shared with the caller or its role lets it list. The
// caller's account is read at most once, and only when a draft needs it.
func (b *builderV2API) partitionDrafts(
	actor builderV2Actor,
	drafts []bapi.DraftMetadata,
) ([]builderDraftResponse, []builderDraftResponse, error) {
	var (
		mine    = []builderDraftResponse{}
		shared  = []builderDraftResponse{}
		account = b.accountOnce(actor.user)
		roles   = builderV2HoldsRoleGrants(actor.role)
	)

	for i := range drafts {
		draft := &drafts[i]

		var grants builderV2RoleGrants
		if roles && draft.Owner != actor.user {
			grants = builderV2RoleGrantsFor(actor.role, builderV2DraftName(draft.Owner, draft.ID))
		}

		access, err := builderV2DraftAccess(actor, draft, grants, account)
		if err != nil {
			return nil, nil, err
		}

		switch {
		case access.owner():
			canShare, err := b.canShare(actor, account)
			if err != nil {
				return nil, nil, err
			}

			response := newBuilderDraftResponse(draft)
			builderDraftAccessFields(&response, actor, access, draft, canShare)
			mine = append(mine, response)
		case access.via == builderV2ViaShare || access.rbac.canList:
			response := newBuilderDraftResponse(draft)
			builderDraftAccessFields(&response, actor, access, draft, false)
			shared = append(shared, response)
		}
	}

	return mine, shared, nil
}

// builderDamagedDraftResponse is the JSON view of a draft whose metadata this
// server can no longer read: what could still be read of it, and whether the
// caller may delete it, which is all that can be done with it.
type builderDamagedDraftResponse struct {
	ID        string     `json:"id"`
	Owner     string     `json:"owner"`
	Title     string     `json:"title,omitempty"`
	Updated   *time.Time `json:"updated,omitempty"`
	ETag      string     `json:"etag"`
	CanDelete bool       `json:"canDelete"`
}

// builderDamagedDrafts returns the damaged drafts the caller may see, as
// [builderV2API.listDrafts] lists readable ones: the caller's own, and those
// of other users the caller may list. A draft whose owner cannot be read is
// left out: no request can name it.
func builderDamagedDrafts(actor builderV2Actor, damaged []bapi.DamagedDraft) []builderDamagedDraftResponse {
	responses := []builderDamagedDraftResponse{}

	for i := range damaged {
		draft := &damaged[i]

		if draft.Owner == "" || !bapi.ValidID(draft.ID) {
			continue
		}

		own := draft.Owner == actor.user

		var grants builderV2RoleGrants
		if !own {
			grants = builderV2RoleGrantsFor(actor.role, builderV2DraftName(draft.Owner, draft.ID))
		}

		if !own && !grants.canList {
			continue
		}

		response := builderDamagedDraftResponse{
			ID:        draft.ID,
			Owner:     draft.Owner,
			Title:     draft.Title,
			Updated:   nil,
			ETag:      draft.ETag(),
			CanDelete: builderV2BaseAllowed(actor.role, builderV2VerbDelete) && (own || grants.canDelete),
		}

		if !draft.Updated.IsZero() {
			response.Updated = &draft.Updated
		}

		responses = append(responses, response)
	}

	return responses
}

// createDraft - POST /builder-v2/drafts.
func (b *builderV2API) createDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2CreateDraft")

	actor, err := builderV2Authorize(r, builderV2VerbCreate, "creating a builder draft")
	if err != nil {
		return err
	}

	var request builderDraftRequest

	if err := builderV2Decode(w, r, &request); err != nil {
		return err
	}

	// Drafts are always owned by the authenticated user. Creating one for
	// somebody else is not a permission that exists.
	if request.Owner != "" && request.Owner != actor.user {
		return builderV2Forbidden(actor, "creating a builder draft for "+request.Owner)
	}

	document, err := builderV2DocumentBytes(request.Document)
	if err != nil {
		return err
	}

	sourceToken := request.SourceToken

	var forked *bapi.ForkedPublication

	if request.ForkOf != "" {
		if sourceToken, forked, err = b.forkOrigin(r, actor, request.ForkOf); err != nil {
			return err
		}
	} else if id, opened := strings.CutPrefix(sourceToken, builderDocTokenPrefix); opened {
		// A draft opened from a published document may update the config it
		// was published to (see [draftOwnsDocument]), so only a caller who
		// may read that document may name it.
		if _, err = b.readableDocument(r, actor, id); err != nil {
			return err
		}
	}

	meta, err := b.drafts.CreateDraft(r.Context(), bapi.CreateDraftRequest{
		Owner:       actor.user,
		Actor:       actor.user,
		Title:       request.Title,
		SourceToken: sourceToken,
		Forked:      forked,
		Document:    document,
		Summary:     request.Summary,
		ID:          "",
	})

	meta, err = builderV2Mutation(
		w, meta, err, "create draft", actor.user, "unable to create the builder draft",
	)
	if err != nil {
		return err
	}

	plog.Info(plog.TypeAction, "created builder draft", "user", actor.user, "draft", meta.ID)

	w.Header().Set("Location", "/api/v1/builder-v2/drafts/"+builderV2DraftName(meta.Owner, meta.ID))

	return builderV2WriteJSON(w, http.StatusCreated, meta.ETag(), newBuilderDraftResponse(meta))
}

// getDraft - GET /builder-v2/drafts/{owner}/{draft}.
func (b *builderV2API) getDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2GetDraft")

	actor, meta, access, err := b.readDraft(r, "getting a builder draft")
	if err != nil {
		return err
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderV2WebError(err, "unable to get the current document of builder draft %s", meta.ID)
	}

	canShare := false

	if access.owner() {
		if canShare, err = b.canShare(actor, b.accountOnce(actor.user)); err != nil {
			return weberror.NewWebError(err, "unable to get builder draft %s", meta.ID).
				SetStatus(http.StatusInternalServerError)
		}
	} else {
		plog.Info(
			plog.TypeAction,
			"opened shared builder draft",
			"user", actor.user,
			"owner", meta.Owner,
			"draft", meta.ID,
			"access", access.name(),
			"via", access.via,
		)
	}

	response := newBuilderDraftResponse(meta)
	builderDraftAccessFields(&response, actor, access, meta, canShare)
	response.Document = snapshot.Data
	response.History = builderSnapshotHistory(meta)

	return builderV2WriteJSON(w, http.StatusOK, meta.ETag(), response)
}

// builderDraftReadOnly reports whether the caller may not change a draft it
// may see: its access does not allow updates, or its role cannot update
// configs, even when a share gives it edit access.
func builderDraftReadOnly(actor builderV2Actor, access builderV2Access) bool {
	return !access.allows(builderV2VerbUpdate) || !builderV2BaseAllowed(actor.role, builderV2VerbUpdate)
}

// deleteDraft - DELETE /builder-v2/drafts/{owner}/{draft}.
func (b *builderV2API) deleteDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2DeleteDraft")

	actor, ok := builderV2RequestActor(r)
	if !ok {
		return builderV2Forbidden(actor, "deleting a builder draft")
	}

	ifMatch, err := builderV2IfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderV2VerbDelete, "deleting a builder draft")
	if err != nil {
		return err
	}

	if err := builderV2CheckIfMatch(ifMatch, meta); err != nil {
		// The caller may delete the draft, so it may know its current ETag,
		// which it has no other way to read when the draft's metadata no
		// longer validates: it is neither listed nor readable then.
		w.Header().Set("ETag", meta.ETag())

		return err
	}

	err = b.drafts.DeleteDraft(r.Context(), meta.ID, actor.user, meta.Revision)

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrCleanup):
		// The draft record is gone; only removing its content failed.
		builderV2WarnCleanup(w, err, "delete draft", actor.user)
	default:
		return builderV2WebError(err, "unable to delete builder draft %s", meta.ID)
	}

	plog.Info(plog.TypeAction, "deleted builder draft", "user", actor.user, "draft", meta.ID)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// listSnapshots - GET /builder-v2/drafts/{owner}/{draft}/snapshots.
func (b *builderV2API) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2ListSnapshots")

	_, meta, _, err := b.readDraft(r, "listing builder draft snapshots")
	if err != nil {
		return err
	}

	return builderV2WriteJSON(w, http.StatusOK, meta.ETag(), map[string]any{
		"snapshots": builderSnapshotHistory(meta),
		"cursor":    meta.Cursor,
	})
}

// getSnapshot - GET /builder-v2/drafts/{owner}/{draft}/snapshots/{snapshot}.
//
// The snapshot "current" names whichever snapshot the cursor points at.
func (b *builderV2API) getSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2GetSnapshot")

	_, meta, _, err := b.readDraft(r, "getting a builder draft snapshot")
	if err != nil {
		return err
	}

	requested := mux.Vars(r)["snapshot"]
	manifest := meta.Snapshot(requested)

	if requested == builderV2CurrentSnapshot {
		manifest = meta.Current()
	}

	if manifest == nil {
		return builderV2NotFound("snapshot", requested)
	}

	snapshot, err := b.drafts.GetSnapshot(r.Context(), meta.ID, manifest.ID)
	if err != nil {
		return builderV2WebError(err, "unable to get snapshot %s of draft %s", manifest.ID, meta.ID)
	}

	current := meta.Current()

	return builderV2WriteJSON(w, http.StatusOK, meta.ETag(), builderSnapshotDocumentResponse{
		Snapshot: newBuilderSnapshotResponse(snapshot.Manifest, current != nil && current.ID == manifest.ID),
		Document: snapshot.Data,
	})
}

// deleteSnapshot - DELETE /builder-v2/drafts/{owner}/{draft}/snapshots/{snapshot}.
//
// Removes a version from the draft's history, as the owner or anyone who may
// save the draft. The current version cannot be removed; the cursor keeps
// pointing at it. It requires the caller's If-Match to name the revision the
// draft is currently at.
func (b *builderV2API) deleteSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2DeleteSnapshot")

	const action = "deleting a builder draft snapshot"

	actor, ok := builderV2RequestActor(r)
	if !ok {
		return builderV2Forbidden(actor, action)
	}

	ifMatch, err := builderV2IfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderV2VerbUpdate, action)
	if err != nil {
		return err
	}

	if err := builderV2CheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	requested := mux.Vars(r)["snapshot"]
	manifest := meta.Snapshot(requested)

	if requested == builderV2CurrentSnapshot {
		manifest = meta.Current()
	}

	switch {
	case manifest == nil:
		return builderV2NotFound("snapshot", requested)
	case manifest.ID == meta.Current().ID:
		return weberror.NewWebError(nil, "The current version cannot be deleted.").
			SetStatus(http.StatusConflict)
	}

	updated, err := b.drafts.DeleteSnapshot(r.Context(), bapi.DeleteSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		SnapshotID:       manifest.ID,
	})

	updated, err = builderV2Mutation(
		w, updated, err, "delete snapshot", actor.user,
		"unable to delete snapshot %s of builder draft %s", manifest.ID, meta.ID,
	)
	if err != nil {
		return err
	}

	plog.Info(
		plog.TypeAction, "deleted builder draft snapshot",
		"user", actor.user, "draft", meta.ID, "snapshot", manifest.ID,
	)

	return builderV2WriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}

// createSnapshot - POST /builder-v2/drafts/{owner}/{draft}/snapshots.
//
// Appending a snapshot is how a draft is saved: it discards the redo branch and
// moves the cursor to the new snapshot. It requires the caller's If-Match to
// name the revision the draft is currently at.
func (b *builderV2API) createSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2CreateSnapshot")

	actor, ok := builderV2RequestActor(r)
	if !ok {
		return builderV2Forbidden(actor, "saving a builder draft")
	}

	ifMatch, err := builderV2IfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderV2VerbUpdate, "saving a builder draft")
	if err != nil {
		return err
	}

	if err := builderV2CheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	var request builderSnapshotRequest

	if err := builderV2Decode(w, r, &request); err != nil {
		return err
	}

	document, err := builderV2DocumentBytes(request.Document)
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

	updated, err = builderV2Mutation(
		w, updated, err, "append snapshot", actor.user,
		"unable to save builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderV2WriteJSON(
		w,
		http.StatusCreated,
		updated.ETag(),
		newBuilderDraftResponse(updated),
	)
}

// updateCursor - PATCH /builder-v2/drafts/{owner}/{draft}/cursor.
//
// Moving the cursor is how undo and redo are performed. No snapshot is
// discarded, but it is still a mutation and requires an If-Match.
func (b *builderV2API) updateCursor(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2UpdateCursor")

	actor, ok := builderV2RequestActor(r)
	if !ok {
		return builderV2Forbidden(actor, "moving a builder draft cursor")
	}

	ifMatch, err := builderV2IfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderV2VerbUpdate, "moving a builder draft cursor")
	if err != nil {
		return err
	}

	if err := builderV2CheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	var request builderCursorRequest

	if err := builderV2Decode(w, r, &request); err != nil {
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

	updated, err = builderV2Mutation(
		w, updated, err, "move cursor", actor.user,
		"unable to move the cursor of builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderV2WriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}
