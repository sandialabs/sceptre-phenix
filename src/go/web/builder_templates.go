package web

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderTemplatesPath is the path of the template library listing.
	// The routes that change the library are below it.
	builderTemplatesPath = builderRoutePrefix + "templates"

	// builderTemplateItemBytes bounds the body of a request replacing one
	// template (1 MiB).
	builderTemplateItemBytes = 1 << 20

	// builderTemplateRequestBytes bounds the body of every other request
	// changing a library that carries no template (64 KiB).
	builderTemplateRequestBytes = 64 << 10

	// builderTemplateLibrary is the source of a template or a collection of
	// the template library of the server.
	builderTemplateLibrary = string(bdoc.TemplateSourceLibrary)

	// builderTemplateBuiltin is the source of a built-in template.
	builderTemplateBuiltin = string(bdoc.TemplateSourceBuiltin)

	// builderTemplatePreloaded is the source of a template or a collection
	// the server read from one of its template files at start.
	builderTemplatePreloaded = string(bdoc.TemplateSourcePreloaded)
)

// Kinds a refused library request names.
const (
	builderKindTemplate   = "template"
	builderKindCollection = "collection"
)

// builderTemplateLibraryResponse is every template the caller can use: the server library, then the read-only templates.
// Damaged is true when the library record cannot be read. Templates and Collections then hold nothing.
type builderTemplateLibraryResponse struct {
	Templates   []builderTemplateResponse           `json:"templates"`
	Collections []builderTemplateCollectionResponse `json:"collections"`
	Builtins    []builderTemplateResponse           `json:"builtins"`
	Preloaded   []builderPreloadedCollection        `json:"preloaded"`
	Damaged     bool                                `json:"damaged"`
	Limits      builderTemplateLimits               `json:"limits"`
}

// builderPreloadedCollection is one collection the server read from a
// template file, with its templates. The listing gives each as it gives any
// other item, with source "preloaded" and version 1.
type builderPreloadedCollection struct {
	Collection builderTemplateCollectionResponse `json:"collection"`
	Templates  []builderTemplateResponse         `json:"templates"`
}

// builderTemplateLimits is what the library and its items may hold.
type builderTemplateLimits struct {
	Templates        int `json:"templates"`
	Collections      int `json:"collections"`
	NameBytes        int `json:"nameBytes"`
	DescriptionBytes int `json:"descriptionBytes"`
	DeviceBytes      int `json:"deviceBytes"`
}

// builderTemplateResponse is one template. Source names its kind, as [bdoc.TemplateSource] does.
// A read-only template has no times and no users.
type builderTemplateResponse struct {
	ID          string              `json:"id"`
	Source      string              `json:"source"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Device      bdoc.TemplateDevice `json:"device"`
	Version     int64               `json:"version"`
	ETag        string              `json:"etag"`
	Created     time.Time           `json:"created,omitzero"`
	Updated     time.Time           `json:"updated,omitzero"`
	CreatedBy   string              `json:"createdBy,omitempty"`
	UpdatedBy   string              `json:"updatedBy,omitempty"`
	Collections []string            `json:"collections"`
}

// builderTemplateCollectionResponse is one collection. CreatedBy and
// UpdatedBy are as for a template.
type builderTemplateCollectionResponse struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	TemplateIDs []string  `json:"templateIds"`
	Version     int64     `json:"version"`
	ETag        string    `json:"etag"`
	Created     time.Time `json:"created,omitzero"`
	Updated     time.Time `json:"updated,omitzero"`
	CreatedBy   string    `json:"createdBy,omitempty"`
	UpdatedBy   string    `json:"updatedBy,omitempty"`
}

// builderTemplateContent is what a template is made of or replaced with.
type builderTemplateContent struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Device      bdoc.TemplateDevice `json:"device"`
}

// builderTemplateCreateRequest adds templates to a library and, with
// Collection, a new collection holding exactly them.
type builderTemplateCreateRequest struct {
	Templates  []builderTemplateContent `json:"templates"`
	Collection *struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"collection"`
}

// builderTemplateUpdateRequest replaces one template.
type builderTemplateUpdateRequest struct {
	builderTemplateContent
}

// builderTemplateCollectionContent is what a collection is made of or
// replaced with.
type builderTemplateCollectionContent struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TemplateIDs []string `json:"templateIds"`
}

// builderTemplateSelection names templates and collections of the library,
// each with the entity tag of the content the caller read.
type builderTemplateSelection struct {
	Templates   []builderTemplateRef `json:"templates"`
	Collections []builderTemplateRef `json:"collections"`
}

