package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderDraftRequest creates a draft. The owner is always the authenticated
// user. A client may send the owner for symmetry with the response, but
// never to create a draft for another user. ForkOf, "<owner>/<draft id>",
// names a draft that the new draft forks. The new draft takes the source and
// last publication of that draft in place of SourceToken (see
// [builderAPI.forkOrigin]). SourceFile is the name of the uploaded file that
// the document came from. It is kept only for display. A fork records none.
type builderDraftRequest struct {
	Title       string          `json:"title"`
	SourceToken string          `json:"sourceToken"`
	SourceFile  string          `json:"sourceFile"`
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
	ID          string `json:"id"`
	Owner       string `json:"owner"`
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
	ReadOnly     bool                      `json:"readOnly"`
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
	// CanDelete reports whether the caller may delete the draft, which a
	// share never allows. It is reported where Access is.
	CanDelete bool `json:"canDelete,omitempty"`
	// Experiment is the name of an experiment that the publication of the draft
	// made, while that experiment exists and the caller may get it. It is
	// calculated for each response and never stored (see
	// [builderAPI.draftExperiment]). GET of one draft, a draft create and the
	// draft of a publish answer report it. The listing never reports it, because
	// the listing would have to list the experiments for it.
	Experiment string `json:"experiment,omitempty"`
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

// builderDraftAccessFields sets what the caller may do with a draft:
//
//   - its access
//   - where access to the draft of another user comes from
//   - whether the draft is read only for the caller
//   - whether the caller may delete it, as DELETE of the draft decides
//   - for its owner only: whether the caller may share it, and whom it is
//     shared with, stale shares included
func builderDraftAccessFields(
	response *builderDraftResponse,
	actor builderActor,
	access builderAccess,
	meta *bapi.DraftMetadata,
	canShare bool,
) {
	response.Access = access.name()
	response.ReadOnly = builderDraftReadOnly(actor, access)
	response.CanDelete = builderBaseAllowed(actor.role, builderVerbDelete) && access.allows(builderVerbDelete)

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

// listDrafts - GET /builder/drafts.
//
// The response separates the drafts of the caller from the drafts of other
// users. Those other drafts are shared with the caller, or the caller has
// explicit permission to list them. The response lists separately the drafts
// whose metadata this server can no longer read. These drafts can only be
// deleted. They are the drafts of the caller, and those of other users that
// the caller may list, never because of a share. The response never counts,
// describes, or hints at drafts that the caller may not see.
func (b *builderAPI) listDrafts(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListDrafts")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder drafts")
	if err != nil {
		return err
	}

	drafts, damaged, err := b.drafts.ListDraftsWithDamaged(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list builder drafts")
	}

	mine, shared, err := b.partitionDrafts(actor, drafts)
	if err != nil {
		return weberror.NewWebError(err, "unable to list builder drafts").
			SetStatus(http.StatusInternalServerError)
	}

	return builderWriteJSON(w, http.StatusOK, "", map[string]any{
		"drafts":  mine,
		"shared":  shared,
		"damaged": builderDamagedDrafts(actor, damaged),
	})
}

// partitionDrafts returns the responses of the drafts of the caller. It also
// returns those of the drafts of other users that are shared with the caller
// or that its role lets it list. It reads the account of the caller at most
// once, and only when a draft needs it.
func (b *builderAPI) partitionDrafts(
	actor builderActor,
	drafts []bapi.DraftMetadata,
) ([]builderDraftResponse, []builderDraftResponse, error) {
	var (
		mine    = []builderDraftResponse{}
		shared  = []builderDraftResponse{}
		account = b.accountOnce(actor.user)
		roles   = builderHoldsRoleGrants(actor.role)
	)

	for i := range drafts {
		draft := &drafts[i]

		var grants builderRoleGrants
		if roles && draft.Owner != actor.user {
			grants = builderRoleGrantsFor(actor.role, builderDraftName(draft.Owner, draft.ID))
		}

		access, err := builderDraftAccess(actor, draft, grants, account)
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
		case access.via == builderViaShare || access.rbac.canList:
			response := newBuilderDraftResponse(draft)
			builderDraftAccessFields(&response, actor, access, draft, false)
			shared = append(shared, response)
		}
	}

	return mine, shared, nil
}

