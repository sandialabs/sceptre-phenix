package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/util/plog"
	"phenix/web/util"
)

// listDocuments - GET /builder/documents.
//
// Published documents are the immutable content a config's "builder-doc"
// annotation points at, so they are authorized exactly like the config they
// were published to.
func (b *builderBetaAPI) listDocuments(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaListDocuments")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "listing builder documents")
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbList) {
		return builderBetaForbidden(actor, "listing builder documents")
	}

	documents, err := b.drafts.ListPublishedDocuments(r.Context())
	if err != nil {
		return builderBetaWebError(err, "unable to list builder documents")
	}

	allowed := make([]builderDocumentResponse, 0, len(documents))

	for i := range documents {
		document := &documents[i]

		if !builderBetaBaseAllowed(actor.role, builderBetaVerbList, builderBetaConfigName(document)) {
			continue
		}

		_, current, err := b.currentBuilderDocument(document)
		if err != nil {
			return builderBetaWebError(err, "unable to verify builder document %s", document.ID)
		}
		if !current {
			continue
		}

		allowed = append(allowed, newBuilderDocumentResponse(document))
	}

	return builderBetaWriteJSON(w, http.StatusOK, "", util.WithRoot("documents", allowed))
}

// getDocument - GET /builder/documents/{document}.
func (b *builderBetaAPI) getDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGetDocument")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "getting a builder document")
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbGet) {
		return builderBetaForbidden(actor, "getting a builder document")
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.drafts.GetPublishedDocument(r.Context(), documentID)
	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return builderBetaNotFound("document", documentID)
		}

		return builderBetaWebError(err, "unable to get builder document %s", documentID)
	}

	// A document the caller may not read is indistinguishable from one that
	// does not exist.
	if !builderBetaBaseAllowed(actor.role, builderBetaVerbGet, builderBetaConfigName(document)) {
		plog.Warn(
			plog.TypeSecurity,
			"builder beta document request not allowed",
			"user",
			actor.user,
			"document",
			documentID,
		)

		return builderBetaNotFound("document", documentID)
	}

	reference, current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderBetaWebError(err, "unable to verify builder document %s", documentID)
	}
	if !current {
		return builderBetaNotFound("document", documentID)
	}

	data, err := b.drafts.VerifyPublishedDocument(r.Context(), reference)
	if err != nil {
		return builderBetaWebError(err, "unable to get builder document %s", documentID)
	}

	response := newBuilderDocumentResponse(document)
	response.Document = data

	return builderBetaWriteJSON(w, http.StatusOK, "", response)
}

// currentBuilderDocument verifies that a published record is the document the
// target config currently references. Superseded or orphaned records may remain
// after interrupted cleanup, but they are never listable or readable.
func (b *builderBetaAPI) currentBuilderDocument(
	document *bapi.PublishedDocument,
) (bapi.DocumentReference, bool, error) {
	configName := builderBetaConfigName(document)
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

	value, ok := config.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return bapi.DocumentReference{}, false, nil
	}

	reference, err := bapi.DecodeReference(value)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"published builder config has an invalid document reference",
			"config", configName,
			"document", document.ID,
			"err", err,
		)

		return bapi.DocumentReference{}, false, nil
	}

	return reference, reference == document.Reference(), nil
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

// builderBetaConfigName returns the canonical "Kind/name" of the config a
// document was published to, which is the resource name it is authorized
// against. An unknown kind yields an empty name, which no policy matches.
func builderBetaConfigName(document *bapi.PublishedDocument) string {
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
		Config:     builderBetaConfigName(document),
		DraftID:    document.DraftID,
		SnapshotID: document.SnapshotID,
		CreatedAt:  document.CreatedAt,
		CreatedBy:  document.CreatedBy,
	}
}