// builderTemplateRefIDs returns the IDs that refs name.
func builderTemplateRefIDs(refs []builderTemplateRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}

	return ids
}

// check returns the [builderTemplateStaleError] of the first item that
// library holds at another tag. Items the library does not hold pass.
func (s builderTemplateSelection) check(library *bapi.TemplateLibrary) error {
	for _, ref := range s.Templates {
		if current := library.Template(ref.ID); current != nil && current.ETag() != ref.ETag {
			return &builderTemplateStaleError{kind: builderKindTemplate, id: ref.ID, etag: current.ETag()}
		}
	}

	for _, ref := range s.Collections {
		if current := library.Collection(ref.ID); current != nil && current.ETag() != ref.ETag {
			return &builderTemplateStaleError{kind: builderKindCollection, id: ref.ID, etag: current.ETag()}
		}
	}

	return nil
}

// tagged refuses a selection with an item that has no strong, quoted entity
// tag, as a PUT without If-Match is refused.
func (s builderTemplateSelection) tagged() error {
	for _, ref := range slices.Concat(s.Templates, s.Collections) {
		if !builderStrongETag(ref.ETag) {
			return weberror.NewWebError(nil, "each template and collection to delete needs its etag").
				SetStatus(http.StatusBadRequest)
		}
	}

	return nil
}

// builderTemplateRef names a template or a collection a request made, with
// the entity tag of its content.
type builderTemplateRef struct {
	ID   string `json:"id"`
	ETag string `json:"etag"`
}

// builderTemplateCreateResponse is what a request adding templates made.
type builderTemplateCreateResponse struct {
	Created    []builderTemplateRef `json:"created"`
	Collection *builderTemplateRef  `json:"collection,omitempty"`
}

// builderTemplateDeleteResponse is how many templates and collections a
// request deleted.
type builderTemplateDeleteResponse struct {
	Deleted struct {
		Templates   int `json:"templates"`
		Collections int `json:"collections"`
	} `json:"deleted"`
}

// builderTemplateStaleError is returned by a change of the library whose
// If-Match does not name the current content of the item it replaces.
type builderTemplateStaleError struct {
	kind, id, etag string
}

func (e *builderTemplateStaleError) Error() string {
	return e.kind + " " + e.id + " changed after you read it. Reload the template library and try again"
}

// builderTemplateError maps a failed change of the template library to its status and code.
// A refusal names the template by its request index and repeats nothing of its content.
func builderTemplateError(w http.ResponseWriter, err error, format string, args ...any) *weberror.WebError {
	var (
		stale   *builderTemplateStaleError
		refused *bapi.LibraryError
		missing *bapi.NotFoundError
	)

	switch {
	case errors.As(err, &stale):
		w.Header().Set("ETag", stale.etag)

		return weberror.NewWebError(nil, "%s", stale.Error()).
			SetStatus(http.StatusPreconditionFailed).WithCode(string(bdoc.CodeTemplateItemStale))
	case errors.As(err, &refused):
		return builderWebError(err, "%s", refused.Reason)
	case errors.As(err, &missing):
		return builderNotFound(missing.Kind, missing.ID)
	case errors.Is(err, bapi.ErrCorrupt):
		return weberror.NewWebError(err, "this version of phenix cannot read the template library").
			SetStatus(http.StatusConflict)
	case errors.Is(err, bapi.ErrBusy):
		w.Header().Set("Retry-After", "1")
	}

	return builderWebError(err, format, args...)
}

// builderTemplateLibraryLimits returns the limits of the template library.
func builderTemplateLibraryLimits() builderTemplateLimits {
	return builderTemplateLimits{
		Templates:        bapi.MaxLibraryTemplates,
		Collections:      bapi.MaxLibraryCollections,
		NameBytes:        bdoc.MaxTemplateNameBytes,
		DescriptionBytes: bdoc.MaxTemplateDescriptionBytes,
		DeviceBytes:      bdoc.MaxTemplateDeviceBytes,
	}
}

// changeLibrary applies change to the template library of the server, as
// actor, in one write (see [bapi.Service.UpdateLibrary]): change may run more
// than once.
func (b *builderAPI) changeLibrary(
	r *http.Request,
	actor builderActor,
	change func(*bapi.TemplateLibrary) error,
) (*bapi.TemplateLibrary, error) {
	return b.drafts.UpdateLibrary(r.Context(), actor.user, change)
}

