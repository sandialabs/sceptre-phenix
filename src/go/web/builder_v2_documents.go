package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/util/plog"
	"phenix/web/util"
	"phenix/web/weberror"
)

// listDocuments - GET /builder-v2/documents.
//
// Published documents are the immutable content a config's "builder-doc"
// annotation points at, so they are authorized exactly like the config they
// were published to.
func (b *builderV2API) listDocuments(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2ListDocuments")

	actor, err := builderV2Authorize(r, builderV2VerbList, "listing builder documents")
	if err != nil {
		return err
	}

	documents, err := b.drafts.ListPublishedDocuments(r.Context())
	if err != nil {
		return builderV2WebError(err, "unable to list builder documents")
	}

	allowed := make([]builderDocumentResponse, 0, len(documents))
	references := newBuilderDocumentReferences(b.listConfigs)

	for i := range documents {
		document := &documents[i]

		if !builderV2BaseAllowed(actor.role, builderV2VerbList, builderV2ConfigName(document)) {
			continue
		}

		current, err := references.current(document)
		if err != nil {
			return builderV2WebError(err, "unable to verify builder document %s", document.ID)
		}
		if !current {
			continue
		}

		allowed = append(allowed, newBuilderDocumentResponse(document))
	}

	return builderV2WriteJSON(w, http.StatusOK, "", util.WithRoot("documents", allowed))
}

// getDocument - GET /builder-v2/documents/{document}.
func (b *builderV2API) getDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2GetDocument")

	actor, err := builderV2Authorize(r, builderV2VerbGet, "getting a builder document")
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		return err
	}

	reference, current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderV2WebError(err, "unable to verify builder document %s", documentID)
	}
	if !current {
		return builderV2NotFound("document", documentID)
	}

	data, err := b.drafts.VerifyPublishedDocument(r.Context(), reference)
	if err != nil {
		return builderV2WebError(err, "unable to get builder document %s", documentID)
	}

	response := newBuilderDocumentResponse(document)
	response.Document = data

	return builderV2WriteJSON(w, http.StatusOK, "", response)
}

// deleteDocument - DELETE /builder-v2/documents/{document}.
//
// Deleting a published topology deletes the topology config the document is
// current for, as DELETE /configs does, then every published document of that
// topology. Drafts and experiments made from it are not changed. A published
// experiment is not deleted here: the Experiments page stops it first.
func (b *builderV2API) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2DeleteDocument")

	actor, err := builderV2Authorize(r, builderV2VerbDelete, "deleting a builder document")
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		return err
	}

	if document.Kind != builderV2KindTopology {
		return weberror.NewWebError(
			nil,
			"Only published topologies can be deleted here. Delete experiments from the Experiments page.",
		).SetStatus(http.StatusUnprocessableEntity)
	}

	name := builderV2ConfigName(document)
	if !builderV2BaseAllowed(actor.role, builderV2VerbDelete, name) {
		return builderV2Forbidden(actor, "deleting config "+name)
	}

	// Publishing holds the same lock, so no publication of the topology runs
	// between the check below and removing its documents.
	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	_, current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderV2WebError(err, "unable to verify builder document %s", documentID)
	}

	if !current {
		return builderV2NotFound("document", documentID)
	}

	if err := b.deleteTopologyConfig(name, document.Target); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return builderV2NotFound("document", documentID)
		}

		return weberror.NewWebError(err, "unable to delete config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	// The hook would keep the documents a publication in flight may have just
	// stored (see bapi.Service.DeleteConfigDocuments). None is in flight in
	// this process while the lock is held, so every document goes. The
	// topology is gone, so its documents are never listed again (see
	// currentBuilderDocument), and the startup cleanup removes any left here.
	if _, err := b.drafts.DeleteTargetDocuments(r.Context(), document.Target); err != nil {
		builderV2WarnCleanup(w, err, "delete published topology", actor.user)
	}

	plog.Info(
		plog.TypeAction,
		"deleted published builder topology",
		"user", actor.user,
		"config", name,
		"document", documentID,
	)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// deleteTopologyConfig deletes the config of the published topology target,
// named name. The Topology config hook, which removes a deleted topology's
// documents and only logs a failure, leaves them to the caller, which reports
// one.
func (b *builderV2API) deleteTopologyConfig(name, target string) error {
	defer bapi.LeaveTopologyDocuments(target)()

	return b.publish.deleteConfig(name)
}

// readableDocument returns the published document with this ID if the caller
// may get the config it was published to. A document the caller may not read
// is indistinguishable from one that does not exist.
func (b *builderV2API) readableDocument(
	r *http.Request,
	actor builderV2Actor,
	documentID string,
) (*bapi.PublishedDocument, error) {
	document, err := b.drafts.GetPublishedDocument(r.Context(), documentID)
	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, builderV2NotFound("document", documentID)
		}

		return nil, builderV2WebError(err, "unable to get builder document %s", documentID)
	}

	if !builderV2BaseAllowed(actor.role, builderV2VerbGet, builderV2ConfigName(document)) {
		plog.Warn(
			plog.TypeSecurity,
			"builder v2 document request not allowed",
			"user",
			actor.user,
			"document",
			documentID,
		)

		return nil, builderV2NotFound("document", documentID)
	}

	return document, nil
}

