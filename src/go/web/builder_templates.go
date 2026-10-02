package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderTemplatesPath is the path of the template library listing.
	// The routes that change a library are below it, under the owner.
	builderTemplatesPath = builderRoutePrefix + "templates"

	// builderTemplateItemBytes bounds the body of a request replacing one
	// template (1 MiB): the template and the custom icon it names.
	builderTemplateItemBytes = 1 << 20

	// builderTemplateRequestBytes bounds the body of every other request
	// changing a library that carries no template (64 KiB).
	builderTemplateRequestBytes = 64 << 10

	// builderTemplateOwn is the source of a template or a collection of the
	// caller's own library.
	builderTemplateOwn = "own"
)

// Kinds a refused library request names.
const (
	builderKindTemplate   = "template"
	builderKindCollection = "collection"
)

// builderTemplateLibraryResponse is everything of the template libraries the
// caller can use: the caller's own templates and collections first. Icons
// holds the custom icons of every template in it, by icon ID, as a document
// carries them. Damaged is set when the caller's own library record cannot
// be read: the lists then hold nothing of it.
type builderTemplateLibraryResponse struct {
	Owner       string                              `json:"owner"`
	Templates   []builderTemplateResponse           `json:"templates"`
	Collections []builderTemplateCollectionResponse `json:"collections"`
	Icons       map[string]bdoc.Icon                `json:"icons,omitempty"`
	CanShare    bool                                `json:"canShare"`
	CanPublish  bool                                `json:"canPublish"`
	Damaged     bool                                `json:"damaged"`
	Limits      builderTemplateLimits               `json:"limits"`
}

// builderTemplateLimits is what a library and its items may hold.
type builderTemplateLimits struct {
	Templates        int `json:"templates"`
	Collections      int `json:"collections"`
	Shares           int `json:"shares"`
	NameBytes        int `json:"nameBytes"`
	DescriptionBytes int `json:"descriptionBytes"`
	DeviceBytes      int `json:"deviceBytes"`
	Icons            int `json:"icons"`
}

// builderTemplateResponse is one template of a library. Collections names
// the collections of the same owner that hold it. Shares is sent to the
// owner only; the account a share is bound to never is.
type builderTemplateResponse struct {
	ID          string                         `json:"id"`
	Owner       string                         `json:"owner"`
	Source      string                         `json:"source"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Device      bdoc.TemplateDevice            `json:"device"`
	Version     int64                          `json:"version"`
	ETag        string                         `json:"etag"`
	Created     time.Time                      `json:"created,omitzero"`
	Updated     time.Time                      `json:"updated,omitzero"`
	ServerWide  bool                           `json:"serverWide"`
	PublishedAt time.Time                      `json:"publishedAt,omitzero"`
	PublishedBy string                         `json:"publishedBy,omitempty"`
	Collections []string                       `json:"collections"`
	Shares      []builderTemplateShareResponse `json:"shares,omitzero"`
}

// builderTemplateCollectionResponse is one collection of a library.
type builderTemplateCollectionResponse struct {
	ID          string                         `json:"id"`
	Owner       string                         `json:"owner"`
	Source      string                         `json:"source"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	TemplateIDs []string                       `json:"templateIds"`
	Version     int64                          `json:"version"`
	ETag        string                         `json:"etag"`
	Created     time.Time                      `json:"created,omitzero"`
	Updated     time.Time                      `json:"updated,omitzero"`
	ServerWide  bool                           `json:"serverWide"`
	PublishedAt time.Time                      `json:"publishedAt,omitzero"`
	PublishedBy string                         `json:"publishedBy,omitempty"`
	Shares      []builderTemplateShareResponse `json:"shares,omitzero"`
}

// builderTemplateShareResponse is one user a template or a collection is
// shared with. Stale is set when the account it was shared with no longer
// exists, so the share grants nothing.
type builderTemplateShareResponse struct {
	User      string    `json:"user"`
	GrantedAt time.Time `json:"grantedAt"`
	Stale     bool      `json:"stale"`
}

// builderTemplateContent is what a template is made of or replaced with.
type builderTemplateContent struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Device      bdoc.TemplateDevice `json:"device"`
}

