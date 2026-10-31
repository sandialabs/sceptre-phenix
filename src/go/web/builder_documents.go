package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// Published documents get the authorization of the config they were published to. The listing reads no file.
func (b *builderAPI) listDocuments(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListDocuments")

	actor, err := builderAuthorizeConfig(r, builderVerbList, "listing Builder documents")
	if err != nil {
		return err
	}

	documents, err := b.drafts.ListPublishedDocuments(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list Builder documents")
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

		if !builderConfigAllowed(actor.role, builderVerbList, builderConfigName(document)) {
			continue
		}

		current, err := references.current(document)
		if err != nil {
			return builderWebError(err, "unable to verify Builder document %s", document.ID)
		}
		if !current {
			continue
		}

		if !linked {
			links, linked = builderListExperimentLinks(references.list), true
		}

		response := newBuilderDocumentResponse(document)
		response.Experiment = builderDocumentExperiment(actor.role, links, document)

		config := references.configs[builderConfigName(document)]
		if response.ETag, err = builderDocumentETag(document, &config); err != nil {
			return err
		}

		allowed = append(allowed, response)
		stored[builderConfigName(document)] = true
	}

	files, err := references.files()
	if err != nil {
		return builderWebError(err, "unable to list the Builder documents of topologies")
	}

	for _, file := range files {
		name := store.ConfigFullName(builderKindTopology, file.topology)

		if stored[name] || !builderConfigAllowed(actor.role, builderVerbList, name) {
			continue
		}

		allowed = append(allowed, newBuilderFileResponse(file.topology, file.path))
	}

	return builderWriteJSON(w, http.StatusOK, "", util.WithRoot("documents", allowed))
}

// getDocument - GET /builder/documents/{document}.
func (b *builderAPI) getDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetDocument")

	actor, err := builderAuthorizeConfig(r, builderVerbGet, "getting a Builder document")
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		return err
	}

	config, current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderWebError(err, "unable to verify Builder document %s", documentID)
	}
	if !current {
		return builderNotFound("document", documentID)
	}

	// The record is read again with its content, which is checked against the
	// digest the record holds: the digest the topology's reference named.
	document, data, err := b.drafts.GetPublishedDocumentData(r.Context(), document.ID)
	if err != nil {
		return builderWebError(err, "unable to get Builder document %s", documentID)
	}

	response := newBuilderDocumentResponse(document)
	response.Document = data

	if response.ETag, err = builderDocumentETag(document, config); err != nil {
		return err
	}

	return builderWriteJSON(w, http.StatusOK, response.ETag, response)
}

// getTopologyDocument - GET /builder/topologies/{topology}/document.
// A topology the caller may not get, one that does not exist, and one without a document all get the same 404.
func (b *builderAPI) getTopologyDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetTopologyDocument")

	actor, err := builderAuthorizeConfig(r, builderVerbGet, "getting the Builder document of a topology")
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

		if response.ETag, err = builderDocumentETag(resolved.record, topology); err != nil {
			return err
		}
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

// readableTopologyDocument returns the stored topology name and its Builder document, if the caller may get that topology.
// Every failure to find it gets the same 404, so the client cannot tell them apart.
func (b *builderAPI) readableTopologyDocument(
	ctx context.Context,
	actor builderActor,
	name string,
) (*store.Config, builderTopologyDocument, error) {
	var none builderTopologyDocument

	missing := builderNotFound("Builder document of topology", name)

	if name == "" || len(name) > bapi.MaxTargetLength || strings.Contains(name, "/") {
		return nil, none, missing
	}

	full := store.ConfigFullName(builderKindTopology, name)

	if !builderConfigAllowed(actor.role, builderVerbGet, full) {
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
		return nil, none, builderWebError(err, "unable to read the Builder document of topology %s", name)
	case !found:
		return nil, none, missing
	}

	return topology, resolved, nil
}

// builderFileError answers a Builder file that cannot be used with the fixed
// sentence of its reason. The sentence names the path that the topology
// reference already shows, and nothing of the file content. The status is
// 404 for a file that does not exist, 413 for a file that is too large, and
// 422 otherwise.
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
		return false, weberror.NewWebError(err, "unable to calculate the digest of topology %s", topology.Metadata.Name).
			SetStatus(http.StatusInternalServerError)
	}

	return holds, nil
}

// builderEveryConfig is the resource name that only a permission on every
// config, such as "*/*", matches.
const builderEveryConfig = "*/*"