// currentBuilderDocument verifies that a published record is the document the
// target config currently references. Superseded or orphaned records may remain
// after interrupted cleanup, but they are never listable or readable.
func (b *builderV2API) currentBuilderDocument(
	document *bapi.PublishedDocument,
) (bapi.DocumentReference, bool, error) {
	configName := builderV2ConfigName(document)
	if configName == "" {
		return bapi.DocumentReference{}, false, nil
	}

	config, err := b.getConfig(configName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return bapi.DocumentReference{}, false, nil
		}

		return bapi.DocumentReference{}, false, err
	}

	reference, ok := builderV2ConfigReference(config, document.ID)
	if !ok {
		return bapi.DocumentReference{}, false, nil
	}

	return reference, reference == document.Reference(), nil
}

// builderV2ConfigReference returns the document reference a config's
// "builder-doc" annotation holds, reporting whether it holds a valid one. An
// invalid reference is logged, naming the document it was read for.
func builderV2ConfigReference(config *store.Config, documentID string) (bapi.DocumentReference, bool) {
	value, ok := config.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return bapi.DocumentReference{}, false
	}

	reference, err := bapi.DecodeReference(value)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"published builder config has an invalid document reference",
			"config", config.FullName(),
			"document", documentID,
			"err", err,
		)

		return bapi.DocumentReference{}, false
	}

	return reference, true
}

// builderDocumentReferences tells a listing which published documents are
// current, as [builderV2API.currentBuilderDocument] does for one document. It
// lists each kind of config once, when a document first needs it, and decodes
// each config's reference once, so documents left behind by deleted or
// republished topologies cost the listing no config read each.
type builderDocumentReferences struct {
	listConfigs func(kind string) (store.Configs, error)
	// configs holds the configs of the kinds listed so far, by full name.
	configs map[string]store.Config
	listed  map[string]bool
	// references holds the reference of each config looked up so far, or nil
	// when it has none (or the config does not exist).
	references map[string]*bapi.DocumentReference
}

func newBuilderDocumentReferences(listConfigs func(kind string) (store.Configs, error)) *builderDocumentReferences {
	return &builderDocumentReferences{
		listConfigs: listConfigs,
		configs:     map[string]store.Config{},
		listed:      map[string]bool{},
		references:  map[string]*bapi.DocumentReference{},
	}
}

// current reports whether document is the one the config it was published to
// references now.
func (r *builderDocumentReferences) current(document *bapi.PublishedDocument) (bool, error) {
	configName := builderV2ConfigName(document)
	if configName == "" {
		return false, nil
	}

	reference, looked := r.references[configName]
	if !looked {
		kind, _, _ := strings.Cut(configName, "/")

		if !r.listed[kind] {
			configs, err := r.listConfigs(kind)
			if err != nil {
				return false, err
			}

			for _, config := range configs {
				r.configs[config.FullName()] = config
			}

			r.listed[kind] = true
		}

		if config, ok := r.configs[configName]; ok {
			if decoded, ok := builderV2ConfigReference(&config, document.ID); ok {
				reference = &decoded
			}
		}

		r.references[configName] = reference
	}

	return reference != nil && *reference == document.Reference(), nil
}

// builderDocumentResponse is the JSON view of a published document. Chunk
// digests are storage details and are not exposed.
type builderDocumentResponse struct {
	ID         string          `json:"id"`
	Digest     string          `json:"digest"`
	Size       int64           `json:"size"`
	Target     string          `json:"target"`
	Kind       string          `json:"kind"`
	Config     string          `json:"config"`
	DraftID    string          `json:"draftId,omitempty"`
	SnapshotID string          `json:"snapshotId,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	CreatedBy  string          `json:"createdBy"`
	Document   json.RawMessage `json:"document,omitempty"`
}

// builderV2ConfigName returns the canonical "Kind/name" of the config a
// document was published to, which is the resource name it is authorized
// against. An unknown kind yields an empty name, which no policy matches.
func builderV2ConfigName(document *bapi.PublishedDocument) string {
	return store.ConfigFullName(document.Kind, document.Target)
}

// newBuilderDocumentResponse converts a published document into its JSON view.
func newBuilderDocumentResponse(document *bapi.PublishedDocument) builderDocumentResponse {
	return builderDocumentResponse{ //nolint:exhaustruct // document bytes are added by the single document endpoint
		ID:         document.ID,
		Digest:     document.Digest,
		Size:       document.Size,
		Target:     document.Target,
		Kind:       document.Kind,
		Config:     builderV2ConfigName(document),
		DraftID:    document.DraftID,
		SnapshotID: document.SnapshotID,
		CreatedAt:  document.CreatedAt,
		CreatedBy:  document.CreatedBy,
	}
}
