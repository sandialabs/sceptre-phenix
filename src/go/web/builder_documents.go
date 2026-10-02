package web

import (
	"context"
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

// listDocuments - GET /builder/documents.
//
// Published documents are the immutable content a config's "builder-doc"
// annotation points at, so they are authorized exactly like the config they
// were published to. A topology whose annotation names a Builder file, and no
// stored document that is current, is listed too, with the path: the listing
// reads no file, so it says nothing of what the file holds, or whether it
// can be read.
//
// A stored document's row names the experiment its publication made, while
// one still exists that the caller may get (see [builderDocumentExperiment]).
func (b *builderAPI) listDocuments(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListDocuments")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder documents")
	if err != nil {
		return err
	}

	documents, err := b.drafts.ListPublishedDocuments(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list builder documents")
	}

	allowed := make([]builderDocumentResponse, 0, len(documents))
	references := newBuilderDocumentReferences(b.listConfigs)
	// stored names the configs a listed document is current for.
	stored := map[string]bool{}
	// links are the experiments Builder publications made, listed once, with
	// the other configs, when the first stored document is listed.
	var (
		links  []builderExperimentLink
		linked bool
	)

	for i := range documents {
		document := &documents[i]

		if !builderBaseAllowed(actor.role, builderVerbList, builderConfigName(document)) {
			continue
		}

		current, err := references.current(document)
		if err != nil {
			return builderWebError(err, "unable to verify builder document %s", document.ID)
		}
		if !current {
			continue
		}

		if !linked {
			links, linked = builderListExperimentLinks(references.list), true
		}

		response := newBuilderDocumentResponse(document)
		response.Experiment = builderDocumentExperiment(actor.role, links, document)

		allowed = append(allowed, response)
		stored[builderConfigName(document)] = true
	}

	files, err := references.files()
	if err != nil {
		return builderWebError(err, "unable to list the builder documents of topologies")
	}

	for _, file := range files {
		name := store.ConfigFullName(builderKindTopology, file.topology)

		if stored[name] || !builderBaseAllowed(actor.role, builderVerbList, name) {
			continue
		}

		allowed = append(allowed, newBuilderFileResponse(file.topology, file.path))
	}

	return builderWriteJSON(w, http.StatusOK, "", util.WithRoot("documents", allowed))
}