// libraryTemplate returns one template of the library as responses carry it,
// with every collection that holds it.
func libraryTemplate(library *bapi.TemplateLibrary, template *bapi.LibraryTemplate) builderTemplateResponse {
	response := builderTemplateResponse{
		ID:          template.ID,
		Source:      builderTemplateLibrary,
		Name:        template.Name,
		Description: template.Description,
		Device:      template.Device,
		Version:     template.Version,
		ETag:        template.ETag(),
		Created:     template.Created,
		Updated:     template.Updated,
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
		Collections: []string{},
	}

	for i := range library.Collections {
		collection := &library.Collections[i]

		if slices.Contains(collection.TemplateIDs, template.ID) {
			response.Collections = append(response.Collections, collection.ID)
		}
	}

	return response
}

// libraryCollection returns one collection of the library as responses carry
// it.
func libraryCollection(collection *bapi.TemplateCollection) builderTemplateCollectionResponse {
	return builderTemplateCollectionResponse{
		ID:          collection.ID,
		Source:      builderTemplateLibrary,
		Name:        collection.Name,
		Description: collection.Description,
		TemplateIDs: append([]string{}, collection.TemplateIDs...),
		Version:     collection.Version,
		ETag:        collection.ETag(),
		Created:     collection.Created,
		Updated:     collection.Updated,
		CreatedBy:   collection.CreatedBy,
		UpdatedBy:   collection.UpdatedBy,
	}
}

// readOnlyTemplate returns a built-in template or a template of a template
// file as responses carry it: source is "builtin" or "preloaded", at version
// 1, in the named collections.
func readOnlyTemplate(template *bdoc.Template, source string, collections []string) builderTemplateResponse {
	return builderTemplateResponse{
		ID:          template.ID,
		Source:      source,
		Name:        template.Name,
		Description: template.Description,
		Device:      template.Device,
		Version:     bapi.ReadOnlyTemplateVersion,
		ETag:        bapi.RevisionETag(bapi.ReadOnlyTemplateVersion),
		Created:     time.Time{},
		Updated:     time.Time{},
		CreatedBy:   "",
		UpdatedBy:   "",
		Collections: collections,
	}
}

// builtinTemplates returns the built-in templates as the listing gives them
// (see [bdoc.BuiltinTemplates]): source "builtin", at version 1, in no
// collection.
func builtinTemplates() []builderTemplateResponse {
	builtins := bdoc.BuiltinTemplates()
	listed := make([]builderTemplateResponse, 0, len(builtins))

	for i := range builtins {
		listed = append(listed, readOnlyTemplate(&builtins[i], builderTemplateBuiltin, []string{}))
	}

	return listed
}

// preloadedCollections returns the collections the server read from its
// template files at start (see [bapi.Service.LoadServerTemplates]), as the
// listing gives them. Each has its templates, source "preloaded", and version
// 1.
func (b *builderAPI) preloadedCollections() []builderPreloadedCollection {
	collections := b.drafts.ServerCollections()
	listed := make([]builderPreloadedCollection, 0, len(collections))
	etag := bapi.RevisionETag(bapi.ReadOnlyTemplateVersion)

	for i := range collections {
		collection := &collections[i]
		ids := make([]string, 0, len(collection.Templates))
		templates := make([]builderTemplateResponse, 0, len(collection.Templates))

		for j := range collection.Templates {
			template := &collection.Templates[j]

			ids = append(ids, template.ID)
			templates = append(templates, readOnlyTemplate(template, builderTemplatePreloaded, []string{collection.ID}))
		}

		listed = append(listed, builderPreloadedCollection{
			Collection: builderTemplateCollectionResponse{
				ID:          collection.ID,
				Source:      builderTemplatePreloaded,
				Name:        collection.Name,
				Description: collection.Description,
				TemplateIDs: ids,
				Version:     bapi.ReadOnlyTemplateVersion,
				ETag:        etag,
				Created:     time.Time{},
				Updated:     time.Time{},
				CreatedBy:   "",
				UpdatedBy:   "",
			},
			Templates: templates,
		})
	}

	return listed
}