// builderDamagedDraftResponse is the JSON view of a draft whose metadata this
// server can no longer read. It holds what could still be read of the draft,
// and whether the caller may delete it. Delete is the only operation left
// for such a draft.
type builderDamagedDraftResponse struct {
	ID        string     `json:"id"`
	Owner     string     `json:"owner"`
	Title     string     `json:"title,omitempty"`
	Updated   *time.Time `json:"updated,omitempty"`
	ETag      string     `json:"etag"`
	CanDelete bool       `json:"canDelete"`
}

// builderDamagedDrafts returns the damaged drafts that the caller may see,
// as [builderAPI.listDrafts] lists readable ones. These are the drafts of the
// caller, and those of other users that the caller may list. A draft whose
// owner cannot be read is left out, because no request can name it.
func builderDamagedDrafts(actor builderActor, damaged []bapi.DamagedDraft) []builderDamagedDraftResponse {
	responses := []builderDamagedDraftResponse{}

	for i := range damaged {
		draft := &damaged[i]

		if draft.Owner == "" || !bapi.ValidID(draft.ID) {
			continue
		}

		own := draft.Owner == actor.user

		var grants builderRoleGrants
		if !own {
			grants = builderRoleGrantsFor(actor.role, builderDraftName(draft.Owner, draft.ID))
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
			CanDelete: builderBaseAllowed(actor.role, builderVerbDelete) && (own || grants.canDelete),
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

	actor, err := builderAuthorize(r, builderVerbCreate, "creating a builder draft")
	if err != nil {
		return err
	}

	var request builderDraftRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	// Drafts are always owned by the authenticated user. Creating one for
	// somebody else is not a permission that exists.
	if request.Owner != "" && request.Owner != actor.user {
		return builderForbidden(actor, "creating a builder draft for "+request.Owner)
	}

	document, err := builderDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	// Check the name here too, so a name that is not valid also refuses a
	// fork. Otherwise the fork would drop it without a message.
	if err := bapi.ValidateSourceFile(request.SourceFile); err != nil {
		return builderWebError(err, "unable to create the builder draft")
	}

	source, err := b.draftSource(r, actor, request)
	if err != nil {
		return err
	}

	meta, err := b.drafts.CreateDraft(r.Context(), bapi.CreateDraftRequest{
		Owner:       actor.user,
		Actor:       actor.user,
		Title:       request.Title,
		SourceToken: source.token,
		SourceFile:  source.file,
		Forked:      source.forked,
		Origin:      source.origin,
		Document:    document,
		Summary:     request.Summary,
		ID:          "",
	})

	meta, err = builderMutation(
		w, meta, err, "create draft", actor.user, "unable to create the builder draft",
	)
	if err != nil {
		return err
	}

	plog.Info(plog.TypeAction, "created builder draft", "user", actor.user, "draft", meta.ID)

	w.Header().Set("Location", "/api/v1/builder/drafts/"+builderDraftName(meta.Owner, meta.ID))

	return builderWriteJSON(w, http.StatusCreated, meta.ETag(), b.draftResponse(actor, meta))
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
//
// A fork takes the source token and the last publication of the draft it
// forks (see [builderAPI.forkOrigin]) and records no source file. Its
// document is what the editor of the caller holds, so it is stamped as any
// request body is.
//
// A draft opened from a published document, or from the Builder file that a
// topology names, may update that topology (see [draftOwnsDocument] and
// [builderAPI.holdsDraftDocument]). Thus only a caller who may read the
// document may name it. For a file, this applies only while the file still
// holds what the caller opened. That document is the origin of the draft.
// When the request sends it back unchanged, the draft stores it as it is
// (see [bapi.Service.CreateDraft]), so the open of a diagram is not an edit
// of it. Only its digest tells this, and the server supplies the digest:
// that of the record, or that of the file as read for this request.
func (b *builderAPI) draftSource(
	r *http.Request,
	actor builderActor,
	request builderDraftRequest,
) (builderDraftSource, error) {
	source := builderDraftSource{token: request.SourceToken, file: request.SourceFile, forked: nil, origin: nil}

	if request.ForkOf != "" {
		token, forked, err := b.forkOrigin(r, actor, request.ForkOf)
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

// getDraft - GET /builder/drafts/{owner}/{draft}.
func (b *builderAPI) getDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetDraft")

	actor, meta, access, err := b.readDraft(r, "getting a builder draft")
	if err != nil {
		return err
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderWebError(err, "unable to get the current document of builder draft %s", meta.ID)
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

	response := b.draftResponse(actor, meta)
	builderDraftAccessFields(&response, actor, access, meta, canShare)
	response.Document = snapshot.Data
	response.History = builderSnapshotHistory(meta)

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), response)
}

// builderDraftReadOnly reports whether the caller may not change a draft
// that it may see. This is true when its access does not allow updates. It
// is also true when its role cannot update configs, even if a share gives
// it edit access.
func builderDraftReadOnly(actor builderActor, access builderAccess) bool {
	return !access.allows(builderVerbUpdate) || !builderBaseAllowed(actor.role, builderVerbUpdate)
}

// deleteDraft - DELETE /builder/drafts/{owner}/{draft}.
func (b *builderAPI) deleteDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteDraft")

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, "deleting a builder draft")
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderVerbDelete, "deleting a builder draft")
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(ifMatch, meta); err != nil {
		// The caller may delete the draft, so it may know its current ETag. When
		// the metadata of the draft no longer validates, the caller has no other
		// way to read the ETag, because the draft is neither listed nor readable.
		w.Header().Set("ETag", meta.ETag())

		return err
	}

	err = b.drafts.DeleteDraft(r.Context(), meta.ID, actor.user, meta.Revision)

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrCleanup):
		// The draft record is gone. Only the removal of its content failed.
		builderWarnCleanup(w, err, "delete draft", actor.user)
	default:
		return builderWebError(err, "unable to delete builder draft %s", meta.ID)
	}

	plog.Info(plog.TypeAction, "deleted builder draft", "user", actor.user, "draft", meta.ID)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// listSnapshots - GET /builder/drafts/{owner}/{draft}/snapshots.