// getDocument - GET /builder/documents/{document}.
func (b *builderAPI) getDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetDocument")

	actor, err := builderAuthorize(r, builderVerbGet, "getting a builder document")
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		return err
	}

	current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderWebError(err, "unable to verify builder document %s", documentID)
	}
	if !current {
		return builderNotFound("document", documentID)
	}

	// The record is read again with its content, which is checked against the
	// digest the record holds: the digest the topology's reference named.
	document, data, err := b.drafts.GetPublishedDocumentData(r.Context(), document.ID)
	if err != nil {
		return builderWebError(err, "unable to get builder document %s", documentID)
	}

	response := newBuilderDocumentResponse(document)
	response.Document = data

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// getTopologyDocument - GET /builder/topologies/{topology}/document.
//
// It returns the Builder document a topology references, wherever it is
// kept: the stored published document, or else the Builder file its
// reference names (see [builderAPI.topologyDocument]). A file is read on
// every request. A topology the caller may not get, one that does not exist,
// and one that references no document are all answered with the same 404.
func (b *builderAPI) getTopologyDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetTopologyDocument")

	actor, err := builderAuthorize(r, builderVerbGet, "getting the builder document of a topology")
	if err != nil {
		return err
	}

	name := mux.Vars(r)["topology"]

	topology, resolved, err := b.readableTopologyDocument(r.Context(), actor, name)
	if err != nil {
		return err
	}

	var response builderDocumentResponse

	if resolved.record != nil {
		response = newBuilderDocumentResponse(resolved.record)
	} else {
		response = newBuilderFileResponse(name, resolved.path)
		response.Digest = resolved.digest
		response.Size = int64(len(resolved.data))

		// Nothing ties a topology to the file it names: the topology may have
		// been written by hand, or the file changed since.
		holds, err := topologyHoldsProjection(resolved.data, topology)
		if err != nil {
			return err
		}

		response.TopologyDiffers = !holds
	}

	response.Document = resolved.data

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// readableTopologyDocument returns the stored topology name and the Builder
// document it references (see [builderAPI.topologyDocument]), if the
// caller may get that topology. A topology the caller may not get, one that
// does not exist, one with no document reference or an invalid one, and one
// whose reference names nothing here are indistinguishable: each is the same
// 404. A Builder file that cannot be used is answered with why (see
// [builderFileError]).
func (b *builderAPI) readableTopologyDocument(
	ctx context.Context,
	actor builderActor,
	name string,
) (*store.Config, builderTopologyDocument, error) {
	var none builderTopologyDocument

	missing := builderNotFound("builder document of topology", name)

	if name == "" || len(name) > bapi.MaxTargetLength || strings.Contains(name, "/") {
		return nil, none, missing
	}

	full := store.ConfigFullName(builderKindTopology, name)

	if !builderBaseAllowed(actor.role, builderVerbGet, full) {
		plog.Warn(
			plog.TypeSecurity,
			"builder document request not allowed",
			"user",
			actor.user,
			"topology",
			name,
		)

		return nil, none, missing
	}

	topology, err := b.getConfig(full)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, none, missing
		}

		return nil, none, weberror.NewWebError(err, "unable to get topology %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	reference, ok := builderConfigReference(topology, "")
	if !ok {
		return nil, none, missing
	}

	resolved, found, err := b.topologyDocument(ctx, name, reference)

	var fileErr *bapi.DocumentFileError

	switch {
	case errors.As(err, &fileErr):
		return nil, none, builderFileError(fileErr)
	case err != nil:
		return nil, none, builderWebError(err, "unable to read the builder document of topology %s", name)
	case !found:
		return nil, none, missing
	}

	return topology, resolved, nil
}

// builderFileError answers a Builder file that cannot be used: the fixed
// sentence of its reason, which names the path the topology's reference
// already shows and nothing of the file's content, with 404 for a file that
// does not exist, 413 for one that is too large, and 422 otherwise.
func builderFileError(err *bapi.DocumentFileError) *weberror.WebError {
	status := http.StatusUnprocessableEntity

	switch err.Reason {
	case bapi.DocumentFileMissing:
		status = http.StatusNotFound
	case bapi.DocumentFileTooLarge:
		status = http.StatusRequestEntityTooLarge
	case bapi.DocumentFileOutside, bapi.DocumentFileUnreadable, bapi.DocumentFileNotRegular,
		bapi.DocumentFileInvalid, bapi.DocumentFileDigest:
	}

	return weberror.NewWebError(nil, "%s", err.Error()).SetStatus(status)
}

// topologyHoldsProjection reports whether a stored topology's spec is
// exactly what the Builder document data publishes as that topology (see
// [bapi.TopologyHoldsDocument]).
func topologyHoldsProjection(data []byte, topology *store.Config) (bool, error) {
	holds, err := bapi.TopologyHoldsDocument(data, topology)
	if err != nil {
		return false, weberror.NewWebError(err, "unable to digest topology %s", topology.Metadata.Name).
			SetStatus(http.StatusInternalServerError)
	}

	return holds, nil
}

// deleteDocument - DELETE /builder/documents/{document}.
//
// Deleting a published topology deletes the topology config the document is
// current for, as DELETE /configs does, then every published document of that
// topology. Drafts and experiments made from it are not changed. A published
// experiment is not deleted here: the Experiments page stops it first.
func (b *builderAPI) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteDocument")

	actor, err := builderAuthorize(r, builderVerbDelete, "deleting a builder document")
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		return err
	}

	if document.Kind != builderKindTopology {
		return weberror.NewWebError(
			nil,
			"Only published topologies can be deleted here. Delete experiments from the Experiments page.",
		).SetStatus(http.StatusUnprocessableEntity)
	}

	name := builderConfigName(document)
	if !builderBaseAllowed(actor.role, builderVerbDelete, name) {
		return builderForbidden(actor, "deleting config "+name)
	}

	// Publishing holds the same lock, so no publication of the topology runs
	// between the check below and removing its documents.
	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderWebError(err, "unable to verify builder document %s", documentID)
	}

	if !current {
		return builderNotFound("document", documentID)
	}

	if err := b.deleteTopologyConfig(name, document.Target); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return builderNotFound("document", documentID)
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
		builderWarnCleanup(w, err, "delete published topology", actor.user)
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
func (b *builderAPI) deleteTopologyConfig(name, target string) error {
	defer bapi.LeaveTopologyDocuments(target)()

	return b.publish.deleteConfig(name)
}