// builderTemplateCreateRequest adds templates to a library and, with
// Collection, a new collection holding exactly them. Icons carries the
// custom icons the templates name that the library may not hold yet.
type builderTemplateCreateRequest struct {
	Templates  []builderTemplateContent `json:"templates"`
	Collection *struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"collection"`
	Icons map[string]bdoc.Icon `json:"icons"`
}

// builderTemplateUpdateRequest replaces one template.
type builderTemplateUpdateRequest struct {
	builderTemplateContent

	Icons map[string]bdoc.Icon `json:"icons"`
}

// builderTemplateCollectionContent is what a collection is made of or
// replaced with.
type builderTemplateCollectionContent struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TemplateIDs []string `json:"templateIds"`
}

// builderTemplateSelection names templates and collections of one library.
type builderTemplateSelection struct {
	Templates   []string `json:"templates"`
	Collections []string `json:"collections"`
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

// builderTemplateStaleError is returned by a change of a library whose
// If-Match does not name the current content of the item it replaces.
type builderTemplateStaleError struct {
	kind, id, etag string
}

func (e *builderTemplateStaleError) Error() string {
	return e.kind + " " + e.id + " has changed since it was last read"
}

// builderTemplateError maps a failed change of a template library to its
// answer. A refusal is answered in the service's own words, which name the
// template by its index in the request and repeat nothing of its content; a
// stale If-Match with the current entity tag; a record this server cannot
// read with 409; and a library that kept changing under the write with 503.
func builderTemplateError(w http.ResponseWriter, err error, format string, args ...any) *weberror.WebError {
	var (
		stale   *builderTemplateStaleError
		refused *bapi.LibraryError
		missing *bapi.NotFoundError
	)

	switch {
	case errors.As(err, &stale):
		w.Header().Set("ETag", stale.etag)

		return weberror.NewWebError(nil, "%s", stale.Error()).SetStatus(http.StatusPreconditionFailed)
	case errors.As(err, &refused):
		return builderWebError(err, "%s", refused.Reason)
	case errors.As(err, &missing):
		return builderNotFound(missing.Kind, missing.ID)
	case errors.Is(err, bapi.ErrCorrupt):
		return weberror.NewWebError(err, "this library cannot be read by this version of phenix").
			SetStatus(http.StatusConflict)
	case errors.Is(err, bapi.ErrBusy):
		w.Header().Set("Retry-After", "1")
	}

	return builderWebError(err, format, args...)
}

// builderTemplateLibraryLimits returns the limits of a template library.
func builderTemplateLibraryLimits() builderTemplateLimits {
	return builderTemplateLimits{
		Templates:        bapi.MaxLibraryTemplates,
		Collections:      bapi.MaxLibraryCollections,
		Shares:           bapi.MaxShares,
		NameBytes:        bdoc.MaxTemplateNameBytes,
		DescriptionBytes: bdoc.MaxTemplateDescriptionBytes,
		DeviceBytes:      bdoc.MaxTemplateDeviceBytes,
		Icons:            bapi.MaxLibraryTemplateIcons,
	}
}

// templateOwner returns the request's actor once it holds the base config
// permission of verb and is the owner the path names. A library is changed
// by its owner only: anyone else is answered 404, as for a draft it may not
// see, whatever its role.
func (b *builderAPI) templateOwner(r *http.Request, verb builderVerb, action string) (builderActor, error) {
	actor, err := builderAuthorize(r, verb, action)
	if err != nil {
		return actor, err
	}

	if owner := mux.Vars(r)["owner"]; owner != actor.user {
		plog.Warn(
			plog.TypeSecurity,
			"builder template library request for another user not allowed",
			"user", actor.user,
			"owner", owner,
			"action", action,
		)

		return actor, builderNotFound("template library", owner)
	}

	return actor, nil
}

// changeOwnLibrary applies change to the library of actor, as actor, in one
// write (see [bapi.Service.UpdateLibrary]): change may run more than once.
func (b *builderAPI) changeOwnLibrary(
	r *http.Request,
	actor builderActor,
	change func(*bapi.TemplateLibrary) error,
) (*bapi.TemplateLibrary, error) {
	return b.drafts.UpdateLibrary(r.Context(), actor.user, actor.user, change)
}

// templateShares returns who an item of the caller's own library is shared
// with, each entry checked against the account it was shared with. accounts
// reads an account at most once however many items name it.
func (b *builderAPI) templateShares(
	shares []bapi.TemplateShare,
	accounts map[string]func() (string, bool, error),
) ([]builderTemplateShareResponse, error) {
	responses := make([]builderTemplateShareResponse, 0, len(shares))

	for _, share := range shares {
		account, ok := accounts[share.User]
		if !ok {
			account = b.accountOnce(share.User)
			accounts[share.User] = account
		}

		created, exists, err := account()
		if err != nil {
			return nil, err
		}

		responses = append(responses, builderTemplateShareResponse{
			User:      share.User,
			GrantedAt: share.GrantedAt,
			Stale:     !exists || created != share.UserCreated,
		})
	}

	return responses, nil
}

// ownTemplate returns one template of the caller's own library as responses
// carry it.
func (b *builderAPI) ownTemplate(
	library *bapi.TemplateLibrary,
	template *bapi.LibraryTemplate,
	accounts map[string]func() (string, bool, error),
) (builderTemplateResponse, error) {
	shares, err := b.templateShares(template.Shares, accounts)
	if err != nil {
		return builderTemplateResponse{}, err
	}

	response := builderTemplateResponse{
		ID:          template.ID,
		Owner:       library.Owner,
		Source:      builderTemplateOwn,
		Name:        template.Name,
		Description: template.Description,
		Device:      template.Device,
		Version:     template.Version,
		ETag:        template.ETag(),
		Created:     template.Created,
		Updated:     template.Updated,
		ServerWide:  template.Public != nil,
		PublishedAt: time.Time{},
		PublishedBy: "",
		Collections: []string{},
		Shares:      shares,
	}

	if template.Public != nil {
		response.PublishedAt, response.PublishedBy = template.Public.At, template.Public.By
	}

	for i := range library.Collections {
		for _, id := range library.Collections[i].TemplateIDs {
			if id == template.ID {
				response.Collections = append(response.Collections, library.Collections[i].ID)
			}
		}
	}

	return response, nil
}

// ownCollection returns one collection of the caller's own library as
// responses carry it.
func (b *builderAPI) ownCollection(
	library *bapi.TemplateLibrary,
	collection *bapi.TemplateCollection,
	accounts map[string]func() (string, bool, error),
) (builderTemplateCollectionResponse, error) {
	shares, err := b.templateShares(collection.Shares, accounts)
	if err != nil {
		return builderTemplateCollectionResponse{}, err
	}

	response := builderTemplateCollectionResponse{
		ID:          collection.ID,
		Owner:       library.Owner,
		Source:      builderTemplateOwn,
		Name:        collection.Name,
		Description: collection.Description,
		TemplateIDs: append([]string{}, collection.TemplateIDs...),
		Version:     collection.Version,
		ETag:        collection.ETag(),
		Created:     collection.Created,
		Updated:     collection.Updated,
		ServerWide:  collection.Public != nil,
		PublishedAt: time.Time{},
		PublishedBy: "",
		Shares:      shares,
	}

	if collection.Public != nil {
		response.PublishedAt, response.PublishedBy = collection.Public.At, collection.Public.By
	}

	return response, nil
}

// listTemplates - GET /builder/templates.
//
// The answer holds the caller's own library, which is the built-in templates
// until the caller changes it. Only the caller's library record is read: no
// other user's library is listed or read here. A record this server cannot
// read is reported as damaged, with none of its items.
func (b *builderAPI) listTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListTemplates")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder templates")
	if err != nil {
		return err
	}

	response := builderTemplateLibraryResponse{
		Owner:       actor.user,
		Templates:   []builderTemplateResponse{},
		Collections: []builderTemplateCollectionResponse{},
		Icons:       nil,
		CanShare:    false,
		CanPublish:  false,
		Damaged:     false,
		Limits:      builderTemplateLibraryLimits(),
	}

	library, err := b.drafts.GetLibrary(r.Context(), actor.user)

	switch {
	case errors.Is(err, bapi.ErrCorrupt):
		plog.Error(plog.TypeSystem, "builder template library is unreadable", "user", actor.user, "err", err)

		response.Damaged = true

		return builderWriteJSON(w, http.StatusOK, "", response)
	case err != nil:
		return builderWebError(err, "unable to list the templates")
	}

	accounts := map[string]func() (string, bool, error){}

	for i := range library.Templates {
		template, err := b.ownTemplate(library, &library.Templates[i], accounts)
		if err != nil {
			return weberror.NewWebError(err, "unable to list the templates").SetStatus(http.StatusInternalServerError)
		}

		response.Templates = append(response.Templates, template)

		// Only the icons the listed templates name are sent.
		if icon, ok := library.Icons[template.Device.Icon]; ok {
			if response.Icons == nil {
				response.Icons = map[string]bdoc.Icon{}
			}

			response.Icons[template.Device.Icon] = icon
		}
	}

	for i := range library.Collections {
		collection, err := b.ownCollection(library, &library.Collections[i], accounts)
		if err != nil {
			return weberror.NewWebError(err, "unable to list the templates").SetStatus(http.StatusInternalServerError)
		}

		response.Collections = append(response.Collections, collection)
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// createTemplates - POST /builder/templates/{owner}/items.
//
// Adds 1 to [bapi.MaxLibraryTemplates] templates, each under a new ID, and
// with "collection" a new collection holding exactly them, all in one write.
func (b *builderAPI) createTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateTemplates")

	actor, err := b.templateOwner(r, builderVerbCreate, "adding builder templates")
	if err != nil {
		return err
	}

	var request builderTemplateCreateRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	if len(request.Templates) == 0 {
		return weberror.NewWebError(nil, "at least one template is required").SetStatus(http.StatusBadRequest)
	}

	templates := make([]bdoc.Template, 0, len(request.Templates))

	for _, content := range request.Templates {
		templates = append(templates, bdoc.Template{
			ID: "", Name: content.Name, Description: content.Description, Device: content.Device,
		})
	}

	var (
		ids        []string
		collection string
	)

	library, err := b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		var err error

		collection = ""

		ids, err = library.AddTemplates(templates, request.Icons, b.drafts.NewID)
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

// putTemplate - PUT /builder/templates/{owner}/items/{template}.
//
// Replaces the name, the description and the device of one template. The
// If-Match header must name the entity tag of the content it replaces.
func (b *builderAPI) putTemplate(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutTemplate")

	actor, err := b.templateOwner(r, builderVerbUpdate, "changing a builder template")
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

	var request builderTemplateUpdateRequest

	if err := builderDecodeLimit(w, r, &request, builderTemplateItemBytes); err != nil {
		return err
	}

	content := bdoc.Template{ID: id, Name: request.Name, Description: request.Description, Device: request.Device}

	library, err := b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		if current := library.Template(id); current != nil && current.ETag() != ifMatch {
			return &builderTemplateStaleError{kind: builderKindTemplate, id: id, etag: current.ETag()}
		}

		return library.ReplaceTemplate(id, content, request.Icons)
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to change template %s", id)
	}

	template := library.Template(id)

	response, err := b.ownTemplate(library, template, map[string]func() (string, bool, error){})
	if err != nil {
		return weberror.NewWebError(err, "unable to read template %s", id).SetStatus(http.StatusInternalServerError)
	}

	plog.Info(
		plog.TypeAction, "changed builder template",
		"user", actor.user, builderKindTemplate, id, "version", template.Version,
	)

	return builderWriteJSON(w, http.StatusOK, template.ETag(), response)
}

// createTemplateCollection - POST /builder/templates/{owner}/collections.
//
// Adds a collection of templates the library already holds.
func (b *builderAPI) createTemplateCollection(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateTemplateCollection")

	actor, err := b.templateOwner(r, builderVerbCreate, "adding a builder template collection")
	if err != nil {
		return err
	}

	var request builderTemplateCollectionContent

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	var id string

	library, err := b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
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

// putTemplateCollection - PUT /builder/templates/{owner}/collections/{collection}.
//
// Replaces the name, the description and the templates of one collection.
// The If-Match header must name the entity tag of the content it replaces.
func (b *builderAPI) putTemplateCollection(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutTemplateCollection")

	actor, err := b.templateOwner(r, builderVerbUpdate, "changing a builder template collection")
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

	library, err := b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
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

// writeCollection answers with one collection of the caller's own library,
// under the entity tag of its content.
func (b *builderAPI) writeCollection(w http.ResponseWriter, status int, library *bapi.TemplateLibrary, id string) error {
	collection := library.Collection(id)

	response, err := b.ownCollection(library, collection, map[string]func() (string, bool, error){})
	if err != nil {
		return weberror.NewWebError(err, "unable to read collection %s", id).SetStatus(http.StatusInternalServerError)
	}

	return builderWriteJSON(w, status, collection.ETag(), response)
}

// deleteTemplates - POST /builder/templates/{owner}/delete.
//
// Deletes the named templates and collections in one write. An ID the
// library does not hold is ignored, so a repeat is harmless. A deleted
// template leaves every collection that held it; a deleted collection
// leaves its templates.
func (b *builderAPI) deleteTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteTemplates")

	actor, err := b.templateOwner(r, builderVerbDelete, "deleting builder templates")
	if err != nil {
		return err
	}

	var request builderTemplateSelection

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	if len(request.Templates)+len(request.Collections) == 0 {
		return weberror.NewWebError(nil, "at least one template or collection is required").
			SetStatus(http.StatusBadRequest)
	}

	var response builderTemplateDeleteResponse

	_, err = b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		response.Deleted.Templates, response.Deleted.Collections = library.Delete(request.Templates, request.Collections)

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
