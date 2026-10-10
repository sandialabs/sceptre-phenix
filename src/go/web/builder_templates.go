package web

import (
	"context"
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

const (
	// builderTemplatesPath is the path of the template library listing.
	// The routes that change a library are below it, under the owner.
	builderTemplatesPath = builderRoutePrefix + "templates"

	// builderTemplateItemBytes bounds the body of a request replacing one
	// template (1 MiB).
	builderTemplateItemBytes = 1 << 20

	// builderTemplateRequestBytes bounds the body of every other request
	// changing a library that carries no template (64 KiB).
	builderTemplateRequestBytes = 64 << 10

	// builderTemplateOwn is the source of a template or a collection of the
	// caller's own library.
	builderTemplateOwn = "own"

	// builderTemplatePreloaded is the source of a template or a collection
	// the server read from one of its template files at start.
	builderTemplatePreloaded = "preloaded"

	// builderTemplatePreloadedVersion is the version of every template and
	// collection the server read from a template file: it never changes
	// while the server runs.
	builderTemplatePreloadedVersion = 1
)

// Kinds a refused library request names.
const (
	builderKindTemplate   = "template"
	builderKindCollection = "collection"
)

// builderTemplateLibraryResponse is everything of the template libraries the
// caller can use: the caller's own templates and collections first. A
// template names its custom icon, which the icon library resolves. Damaged
// is set when the caller's own library record cannot be read: the lists
// then hold nothing of it. Preloaded are the collections the server read from
// its template files at start, read only for everyone.
type builderTemplateLibraryResponse struct {
	Owner       string                              `json:"owner"`
	Templates   []builderTemplateResponse           `json:"templates"`
	Collections []builderTemplateCollectionResponse `json:"collections"`
	Preloaded   []builderPreloadedCollection        `json:"preloaded"`
	CanShare    bool                                `json:"canShare"`
	CanPublish  bool                                `json:"canPublish"`
	Damaged     bool                                `json:"damaged"`
	Limits      builderTemplateLimits               `json:"limits"`
}

// builderPreloadedCollection is one collection the server read from a
// template file, with its templates, each as the listing gives any other:
// source "preloaded", no owner, version 1.
type builderPreloadedCollection struct {
	Collection builderTemplateCollectionResponse `json:"collection"`
	Templates  []builderTemplateResponse         `json:"templates"`
}

// builderTemplateLimits is what a library and its items may hold.
type builderTemplateLimits struct {
	Templates        int `json:"templates"`
	Collections      int `json:"collections"`
	Shares           int `json:"shares"`
	NameBytes        int `json:"nameBytes"`
	DescriptionBytes int `json:"descriptionBytes"`
	DeviceBytes      int `json:"deviceBytes"`
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

// builderTemplateShareRequest adds users to, and removes users from, who
// templates and collections of the caller's library are shared with.
type builderTemplateShareRequest struct {
	Templates   []string `json:"templates"`
	Collections []string `json:"collections"`
	Add         []string `json:"add"`
	Remove      []string `json:"remove"`
}

// builderTemplatePublishRequest publishes templates and collections of one
// library to every user, or with ServerWide false takes them back.
type builderTemplatePublishRequest struct {
	Templates   []string `json:"templates"`
	Collections []string `json:"collections"`
	ServerWide  *bool    `json:"serverWide"`
}

// builderTemplateFailure names an item a request left unchanged, and why:
// "not-found" or "too-many", and the code of that reason.
type builderTemplateFailure struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Code   string `json:"code"`
}

// builderTemplateResult is what a request sharing or publishing items could
// not do. Failed is empty when every item took the change.
type builderTemplateResult struct {
	Failed []builderTemplateFailure `json:"failed"`
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

// templateItem returns one template of a library as responses carry it, to
// a caller that sees it as source. visible names the collections of the
// library the caller sees, all of them when nil; the template names only
// those that hold it. Who it is shared with is not set.
func templateItem(
	library *bapi.TemplateLibrary,
	template *bapi.LibraryTemplate,
	source string,
	visible map[string]bapi.Visibility,
) builderTemplateResponse {
	response := builderTemplateResponse{
		ID:          template.ID,
		Owner:       library.Owner,
		Source:      source,
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
		Shares:      nil,
	}

	if template.Public != nil {
		response.PublishedAt, response.PublishedBy = template.Public.At, template.Public.By
	}

	for i := range library.Collections {
		collection := &library.Collections[i]

		if _, seen := visible[collection.ID]; visible != nil && !seen {
			continue
		}

		if slices.Contains(collection.TemplateIDs, template.ID) {
			response.Collections = append(response.Collections, collection.ID)
		}
	}

	return response
}

// collectionItem returns one collection of a library as responses carry it,
// to a caller that sees it as source. Who it is shared with is not set.
func collectionItem(
	library *bapi.TemplateLibrary,
	collection *bapi.TemplateCollection,
	source string,
) builderTemplateCollectionResponse {
	response := builderTemplateCollectionResponse{
		ID:          collection.ID,
		Owner:       library.Owner,
		Source:      source,
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
		Shares:      nil,
	}

	if collection.Public != nil {
		response.PublishedAt, response.PublishedBy = collection.Public.At, collection.Public.By
	}

	return response
}

// ownTemplate returns one template of the caller's own library as responses
// carry it: with every collection that holds it, and who it is shared with.
func (b *builderAPI) ownTemplate(
	library *bapi.TemplateLibrary,
	template *bapi.LibraryTemplate,
	accounts map[string]func() (string, bool, error),
) (builderTemplateResponse, error) {
	shares, err := b.templateShares(template.Shares, accounts)
	if err != nil {
		return builderTemplateResponse{}, err
	}

	response := templateItem(library, template, builderTemplateOwn, nil)
	response.Shares = shares

	return response, nil
}

// ownCollection returns one collection of the caller's own library as
// responses carry it, with who it is shared with.
func (b *builderAPI) ownCollection(
	library *bapi.TemplateLibrary,
	collection *bapi.TemplateCollection,
	accounts map[string]func() (string, bool, error),
) (builderTemplateCollectionResponse, error) {
	shares, err := b.templateShares(collection.Shares, accounts)
	if err != nil {
		return builderTemplateCollectionResponse{}, err
	}

	response := collectionItem(library, collection, builderTemplateOwn)
	response.Shares = shares

	return response, nil
}

// preloadedCollections returns the collections the server read from its
// template files at start (see [bapi.Service.LoadServerTemplates]), as the
// listing gives them: each with its templates, of source "preloaded", with no
// owner and at version 1.
func (b *builderAPI) preloadedCollections() []builderPreloadedCollection {
	collections := b.drafts.ServerCollections()
	listed := make([]builderPreloadedCollection, 0, len(collections))
	etag := bapi.RevisionETag(builderTemplatePreloadedVersion)

	for i := range collections {
		collection := &collections[i]
		ids := make([]string, 0, len(collection.Templates))
		templates := make([]builderTemplateResponse, 0, len(collection.Templates))

		for j := range collection.Templates {
			template := &collection.Templates[j]

			ids = append(ids, template.ID)
			templates = append(templates, builderTemplateResponse{
				ID:          template.ID,
				Owner:       "",
				Source:      builderTemplatePreloaded,
				Name:        template.Name,
				Description: template.Description,
				Device:      template.Device,
				Version:     builderTemplatePreloadedVersion,
				ETag:        etag,
				Created:     time.Time{},
				Updated:     time.Time{},
				ServerWide:  false,
				PublishedAt: time.Time{},
				PublishedBy: "",
				Collections: []string{collection.ID},
				Shares:      nil,
			})
		}

		listed = append(listed, builderPreloadedCollection{
			Collection: builderTemplateCollectionResponse{
				ID:          collection.ID,
				Owner:       "",
				Source:      builderTemplatePreloaded,
				Name:        collection.Name,
				Description: collection.Description,
				TemplateIDs: ids,
				Version:     builderTemplatePreloadedVersion,
				ETag:        etag,
				Created:     time.Time{},
				Updated:     time.Time{},
				ServerWide:  false,
				PublishedAt: time.Time{},
				PublishedBy: "",
				Shares:      nil,
			},
			Templates: templates,
		})
	}

	return listed
}

// refusePreloaded fails a change of a library that names a template or a
// collection the server read from one of its template files: those are read
// only for everyone, and are changed by changing the file. The refusal is a
// 409 that names the item and says how to get a copy that can be changed.
func (b *builderAPI) refusePreloaded(templateIDs, collectionIDs []string) error {
	collections := b.drafts.ServerCollections()

	for i := range collections {
		collection := &collections[i]

		if slices.Contains(collectionIDs, collection.ID) {
			return weberror.NewWebError(
				nil,
				"collection %s (%q) is read from a template file of the phenix server and cannot be changed; "+
					"copy it to your library to change it",
				collection.ID, collection.Name,
			).SetStatus(http.StatusConflict)
		}

		for j := range collection.Templates {
			template := &collection.Templates[j]

			if slices.Contains(templateIDs, template.ID) {
				return weberror.NewWebError(
					nil,
					"template %s (%q) is read from a template file of the phenix server and cannot be changed; "+
						"copy it to your library to change it",
					template.ID, template.Name,
				).SetStatus(http.StatusConflict)
			}
		}
	}

	return nil
}

// listTemplates - GET /builder/templates.
//
// The answer holds the caller's own library first, which is the built-in
// templates until the caller changes it, then the items of other users'
// libraries the caller sees, by owner: those shared with it and those
// published to every user, read only. A library is read only when a hint
// record names it (see [bapi.Service.LibrarySources]): no listing reads every
// user's library. A record of the caller's this server cannot read is
// reported as damaged, with none of its items. The collections the server
// read from its template files are in "preloaded", for every caller.
func (b *builderAPI) listTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListTemplates")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder templates")
	if err != nil {
		return err
	}

	account := b.accountOnce(actor.user)

	canShare, err := b.canShare(actor, account)
	if err != nil {
		return weberror.NewWebError(err, "unable to list the templates").SetStatus(http.StatusInternalServerError)
	}

	response := builderTemplateLibraryResponse{
		Owner:       actor.user,
		Templates:   []builderTemplateResponse{},
		Collections: []builderTemplateCollectionResponse{},
		Preloaded:   b.preloadedCollections(),
		CanShare:    canShare,
		CanPublish:  builderBaseAllowed(actor.role, builderVerbUpdate) && builderTemplatesPublishAllowed(actor.role),
		Damaged:     false,
		Limits:      builderTemplateLibraryLimits(),
	}

	library, err := b.drafts.GetLibrary(r.Context(), actor.user)

	switch {
	case errors.Is(err, bapi.ErrCorrupt):
		plog.Error(plog.TypeSystem, "builder template library is unreadable", "user", actor.user, "err", err)

		response.Damaged = true
	case err != nil:
		return builderWebError(err, "unable to list the templates")
	default:
		if err := b.listOwnTemplates(&response, library); err != nil {
			return weberror.NewWebError(err, "unable to list the templates").SetStatus(http.StatusInternalServerError)
		}
	}

	if err := b.listOtherTemplates(r.Context(), actor, account, &response); err != nil {
		return weberror.NewWebError(err, "unable to list the templates").SetStatus(http.StatusInternalServerError)
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// listOwnTemplates adds the items of the caller's own library to the
// listing, each with who it is shared with.
func (b *builderAPI) listOwnTemplates(response *builderTemplateLibraryResponse, library *bapi.TemplateLibrary) error {
	accounts := map[string]func() (string, bool, error){}

	for i := range library.Templates {
		template, err := b.ownTemplate(library, &library.Templates[i], accounts)
		if err != nil {
			return err
		}

		response.Templates = append(response.Templates, template)
	}

	for i := range library.Collections {
		collection, err := b.ownCollection(library, &library.Collections[i], accounts)
		if err != nil {
			return err
		}

		response.Collections = append(response.Collections, collection)
	}

	return nil
}

// listOtherTemplates adds to the listing the items of other users' libraries
// the caller sees (see [bapi.TemplateLibrary.VisibleTo]), by owner name, in
// each library's order. Only the libraries a hint record names are read; one
// that is gone or that this server cannot read is left out.
func (b *builderAPI) listOtherTemplates(
	ctx context.Context,
	actor builderActor,
	account func() (string, bool, error),
	response *builderTemplateLibraryResponse,
) error {
	shared, public, err := b.drafts.LibrarySources(ctx, actor.user)
	if err != nil {
		return err
	}

	var (
		libraries []*bapi.TemplateLibrary
		read      = map[string]bool{}
	)

	for _, scope := range slices.Concat(shared, public) {
		if read[scope] {
			continue
		}

		read[scope] = true

		library, err := b.drafts.GetLibraryByKey(ctx, scope)

		switch {
		case errors.Is(err, bapi.ErrNotFound):
			continue
		case errors.Is(err, bapi.ErrCorrupt):
			plog.Warn(
				plog.TypeSystem, "skipping a builder template library that is unreadable",
				"user", actor.user, "library", scope, "err", err,
			)

			continue
		case err != nil:
			return err
		}

		libraries = append(libraries, library)
	}

	if len(libraries) == 0 {
		return nil
	}

	// Shares apply to the account they were made for.
	created, exists, err := account()
	if err != nil {
		return err
	}

	slices.SortFunc(libraries, func(a, b *bapi.TemplateLibrary) int { return strings.Compare(a.Owner, b.Owner) })

	for _, library := range libraries {
		templates, collections := library.VisibleTo(actor.user, created, exists)

		for i := range library.Templates {
			template := &library.Templates[i]

			if how, ok := templates[template.ID]; ok {
				response.Templates = append(response.Templates, templateItem(library, template, string(how), collections))
			}
		}

		for i := range library.Collections {
			collection := &library.Collections[i]

			if how, ok := collections[collection.ID]; ok {
				response.Collections = append(response.Collections, collectionItem(library, collection, string(how)))
			}
		}
	}

	return nil
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

	if err := b.refusePreloaded([]string{id}, nil); err != nil {
		return err
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

		return library.ReplaceTemplate(id, content)
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

	if err := b.refusePreloaded(request.TemplateIDs, nil); err != nil {
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

	if err := b.refusePreloaded(request.TemplateIDs, []string{id}); err != nil {
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

	if err := b.refusePreloaded(request.Templates, request.Collections); err != nil {
		return err
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

// builderTemplateFailures returns the items a change left unchanged as a
// response names them, each with the code of its reason: always a list.
func builderTemplateFailures(failed []bapi.LibraryFailure) []builderTemplateFailure {
	failures := make([]builderTemplateFailure, 0, len(failed))

	for _, failure := range failed {
		code := bdoc.CodeTemplateItemNotFound
		if failure.Reason == bapi.FailureTooMany {
			code = bdoc.CodeTemplateSharesTooMany
		}

		failures = append(failures, builderTemplateFailure{
			Kind: failure.Kind, ID: failure.ID, Reason: failure.Reason, Code: string(code),
		})
	}

	return failures
}

// builderTemplateLogIDs joins, for a log, the IDs a request named that can
// name an item. Any other was not found, and is not repeated.
func builderTemplateLogIDs(ids []string) string {
	kept := make([]string, 0, len(ids))

	for _, id := range ids {
		if bapi.ValidID(id) && !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}

	return strings.Join(kept, ",")
}

// builderDistinct returns values without repeats, in the order first given.
func builderDistinct(values []string) []string {
	kept := make([]string, 0, len(values))

	for _, value := range values {
		if !slices.Contains(kept, value) {
			kept = append(kept, value)
		}
	}

	return kept
}

// mayShareTemplates fails the request unless actor may share items of its
// library: that takes a user account, which a caller has no other reason to
// hold when authentication is off, on top of config update.
func (b *builderAPI) mayShareTemplates(actor builderActor, action string) error {
	canShare, err := b.canShare(actor, b.accountOnce(actor.user))
	if err != nil {
		return weberror.NewWebError(err, "unable to check who may share builder templates").
			SetStatus(http.StatusInternalServerError)
	}

	// The UI never offers sharing then: see the listing's canShare.
	if !canShare {
		return builderForbidden(actor, action+" without a user account")
	}

	return nil
}

// getTemplateShareCandidates - GET /builder/templates/candidates.
//
// Who the caller may share items of its library with: every account a share
// request would accept, whatever the caller's users permissions, sorted by
// username. It needs what sharing needs: config update and a user account.
func (b *builderAPI) getTemplateShareCandidates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetTemplateShareCandidates")

	const action = "listing who builder templates can be shared with"

	actor, err := builderAuthorize(r, builderVerbShare, action)
	if err != nil {
		return err
	}

	if err := b.mayShareTemplates(actor, action); err != nil {
		return err
	}

	users, err := b.listShareCandidates(func(user, created string) bool {
		return user != actor.user && bapi.ValidateShareUser(user) == nil && created != ""
	})
	if err != nil {
		return weberror.NewWebError(err, "unable to list who builder templates can be shared with").
			SetStatus(http.StatusInternalServerError)
	}

	return builderWriteJSON(w, http.StatusOK, "", builderShareCandidatesResponse{Users: users})
}

// builderTemplateShareErrors returns what is wrong with the users a share
// request of owner adds and removes, before any account is read: a name that
// cannot name a user, the owner, a user both added and removed, and more
// users added than an item may be shared with.
func builderTemplateShareErrors(owner string, add, remove []string) []builderShareError {
	var problems []builderShareError

	added := builderDistinct(add)

	if len(added) > bapi.MaxShares {
		problems = append(problems, builderShareError{User: "", Reason: builderShareTooMany})
	}

	for _, user := range added {
		reason := ""

		switch {
		case bapi.ValidateShareUser(user) != nil:
			reason = builderShareInvalidUser
		case user == owner:
			reason = builderShareOwner
		case slices.Contains(remove, user):
			reason = builderShareDuplicate
		}

		if reason != "" {
			problems = append(problems, builderShareError{User: user, Reason: reason})
		}
	}

	for _, user := range builderDistinct(remove) {
		if !slices.Contains(added, user) && bapi.ValidateShareUser(user) != nil {
			problems = append(problems, builderShareError{User: user, Reason: builderShareInvalidUser})
		}
	}

	return problems
}

// resolveTemplateGrants binds each user a share request adds to the account
// it names, which must exist. Refusals are logged: they may be probing for
// user names.
func (b *builderAPI) resolveTemplateGrants(
	actor builderActor,
	users []string,
) ([]bapi.TemplateShare, []builderShareError, error) {
	var (
		grants   = make([]bapi.TemplateShare, 0, len(users))
		problems []builderShareError
	)

	for _, user := range users {
		created, exists, err := b.accountCreated(user)
		if err != nil {
			return nil, nil, err
		}

		if exists {
			grants = append(grants, bapi.TemplateShare{User: user, UserCreated: created, GrantedAt: time.Time{}})

			continue
		}

		plog.Warn(
			plog.TypeSecurity,
			"builder template share names an unknown user",
			"user", actor.user,
			"recipient", user,
		)

		problems = append(problems, builderShareError{User: user, Reason: builderShareUnknownUser})
	}

	return grants, problems, nil
}

// shareTemplates - POST /builder/templates/{owner}/share.
//
// Adds users to, and removes users from, who the named templates and
// collections of the caller's library are shared with, read only. Only the
// owner shares, with config update and a user account. The request is a
// change of each list, not a new list, so it needs no entity tag. Every user
// added must have an account, which the share is bound to; when one is
// refused nothing changes. An item that would be shared with more than
// [bapi.MaxShares] users, and an ID the library does not hold, are answered
// in "failed"; the other items are changed.
func (b *builderAPI) shareTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderShareTemplates")

	const action = "sharing builder templates"

	actor, err := b.templateOwner(r, builderVerbShare, action)
	if err != nil {
		return err
	}

	if err := b.mayShareTemplates(actor, action); err != nil {
		return err
	}

	var request builderTemplateShareRequest

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	switch {
	case len(request.Templates)+len(request.Collections) == 0:
		return weberror.NewWebError(nil, "at least one template or collection is required").
			SetStatus(http.StatusBadRequest)
	case len(request.Add)+len(request.Remove) == 0:
		return weberror.NewWebError(nil, "at least one user to add or remove is required").
			SetStatus(http.StatusBadRequest)
	}

	if err := b.refusePreloaded(request.Templates, request.Collections); err != nil {
		return err
	}

	if problems := builderTemplateShareErrors(actor.user, request.Add, request.Remove); len(problems) != 0 {
		return builderWriteShareErrors(w, problems)
	}

	added := builderDistinct(request.Add)

	grants, problems, err := b.resolveTemplateGrants(actor, added)
	if err != nil {
		return weberror.NewWebError(err, "unable to share the templates").SetStatus(http.StatusInternalServerError)
	}

	if len(problems) != 0 {
		return builderWriteShareErrors(w, problems)
	}

	// The hints go first, so a share never exists without the hint that
	// lists the library to its recipient.
	if len(added) != 0 {
		if err := b.drafts.NoteShared(r.Context(), actor.user, added); err != nil {
			return builderTemplateError(w, err, "unable to share the templates")
		}
	}

	var (
		failed []bapi.LibraryFailure
		read   int64
	)

	library, err := b.changeOwnLibrary(r, actor, func(library *bapi.TemplateLibrary) error {
		read = library.Revision
		failed = library.Share(request.Templates, request.Collections, grants, request.Remove)

		return nil
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to share the templates")
	}

	if library.Revision != read {
		plog.Info(
			plog.TypeSecurity, "builder template sharing changed",
			"user", actor.user,
			"owner", actor.user,
			"templates", builderTemplateLogIDs(request.Templates),
			"collections", builderTemplateLogIDs(request.Collections),
			"added", strings.Join(added, ","),
			"removed", strings.Join(builderDistinct(request.Remove), ","),
		)
	}

	return builderWriteJSON(w, http.StatusOK, "", builderTemplateResult{Failed: builderTemplateFailures(failed)})
}

// publishTemplates - POST /builder/templates/{owner}/publish.
//
// Publishes the named templates and collections of a library to every user,
// or with "serverWide" false takes them back. Both need config update.
// Publishing is for the owner holding builder-templates publish: the owner
// without it is answered 403, anyone else 404. Taking an item back is for
// the owner, or for anyone holding builder-templates publish, of any
// owner's library; anyone else is answered 403, since a published item is
// seen by everyone. An ID the library does not hold is answered in "failed".
func (b *builderAPI) publishTemplates(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPublishTemplates")

	const action = "publishing builder templates"

	actor, err := builderAuthorize(r, builderVerbUpdate, action)
	if err != nil {
		return err
	}

	var request builderTemplatePublishRequest

	if err := builderDecodeLimit(w, r, &request, builderTemplateRequestBytes); err != nil {
		return err
	}

	switch {
	case request.ServerWide == nil:
		return weberror.NewWebError(nil, "serverWide is required").SetStatus(http.StatusBadRequest)
	case len(request.Templates)+len(request.Collections) == 0:
		return weberror.NewWebError(nil, "at least one template or collection is required").
			SetStatus(http.StatusBadRequest)
	}

	if err := b.refusePreloaded(request.Templates, request.Collections); err != nil {
		return err
	}

	var (
		owner      = mux.Vars(r)["owner"]
		serverWide = *request.ServerWide
		mayPublish = builderTemplatesPublishAllowed(actor.role)
	)

	switch {
	case owner != actor.user && (serverWide || bapi.ValidateShareUser(owner) != nil):
		plog.Warn(
			plog.TypeSecurity,
			"builder template library request for another user not allowed",
			"user", actor.user,
			"owner", owner,
			"action", action,
		)

		return builderNotFound("template library", owner)
	case owner != actor.user && !mayPublish:
		return builderForbidden(actor, "taking another user's builder templates back from every user")
	case serverWide && !mayPublish:
		return builderForbidden(actor, action+" to every user")
	}

	// The hint goes first, so an item is never published without the hint
	// that lists its library to every user.
	if serverWide {
		if err := b.drafts.NotePublic(r.Context(), owner); err != nil {
			return builderTemplateError(w, err, "unable to publish the templates")
		}
	}

	var (
		failed []bapi.LibraryFailure
		read   int64
	)

	library, err := b.drafts.UpdateLibrary(r.Context(), owner, actor.user, func(library *bapi.TemplateLibrary) error {
		read = library.Revision
		failed = library.SetPublic(request.Templates, request.Collections, serverWide, actor.user)

		return nil
	})
	if err != nil {
		return builderTemplateError(w, err, "unable to publish the templates")
	}

	if library.Revision != read {
		plog.Info(
			plog.TypeSecurity, "builder template publication changed",
			"user", actor.user,
			"owner", owner,
			"templates", builderTemplateLogIDs(request.Templates),
			"collections", builderTemplateLogIDs(request.Collections),
			"serverWide", serverWide,
		)
	}

	return builderWriteJSON(w, http.StatusOK, "", builderTemplateResult{Failed: builderTemplateFailures(failed)})
}