// refuseReadOnly fails a change of the library that names a built-in
// template, or a template or a collection that the server read from one of
// its template files. Those items are read only for everyone. The refusal is
// a 409 with the code [bdoc.CodeTemplateItemReadOnly] that names the item and
// tells how to get a copy that can be changed.
func (b *builderAPI) refuseReadOnly(templateIDs, collectionIDs []string) error {
	readOnly := func(format string, args ...any) error {
		return weberror.NewWebError(nil, format, args...).
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodeTemplateItemReadOnly))
	}

	for _, template := range bdoc.BuiltinTemplates() {
		if slices.Contains(templateIDs, template.ID) {
			return readOnly(
				"template %s (%q) is built in and is read only. Duplicate it into the library to change it",
				template.ID, template.Name,
			)
		}
	}

	collections := b.drafts.ServerCollections()

	for i := range collections {
		collection := &collections[i]

		if slices.Contains(collectionIDs, collection.ID) {
			return readOnly(
				"collection %s (%q) comes from a template file of the phenix server and is read only. "+
					"Duplicate its templates into the library to change them",
				collection.ID, collection.Name,
			)
		}

		for j := range collection.Templates {
			template := &collection.Templates[j]

			if slices.Contains(templateIDs, template.ID) {
				return readOnly(
					"template %s (%q) comes from a template file of the phenix server and is read only. "+
						"Duplicate it into the library to change it",
					template.ID, template.Name,
				)
			}
		}
	}

	return nil
}

// listTemplates - GET /builder/templates.
// Every caller gets the same answer. An unreadable library record is reported as damaged, with none of its items.
func (b *builderAPI) listTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListTemplates")

	actor, err := builderAuthorize(r, "listing Builder templates")
	if err != nil {
		return err
	}

	response := builderTemplateLibraryResponse{
		Templates:   []builderTemplateResponse{},
		Collections: []builderTemplateCollectionResponse{},
		Builtins:    builtinTemplates(),
		Preloaded:   b.preloadedCollections(),
		Damaged:     false,
		Limits:      builderTemplateLibraryLimits(),
	}

	library, err := b.drafts.GetLibrary(r.Context())

	switch {
	case errors.Is(err, bapi.ErrCorrupt):
		plog.Error(plog.TypeSystem, "builder template library is unreadable", "user", actor.user, "err", err)

		response.Damaged = true
	case err != nil:
		return builderWebError(err, "unable to list the templates")
	default:
		for i := range library.Templates {
			response.Templates = append(response.Templates, libraryTemplate(library, &library.Templates[i]))
		}

		for i := range library.Collections {
			response.Collections = append(response.Collections, libraryCollection(&library.Collections[i]))
		}
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// createTemplates - POST /builder/templates/items.
//
// Adds 1 to [bapi.MaxLibraryTemplates] templates, each under a new ID, and
// with "collection" a new collection holding exactly them, all in one write.
func (b *builderAPI) createTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateTemplates")

	actor, err := builderAuthorize(r, "adding Builder templates")
	if err != nil {
		return err
	}

	var request builderTemplateCreateRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	if len(request.Templates) == 0 {
		return weberror.NewWebError(nil, "the request needs at least one template").SetStatus(http.StatusBadRequest)
	}

	templates := make([]bdoc.Template, 0, len(request.Templates))

	for _, content := range request.Templates {
		templates = append(templates, bdoc.Template{
			ID: "", Name: content.Name, Description: content.Description, Device: content.Device, Source: nil,
		})
	}

	var (
		ids        []string
		collection string
	)

	library, err := b.changeLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		var err error

		collection = ""

		ids, err = library.AddTemplates(templates, b.drafts.NewID)
		if err != nil || request.Collection == nil {
			return err
		}

		collection, err = library.AddCollection(bapi.CollectionContent{
			Name:        request.Collection.Name,
			Description: request.Collection.Description,
			TemplateIDs: ids,
		}, b.drafts.NewID)

		return err
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to add the templates")
	}

	response := builderTemplateCreateResponse{Created: make([]builderTemplateRef, 0, len(ids)), Collection: nil}

	for _, id := range ids {
		response.Created = append(response.Created, builderTemplateRef{ID: id, ETag: library.Template(id).ETag()})
	}

	if collection != "" {
		response.Collection = &builderTemplateRef{ID: collection, ETag: library.Collection(collection).ETag()}
	}

	plog.Info(
		plog.TypeAction, "added builder templates",
		"user", actor.user, "templates", len(ids), builderKindCollection, collection,
	)

	return builderWriteJSON(w, http.StatusCreated, "", response)
}