func (b *builderAPI) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListSnapshots")

	_, meta, _, err := b.readDraft(r, "listing builder draft snapshots")
	if err != nil {
		return err
	}

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), map[string]any{
		"snapshots": builderSnapshotHistory(meta),
		"cursor":    meta.Cursor,
	})
}

// getSnapshot - GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot}.
//
// The snapshot "current" names whichever snapshot the cursor points at.
func (b *builderAPI) getSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetSnapshot")

	_, meta, _, err := b.readDraft(r, "getting a builder draft snapshot")
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

// deleteSnapshot - DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}.
//
// Removes a version from the history of the draft. The owner, or any caller
// who may save the draft, may do this. The current version cannot be
// removed. The cursor stays on it. The If-Match of the caller must name the
// current revision of the draft.
func (b *builderAPI) deleteSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteSnapshot")

	const action = "deleting a builder draft snapshot"

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, action)
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderVerbUpdate, action)
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(ifMatch, meta); err != nil {
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
		return weberror.NewWebError(nil, "The current version cannot be deleted.").
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodeDraftSnapshotCurrent))
	}

	updated, err := b.drafts.DeleteSnapshot(r.Context(), bapi.DeleteSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		SnapshotID:       manifest.ID,
	})

	updated, err = builderMutation(
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

	return builderWriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}

// createSnapshot - POST /builder/drafts/{owner}/{draft}/snapshots.
//
// Appending a snapshot is how a draft is saved: it discards the redo branch and
// moves the cursor to the new snapshot. It requires the caller's If-Match to
// name the revision the draft is currently at.
func (b *builderAPI) createSnapshot(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateSnapshot")

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, "saving a builder draft")
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderVerbUpdate, "saving a builder draft")
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(ifMatch, meta); err != nil {
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

	updated, err = builderMutation(
		w, updated, err, "append snapshot", actor.user,
		"unable to save builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderWriteJSON(
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
func (b *builderAPI) updateCursor(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderUpdateCursor")

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, "moving a builder draft cursor")
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	meta, err := b.draftFor(r, actor, builderVerbUpdate, "moving a builder draft cursor")
	if err != nil {
		return err
	}

	if err := builderCheckIfMatch(ifMatch, meta); err != nil {
		return err
	}

	var request builderCursorRequest

	if err := builderDecode(w, r, &request); err != nil {
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

	updated, err = builderMutation(
		w, updated, err, "move cursor", actor.user,
		"unable to move the cursor of builder draft %s", meta.ID,
	)
	if err != nil {
		return err
	}

	return builderWriteJSON(w, http.StatusOK, updated.ETag(), newBuilderDraftResponse(updated))
}