// deleteDocument - DELETE /builder/documents/{document}.
// It deletes the current topology config, as DELETE /configs does, then every published document of that topology.
// If-Match must name the listed entity tag. A document with unreadable metadata is deleted alone.
func (b *builderAPI) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteDocument")

	actor, err := builderAuthorizeConfig(r, builderVerbDelete, "deleting a Builder document")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	documentID := mux.Vars(r)["document"]

	document, err := b.readableDocument(r, actor, documentID)
	if err != nil {
		if damaged, damagedErr := b.drafts.GetDamagedDocument(r.Context(), documentID); damagedErr == nil {
			return b.deleteDamagedDocument(w, r, actor, ifMatch, damaged)
		}

		return err
	}

	if document.Kind != builderKindTopology {
		return weberror.NewWebError(
			nil,
			"You can delete only published topologies here. Delete experiments from the Experiments page.",
		).SetStatus(http.StatusUnprocessableEntity)
	}

	name := builderConfigName(document)
	if !builderConfigAllowed(actor.role, builderVerbDelete, name) {
		return builderForbidden(actor, "deleting config "+name)
	}

	// Publishing holds the same lock, so no publication of the topology runs
	// between the check below and removing its documents.
	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	config, current, err := b.currentBuilderDocument(document)
	if err != nil {
		return builderWebError(err, "unable to verify Builder document %s", documentID)
	}

	if !current {
		return builderNotFound("document", documentID)
	}

	etag, err := builderDocumentETag(document, config)
	if err != nil {
		return err
	}

	if etag != ifMatch {
		w.Header().Set("ETag", etag)

		return weberror.NewWebError(nil, "topology %s changed after you last read it", document.Target).
			SetStatus(http.StatusPreconditionFailed)
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
	// currentBuilderDocument), and the next cleanup removes any left here.
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

// deleteDamagedDocument deletes a published document with unreadable metadata, and its content. It changes no config.
// The caller needs get and delete on the named config, or on every config when the record names none.
func (b *builderAPI) deleteDamagedDocument(
	w http.ResponseWriter,
	r *http.Request,
	actor builderActor,
	ifMatch string,
	damaged *bapi.DamagedDocument,
) error {
	name := store.ConfigFullName(damaged.Kind, damaged.Target)
	if name == "" {
		name = builderEveryConfig
	}

	if !builderConfigAllowed(actor.role, builderVerbGet, name) {
		return builderNotFound("document", damaged.ID)
	}

	if !builderConfigAllowed(actor.role, builderVerbDelete, name) {
		return builderForbidden(actor, "deleting damaged Builder document of "+name)
	}

	if damaged.ETag() != ifMatch {
		w.Header().Set("ETag", damaged.ETag())

		return weberror.NewWebError(nil, "document %s changed after you last read it", damaged.ID).
			SetStatus(http.StatusPreconditionFailed)
	}

	err := b.drafts.DeleteDamagedDocument(r.Context(), damaged.ID, damaged.Revision)

	switch {
	case errors.Is(err, bapi.ErrConflict):
		return weberror.NewWebError(err, "document %s changed after you last read it", damaged.ID).
			SetStatus(http.StatusPreconditionFailed)
	case err != nil:
		return builderWebError(err, "unable to delete damaged Builder document %s", damaged.ID)
	}

	plog.Info(
		plog.TypeAction,
		"deleted damaged published builder document",
		"user", actor.user,
		"document", damaged.ID,
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

		return nil, builderWebError(err, "unable to get Builder document %s", documentID)
	}

	if !builderConfigAllowed(actor.role, builderVerbGet, builderConfigName(document)) {
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

// currentBuilderDocument reports whether the target config of a published
// record currently references it (see [bapi.DocumentReference.Names]), and
// returns that config when it does. Superseded or orphaned records may remain
// after interrupted cleanup, but they are never listable or readable.
func (b *builderAPI) currentBuilderDocument(document *bapi.PublishedDocument) (*store.Config, bool, error) {
	configName := builderConfigName(document)
	if configName == "" {
		return nil, false, nil
	}

	config, err := b.getConfig(configName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, false, nil
		}

		return nil, false, err
	}

	reference, ok := builderConfigReference(config, document.ID)

	return config, ok && reference.Names(document), nil
}

// builderDocumentETag returns the entity tag of a published document that
// config, its target, currently references. It covers the document and all
// that config holds, so a publish or any other change of the config gives a
// new tag.
func builderDocumentETag(document *bapi.PublishedDocument, config *store.Config) (string, error) {
	data, err := config.StoredJSON()
	if err != nil {
		return "", weberror.NewWebError(err, "unable to read config %s", config.FullName()).
			SetStatus(http.StatusInternalServerError)
	}

	sum := sha256.New()
	sum.Write([]byte(document.ID + "\n"))
	sum.Write(data)

	return `"` + hex.EncodeToString(sum.Sum(nil))[:32] + `"`, nil
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

// topologyDocument resolves the Builder document that reference names, and reports false when it names none here.
// The stored published document wins over a file, because the topology was published from it.
// A digest beside a path pins the file, so a reference with a digest never gets other content.
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

// storedTopologyDocument returns the verified stored published document that reference names for topology name.
// It reports false when there is none. A damaged record or content gives an error that matches [bapi.ErrCorrupt].
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

	// Read the content with the record again. The record may have been stored
	// again since then, but its ID still says it has this target and digest.
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

// builderDocumentReferences tells a listing which published documents are current and which topologies name a file.
// It lists each config kind once, so old documents of deleted topologies do not each cost a config read.
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

// builderDocumentResponse is the JSON view of a stored published document ("store") or a Builder file ("file").
// A file row in a listing holds only its location, because a listing reads no file.
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
	// ETag is the entity tag that a delete of a stored document must name
	// (see [builderDocumentETag]). A file has none.
	ETag string `json:"etag,omitempty"`
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