// putTemplate - PUT /builder/templates/items/{template}.
//
// Replaces the name, the description and the device of one template. The
// If-Match header must name the entity tag of the content it replaces.
func (b *builderAPI) putTemplate(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutTemplate")

	actor, err := builderAuthorize(r, "changing a Builder template")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	id := mux.Vars(r)[builderKindTemplate]

	if !bapi.ValidID(id) {
		return builderNotFound(builderKindTemplate, id)
	}

	if err := b.refuseReadOnly([]string{id}, nil); err != nil {
		return err
	}

	var request builderTemplateUpdateRequest

	if err := builderDecodeLimit(w, r, &request, builderTemplateItemBytes); err != nil {
		return err
	}

	content := bdoc.Template{
		ID: id, Name: request.Name, Description: request.Description, Device: request.Device, Source: nil,
	}

	library, err := b.changeLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		if current := library.Template(id); current != nil && current.ETag() != ifMatch {
			return &builderTemplateStaleError{kind: builderKindTemplate, id: id, etag: current.ETag()}
		}

		return library.ReplaceTemplate(id, content)
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to change template %s", id)
	}

	template := library.Template(id)

	plog.Info(
		plog.TypeAction, "changed builder template",
		"user", actor.user, builderKindTemplate, id, "version", template.Version,
	)

	return builderWriteJSON(w, http.StatusOK, template.ETag(), libraryTemplate(library, template))
}

// createTemplateCollection - POST /builder/templates/collections.
//
// Adds a collection of templates the library already holds.
func (b *builderAPI) createTemplateCollection(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateTemplateCollection")

	actor, err := builderAuthorize(r, "adding a Builder template collection")
	if err != nil {
		return err
	}

	var request builderTemplateCollectionContent

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	if err := b.refuseReadOnly(request.TemplateIDs, nil); err != nil {
		return err
	}

	var id string

	library, err := b.changeLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		var err error

		id, err = library.AddCollection(request.content(), b.drafts.NewID)

		return err
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to add the collection")
	}

	plog.Info(plog.TypeAction, "added builder template collection", "user", actor.user, builderKindCollection, id)

	return b.writeCollection(w, http.StatusCreated, library, id)
}

// putTemplateCollection - PUT /builder/templates/collections/{collection}.
//
// Replaces the name, the description and the templates of one collection.
// The If-Match header must name the entity tag of the content it replaces.
func (b *builderAPI) putTemplateCollection(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutTemplateCollection")

	actor, err := builderAuthorize(r, "changing a Builder template collection")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	id := mux.Vars(r)[builderKindCollection]

	if !bapi.ValidID(id) {
		return builderNotFound(builderKindCollection, id)
	}

	var request builderTemplateCollectionContent

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	if err := b.refuseReadOnly(request.TemplateIDs, []string{id}); err != nil {
		return err
	}

	library, err := b.changeLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		if current := library.Collection(id); current != nil && current.ETag() != ifMatch {
			return &builderTemplateStaleError{kind: builderKindCollection, id: id, etag: current.ETag()}
		}

		return library.ReplaceCollection(id, request.content())
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to change collection %s", id)
	}

	plog.Info(plog.TypeAction, "changed builder template collection", "user", actor.user, builderKindCollection, id)

	return b.writeCollection(w, http.StatusOK, library, id)
}

// content returns the collection content a request carries.
func (c builderTemplateCollectionContent) content() bapi.CollectionContent {
	return bapi.CollectionContent{Name: c.Name, Description: c.Description, TemplateIDs: c.TemplateIDs}
}

// writeCollection answers with one collection of the library, under the
// entity tag of its content.
func (b *builderAPI) writeCollection(w http.ResponseWriter, status int, library *bapi.TemplateLibrary, id string) error {
	collection := library.Collection(id)

	return builderWriteJSON(w, status, collection.ETag(), libraryCollection(collection))
}

// deleteTemplates - POST /builder/templates/delete.
// It deletes in one write. A changed entity tag refuses the whole request with 412. An unknown ID is ignored.
func (b *builderAPI) deleteTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteTemplates")

	actor, err := builderAuthorize(r, "deleting Builder templates")
	if err != nil {
		return err
	}

	var request builderTemplateSelection

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	if len(request.Templates)+len(request.Collections) == 0 {
		return weberror.NewWebError(nil, "the request needs at least one template or collection").
			SetStatus(http.StatusBadRequest)
	}

	if err := request.tagged(); err != nil {
		return err
	}

	templates := builderTemplateRefIDs(request.Templates)
	collections := builderTemplateRefIDs(request.Collections)

	if err := b.refuseReadOnly(templates, collections); err != nil {
		return err
	}

	var response builderTemplateDeleteResponse

	_, err = b.changeLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		if err := request.check(library); err != nil {
			return err
		}

		response.Deleted.Templates, response.Deleted.Collections = library.Delete(templates, collections)

		return nil
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to delete the templates")
	}

	plog.Info(
		plog.TypeAction, "deleted builder templates",
		"user", actor.user, "templates", response.Deleted.Templates, "collections", response.Deleted.Collections,
	)

	return builderWriteJSON(w, http.StatusOK, "", response)
}