// readableDocument returns the published document with this ID if the caller
// may get the config it was published to. A document the caller may not read
// is indistinguishable from one that does not exist.
func (b *builderAPI) readableDocument(
	r *http.Request,
	actor builderActor,
	documentID string,
) (*bapi.PublishedDocument, error) {
	document, err := b.drafts.GetPublishedDocument(r.Context(), documentID)
	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, builderNotFound("document", documentID)
		}

		return nil, builderWebError(err, "unable to get builder document %s", documentID)
	}

	if !builderBaseAllowed(actor.role, builderVerbGet, builderConfigName(document)) {
		plog.Warn(
			plog.TypeSecurity,
			"builder document request not allowed",
			"user",
			actor.user,
			"document",
			documentID,
		)

		return nil, builderNotFound("document", documentID)
	}

	return document, nil
}

// currentBuilderDocument verifies that a published record is the document the
// target config currently references (see [bapi.DocumentReference.Names]).
// Superseded or orphaned records may remain after interrupted cleanup, but
// they are never listable or readable.
func (b *builderAPI) currentBuilderDocument(document *bapi.PublishedDocument) (bool, error) {
	configName := builderConfigName(document)
	if configName == "" {
		return false, nil
	}

	config, err := b.getConfig(configName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	reference, ok := builderConfigReference(config, document.ID)

	return ok && reference.Names(document), nil
}

// builderTopologyDocument is the Builder document a topology references, as
// [builderAPI.topologyDocument] resolves it.
type builderTopologyDocument struct {
	// record is the stored published document, and nil for a document read
	// from a file.
	record *bapi.PublishedDocument
	// path is the path of the Builder file the document was read from, and
	// empty for a stored document.
	path string
	// digest is the digest of data, the document's canonical JSON.
	digest string
	data   []byte
}

// topologyDocument resolves the Builder document that reference, read from
// the topology name, names. It reports false when the reference names no
// document here.
//
// The stored published document wins (see
// [builderAPI.storedTopologyDocument]): it is what the stored topology was
// published from, and a file may have moved on since. A stored document that
// cannot be read is an error, never passed over for the file.
//
// Without one, a reference with a path names the Builder file at that path
// on this server, which is read now (see [bapi.ReadDocumentFile]). A digest
// beside the path pins the file: a file whose document has another digest is
// refused, so a reference that names a digest is never answered with other
// content. An ID beside the path says nothing of the file. A file that
// cannot be used is an error, a [bapi.DocumentFileError], and is logged with
// its reason.
func (b *builderAPI) topologyDocument(
	ctx context.Context,
	name string,
	reference bapi.DocumentReference,
) (builderTopologyDocument, bool, error) {
	none := builderTopologyDocument{record: nil, path: "", digest: "", data: nil}

	stored, found, err := b.storedTopologyDocument(ctx, name, reference)
	if err != nil || found {
		return stored, found, err
	}

	if reference.Path == "" {
		return none, false, nil
	}

	root, excluded := b.documentFiles()

	file, err := bapi.ReadDocumentFile(root, excluded, reference.Path)
	if err == nil && reference.Digest != "" && file.Digest != reference.Digest {
		err = &bapi.DocumentFileError{Reason: bapi.DocumentFileDigest, Path: reference.Path, Root: "", Topology: name}
	}

	if err != nil {
		reason := "error"

		var fileErr *bapi.DocumentFileError
		if errors.As(err, &fileErr) {
			reason = string(fileErr.Reason)
		}

		plog.Warn(
			plog.TypeSystem,
			"builder document file not usable",
			"topology", name,
			"path", reference.Path,
			"reason", reason,
		)

		return none, false, err
	}

	return builderTopologyDocument{record: nil, path: reference.Path, digest: file.Digest, data: file.Data}, true, nil
}

// storedTopologyDocument returns the stored published document of the
// topology name that reference names (see [bapi.DocumentReference.Names]),
// with its verified content. It reports false when the reference names no
// stored document here: no such record, another topology's, or one with
// another digest. A stored document that cannot be read is an error,
// matching [bapi.ErrCorrupt] when its record or its content is damaged.
func (b *builderAPI) storedTopologyDocument(
	ctx context.Context,
	name string,
	reference bapi.DocumentReference,
) (builderTopologyDocument, bool, error) {
	none := builderTopologyDocument{record: nil, path: "", digest: "", data: nil}

	id := reference.StoredID(name)
	if id == "" {
		return none, false, nil
	}

	record, err := b.drafts.GetPublishedDocument(ctx, id)

	switch {
	case errors.Is(err, bapi.ErrNotFound):
		return none, false, nil
	case err != nil:
		return none, false, err
	case builderConfigName(record) != store.ConfigFullName(builderKindTopology, name) || !reference.Names(record):
		return none, false, nil
	}

	// The content is read with the record again, which may have been stored
	// afresh since: its ID still says it has this target and this digest.
	record, data, err := b.drafts.GetPublishedDocumentData(ctx, id)

	switch {
	case errors.Is(err, bapi.ErrNotFound):
		return none, false, nil
	case err != nil:
		return none, false, err
	}

	return builderTopologyDocument{record: record, path: "", digest: record.Digest, data: data}, true, nil
}

// builderConfigReference returns the document reference a config's
// "builder-doc" annotation holds, reporting whether it holds a valid one. An
// invalid reference is logged, naming the document it was read for.
func builderConfigReference(config *store.Config, documentID string) (bapi.DocumentReference, bool) {
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
// current, as [builderAPI.currentBuilderDocument] does for one document,
// and which topologies name a Builder file. It lists each kind of config
// once, when it is first needed, and decodes each config's reference once,
// so documents left behind by deleted or republished topologies cost the
// listing no config read each.
type builderDocumentReferences struct {
	listConfigs func(kind string) (store.Configs, error)
	// configs holds the configs of the kinds listed so far, by full name, and
	// kinds the same configs by kind, as they were listed.
	configs map[string]store.Config
	kinds   map[string]store.Configs
	// references holds the reference of each config looked up so far, or nil
	// when it has none (or the config does not exist).
	references map[string]*bapi.DocumentReference
}

// builderFileReference is a topology whose document reference names a
// Builder file.
type builderFileReference struct {
	topology string
	path     string
}

func newBuilderDocumentReferences(listConfigs func(kind string) (store.Configs, error)) *builderDocumentReferences {
	return &builderDocumentReferences{
		listConfigs: listConfigs,
		configs:     map[string]store.Config{},
		kinds:       map[string]store.Configs{},
		references:  map[string]*bapi.DocumentReference{},
	}
}

// list returns the configs of a kind, listing them the first time.
func (r *builderDocumentReferences) list(kind string) (store.Configs, error) {
	if configs, listed := r.kinds[kind]; listed {
		return configs, nil
	}

	configs, err := r.listConfigs(kind)
	if err != nil {
		return nil, err
	}

	for _, config := range configs {
		r.configs[config.FullName()] = config
	}

	r.kinds[kind] = configs

	return configs, nil
}

// reference returns the document reference the config named configName
// holds, or nil when it holds no valid one or does not exist. An invalid
// reference is logged once, naming the document it was first read for.
func (r *builderDocumentReferences) reference(configName, documentID string) (*bapi.DocumentReference, error) {
	if reference, looked := r.references[configName]; looked {
		return reference, nil
	}

	kind, _, _ := strings.Cut(configName, "/")

	if _, err := r.list(kind); err != nil {
		return nil, err
	}

	var reference *bapi.DocumentReference

	if config, ok := r.configs[configName]; ok {
		if decoded, ok := builderConfigReference(&config, documentID); ok {
			reference = &decoded
		}
	}

	r.references[configName] = reference

	return reference, nil
}

// current reports whether document is the one the config it was published to
// references now.
func (r *builderDocumentReferences) current(document *bapi.PublishedDocument) (bool, error) {
	configName := builderConfigName(document)
	if configName == "" {
		return false, nil
	}

	reference, err := r.reference(configName, document.ID)
	if err != nil {
		return false, err
	}

	return reference != nil && reference.Names(document), nil
}

// files returns the topologies whose document reference names a Builder
// file, in the order the topologies are listed. No file is read.
func (r *builderDocumentReferences) files() ([]builderFileReference, error) {
	topologies, err := r.list(builderKindTopology)
	if err != nil {
		return nil, err
	}

	var files []builderFileReference

	for _, topology := range topologies {
		reference, err := r.reference(topology.FullName(), "")
		if err != nil {
			return nil, err
		}

		if reference != nil && reference.Path != "" {
			files = append(files, builderFileReference{topology: topology.Metadata.Name, path: reference.Path})
		}
	}

	return files, nil
}

// Where the Builder document a config references is kept (see
// [builderDocumentResponse]).
const (
	builderDocumentSourceStore = "store"
	builderDocumentSourceFile  = "file"
)

// builderDocumentResponse is the JSON view of the Builder document a config
// references: a stored published document (source "store"), or the Builder
// file a topology names (source "file"). A file has no ID, author or time.
// Its row in a listing holds only where it is, since a listing reads no
// file; its digest, size and content, and whether the stored topology
// differs from what it publishes, are in the answer of the request that
// reads it. Chunk digests are storage details and are not exposed.
type builderDocumentResponse struct {
	Source     string    `json:"source"`
	ID         string    `json:"id,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Size       int64     `json:"size,omitempty"`
	Target     string    `json:"target"`
	Kind       string    `json:"kind"`
	Config     string    `json:"config"`
	Path       string    `json:"path,omitempty"`
	DraftID    string    `json:"draftId,omitempty"`
	SnapshotID string    `json:"snapshotId,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitzero"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	// TopologyDiffers is set for a file whose document does not publish as
	// the stored topology's spec.
	TopologyDiffers bool            `json:"topologyDiffers,omitempty"`
	Document        json.RawMessage `json:"document,omitempty"`
	// Experiment is the name of an experiment the publication behind a
	// stored document made, on the rows of a listing only. It is worked out
	// for each response and never stored (see [builderDocumentExperiment]).
	Experiment string `json:"experiment,omitempty"`
}

// builderConfigName returns the canonical "Kind/name" of the config a
// document was published to, which is the resource name it is authorized
// against. An unknown kind yields an empty name, which no policy matches.
func builderConfigName(document *bapi.PublishedDocument) string {
	return store.ConfigFullName(document.Kind, document.Target)
}

// newBuilderDocumentResponse converts a published document into its JSON view.
func newBuilderDocumentResponse(document *bapi.PublishedDocument) builderDocumentResponse {
	return builderDocumentResponse{ //nolint:exhaustruct // document bytes are added by the single document endpoints
		Source:     builderDocumentSourceStore,
		ID:         document.ID,
		Digest:     document.Digest,
		Size:       document.Size,
		Target:     document.Target,
		Kind:       document.Kind,
		Config:     builderConfigName(document),
		DraftID:    document.DraftID,
		SnapshotID: document.SnapshotID,
		CreatedAt:  document.CreatedAt,
		CreatedBy:  document.CreatedBy,
	}
}

// newBuilderFileResponse is the JSON view of the Builder file at path that
// the topology named topology references.
func newBuilderFileResponse(topology, path string) builderDocumentResponse {
	return builderDocumentResponse{ //nolint:exhaustruct // what the file holds is added by the endpoint that reads it
		Source: builderDocumentSourceFile,
		Target: topology,
		Kind:   builderKindTopology,
		Config: store.ConfigFullName(builderKindTopology, topology),
		Path:   path,
	}
}
