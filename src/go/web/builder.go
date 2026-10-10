package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	putil "phenix/util"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// Builder is the HTTP API of the Vue Flow builder. Draft autosave lives in
// the generic record store (see [phenix/api/builder]). Only an explicit
// publish creates or updates configs. Only
// DELETE /builder/documents/{document} deletes them.
//
// Authorization has two layers:
//
//   - Every request needs the base config permission of its operation, so
//     builder access can never exceed the config access of a user.
//   - A request that touches a draft also needs access to that draft. The
//     access comes from one of three sources, and the strongest source wins:
//     owning the draft, a share from its owner that names the caller, or the
//     "builder-drafts" permission of the same verb for the
//     "{owner}/{draftID}" resource name. A share grants view (list and get)
//     or edit (also update). It never grants delete or a change to who the
//     draft is shared with.
//
// When no source grants a caller access to a draft, the caller gets 404,
// the same as for a draft that does not exist. Thus the API never discloses
// that a draft exists. A caller who can see a draft but may not do the
// operation gets 403.
const (
	// builderDraftsResource is the RBAC resource authorizing operations on
	// drafts owned by another user. Resource names are "{owner}/{draftID}".
	builderDraftsResource = "builder-drafts"

	// builderCurrentSnapshot is the snapshot path segment that names
	// whichever snapshot the draft cursor currently points at.
	builderCurrentSnapshot = "current"

	// builderEnvelopeBytes is the space a Builder request body may use in
	// addition to the document it carries: titles, summaries, and JSON string
	// escaping.
	builderEnvelopeBytes = 1 << 20

	// builderMaxRequestBytes bounds every Builder request body.
	// [bapi.MaxDocumentBytes] also bounds the payloads within the envelope.
	builderMaxRequestBytes = bapi.MaxDocumentBytes + builderEnvelopeBytes
)

// builderVerb is one of the operations Builder authorizes. It exists
// so the base config permission and the cross-user draft permission of an
// operation are always derived from the same value.
type builderVerb string

const (
	builderVerbList   builderVerb = "list"
	builderVerbGet    builderVerb = "get"
	builderVerbCreate builderVerb = "create"
	builderVerbUpdate builderVerb = "update"
	builderVerbDelete builderVerb = "delete"
	// builderVerbShare changes who a draft is shared with. Its base
	// permission is config update. Only the draft owner may do it.
	builderVerbShare builderVerb = "share"
)

// builderLevel orders what a caller may do with a draft.
type builderLevel int

const (
	builderLevelNone builderLevel = iota
	builderLevelView
	builderLevelEdit
	builderLevelOwner
)

// Where access to another user's draft comes from: a share naming the caller,
// or the caller's role. A caller holding both is reported as "share".
const (
	builderViaShare = "share"
	builderViaRole  = "role"
)

// builderAccessOwner is how responses report a caller's access to its own
// draft. Responses report access through a share as the access of the share.
const builderAccessOwner = "owner"

// builderRoleGrants is what the "builder-drafts" permission of a role
// grants on one draft of another user.
type builderRoleGrants struct {
	canList, canGet, canUpdate, canDelete bool
}

// builderAccess is a caller's access to one draft (see [builderAPI]).
// Level is the strongest of what its ownership, a share and its role grant.
// Via is where access to the draft of another user comes from. Stale is true
// when a share names the caller but no longer matches its account. Such a
// share grants nothing.
type builderAccess struct {
	level builderLevel
	via   string
	rbac  builderRoleGrants
	stale bool
}

// builderAPI serves the Builder routes. Config access, publication effects
// and the Builder file directories are injected, so tests can run the
// handlers without a real store or the directories of the server.
type builderAPI struct {
	drafts      *bapi.Service
	listConfigs func(kind string) (store.Configs, error)
	getConfig   func(name string) (*store.Config, error)
	publish     builderPublishOps
	// documentFiles returns the directory that holds the Builder files that
	// document references name. It also returns the directories below it that
	// the server never reads these files from (see [bapi.ReadDocumentFile]).
	documentFiles func() (string, []string)
	// templateFiles is the directory whose template files the server reads
	// once, at start, as its template collections (see
	// [bapi.Service.LoadServerTemplates]). "" reads none.
	templateFiles string
	// disks lists the disk images of this server, as GET /disks lists them.
	// Routes that compare a document with the disk images use it: package
	// resolve (see [builderAPI.resolvePackage]), the dry run of a publication
	// and the preflight disks check. It reuses a listing for a short time.
	disks *builderDiskLister
	// appNames lists the apps this server runs, which a package's diagram
	// may need.
	appNames func() []string
	// preflight is where the preflight checks of a draft read the cluster
	// and the server from (see [builderAPI.preflightDraft]).
	preflight builderPreflightSources
}

// builderOption configures a [builderAPI].
type builderOption func(*builderAPI)

// builderActor is the authenticated identity and role of a request.
type builderActor struct {
	user string
	role rbac.Role
}

// newBuilderAPI returns an API bound to the phenix config store, with the
// given options applied.
func newBuilderAPI(opts ...builderOption) (*builderAPI, error) {
	service, err := bapi.New()
	if err != nil {
		return nil, err
	}

	api := &builderAPI{
		drafts:        service,
		listConfigs:   config.List,
		getConfig:     func(name string) (*store.Config, error) { return config.Get(name, false) },
		publish:       newBuilderPublishOps(),
		documentFiles: builderDocumentFiles,
		templateFiles: common.BuilderTemplatesDir(),
		disks:         newBuilderDiskLister(builderDiskImages, time.Now),
		appNames:      builderAppNames,
		preflight:     defaultBuilderPreflightSources(),
	}

	for _, opt := range opts {
		opt(api)
	}

	api.cleanupStorage()

	// Read the template collections of the server here, one time. A change to
	// the directory shows at the next start.
	api.drafts.LoadServerTemplates(context.Background(), api.templateFiles)

	return api, nil
}

// cleanupStorage removes interrupted chunk writes, published documents no
// topology references, and icon records of the per-user layout of earlier
// builds. Document cleanup runs only after a complete topology listing in
// which all references decode. Otherwise, the deletion of an apparently
// orphaned document could break a topology that is not in the reference
// set. A reference that names only a file names no stored document, and
// keeps none.
func (b *builderAPI) cleanupStorage() {
	topologies, err := b.listConfigs(builderKindTopology)
	if err != nil {
		plog.Error(plog.TypeSystem, "listing builder topology references for cleanup", "err", err)
	} else {
		live := make([]bapi.LiveDocument, 0)
		complete := true

		for _, topology := range topologies {
			value, ok := topology.Metadata.Annotations[bapi.DocumentAnnotation]
			if !ok {
				continue
			}

			reference, decodeErr := bapi.DecodeReference(value)
			if decodeErr != nil {
				complete = false
				plog.Error(
					plog.TypeSystem,
					"skipping builder document cleanup because a topology reference is invalid",
					"topology", topology.FullName(),
					"err", decodeErr,
				)

				break
			}

			if id := reference.StoredID(topology.Metadata.Name); id != "" {
				live = append(live, bapi.LiveDocument{Target: topology.Metadata.Name, ID: id})
			}
		}

		if complete {
			if _, cleanupErr := b.drafts.CleanupOrphanedDocuments(
				context.Background(),
				live,
			); cleanupErr != nil {
				plog.Error(plog.TypeSystem, "cleaning orphaned builder documents", "err", cleanupErr)
			}
		}
	}

	if _, err := b.drafts.CleanupOrphanedChunks(context.Background()); err != nil {
		plog.Error(plog.TypeSystem, "cleaning orphaned builder chunks", "err", err)
	}

	removed, err := b.drafts.CleanupLegacyIcons(context.Background())
	if err != nil {
		plog.Error(plog.TypeSystem, "removing builder icon records of the per-user layout", "err", err)
	}

	if removed > 0 {
		plog.Info(plog.TypeSystem, "removed builder icon records of the per-user layout", "records", removed)
	}
}

// withBuilderService sets the draft service the handlers persist to.
func withBuilderService(service *bapi.Service) builderOption {
	return func(api *builderAPI) { api.drafts = service }
}

// withBuilderConfigs sets the read-only config accessors used by the source
// listing and generation endpoints.
func withBuilderConfigs(
	list func(kind string) (store.Configs, error),
	get func(name string) (*store.Config, error),
) builderOption {
	return func(api *builderAPI) {
		api.listConfigs = list
		api.getConfig = get
	}
}

// withBuilderPublishOps replaces explicit publication effects in tests.
func withBuilderPublishOps(ops builderPublishOps) builderOption {
	return func(api *builderAPI) { api.publish = ops }
}

// builderDocumentFiles is where this server reads Builder files from: the
// phenix base directory (--base-dir.phenix). It excludes the directory that
// holds the mounted VM file systems, because that directory holds guest
// files and can hang a read. The store and the settings file (with its
// signing key) are outside the base directory. The function reads both
// directories when the server reads a file, so they are the directories the
// server started with.
func builderDocumentFiles() (string, []string) {
	return common.PhenixBase, []string{common.MountDir()}
}

// withBuilderTemplateFiles sets the directory whose template files the
// server reads at start as its template collections. "" reads none.
func withBuilderTemplateFiles(directory string) builderOption {
	return func(api *builderAPI) { api.templateFiles = directory }
}

// withBuilderDocumentFiles sets the directory that the server reads Builder
// files from, and the directories below it that it never reads them from.
func withBuilderDocumentFiles(root string, excluded ...string) builderOption {
	return func(api *builderAPI) {
		api.documentFiles = func() (string, []string) { return root, excluded }
	}
}

// registerBuilderRoutes adds the Builder routes to the given API router.
// The router must already have a NotFoundHandler. This function adds the
// Builder response headers to that handler (see [builderResponseHeaders]).
func registerBuilderRoutes(router *mux.Router, opts ...builderOption) error {
	api, err := newBuilderAPI(opts...)
	if err != nil {
		return err
	}

	api.routes(router)

	return nil
}

// builderRequestActor returns the authenticated identity of a request. The
// second return value is false when the request carries no usable identity,
// which the auth middleware normally prevents.
func builderRequestActor(r *http.Request) (builderActor, bool) {
	ctx := r.Context()

	actor := builderActor{
		user: middleware.UserFromContext(ctx),
		role: middleware.RoleFromContext(ctx),
	}

	if actor.user == "" || actor.role.Spec == nil {
		return actor, false
	}

	return actor, true
}

// builderAuthorize returns the request's actor when it holds the base
// config permission of verb, and otherwise the 403 of [builderForbidden].
func builderAuthorize(r *http.Request, verb builderVerb, action string) (builderActor, error) {
	actor, ok := builderRequestActor(r)
	if !ok || !builderBaseAllowed(actor.role, verb) {
		return actor, builderForbidden(actor, action)
	}

	return actor, nil
}

// builderBaseAllowed reports whether the role holds the base config
// permission every Builder operation of this kind requires. Each check is a
// literal call so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records it.
func builderBaseAllowed(role rbac.Role, verb builderVerb, names ...string) bool {
	switch verb {
	case builderVerbList:
		return role.Allowed("configs", "list", names...)
	case builderVerbGet:
		return role.Allowed("configs", "get", names...)
	case builderVerbCreate:
		return role.Allowed("configs", "create", names...)
	case builderVerbUpdate, builderVerbShare:
		return role.Allowed("configs", "update", names...)
	case builderVerbDelete:
		return role.Allowed("configs", "delete", names...)
	}

	return false
}

// builderTemplatesPublishAllowed reports whether the role may publish
// template library items to every user, and take any user's published item
// back. The check is a literal call so the RBAC policy generator records it.
func builderTemplatesPublishAllowed(role rbac.Role) bool {
	return role.Allowed("builder-templates", "publish")
}

// builderIconsUpdateAllowed reports whether the role may rename any icon of
// the icon library, whoever uploaded it. The check is a literal call so the
// RBAC policy generator records it.
func builderIconsUpdateAllowed(role rbac.Role) bool {
	return role.Allowed("builder-icons", "update")
}

// builderIconsDeleteAllowed reports whether the role may delete any icon of
// the icon library, whoever uploaded it. The check is a literal call so the
// RBAC policy generator records it.
func builderIconsDeleteAllowed(role rbac.Role) bool {
	return role.Allowed("builder-icons", "delete")
}

// builderCrossUserAllowed reports whether the role may operate on a draft
// owned by another user. Create and share are missing on purpose. A user
// always creates a draft for itself, never for another user. Only the owner
// changes who a draft is shared with.
func builderCrossUserAllowed(role rbac.Role, verb builderVerb, names ...string) bool {
	switch verb {
	case builderVerbList:
		return role.Allowed("builder-drafts", "list", names...)
	case builderVerbGet:
		return role.Allowed("builder-drafts", "get", names...)
	case builderVerbUpdate:
		return role.Allowed("builder-drafts", "update", names...)
	case builderVerbDelete:
		return role.Allowed("builder-drafts", "delete", names...)
	case builderVerbCreate, builderVerbShare:
		return false
	}

	return false
}

// builderRoleGrantsFor returns what the role grants on the draft of
// another user with the given resource name.
func builderRoleGrantsFor(role rbac.Role, name string) builderRoleGrants {
	return builderRoleGrants{
		canList:   builderCrossUserAllowed(role, builderVerbList, name),
		canGet:    builderCrossUserAllowed(role, builderVerbGet, name),
		canUpdate: builderCrossUserAllowed(role, builderVerbUpdate, name),
		canDelete: builderCrossUserAllowed(role, builderVerbDelete, name),
	}
}

// builderHoldsRoleGrants reports whether the role grants anything on any
// draft of another user, so callers can skip the checks by name otherwise.
func builderHoldsRoleGrants(role rbac.Role) bool {
	return builderCrossUserAllowed(role, builderVerbList) ||
		builderCrossUserAllowed(role, builderVerbGet) ||
		builderCrossUserAllowed(role, builderVerbUpdate) ||
		builderCrossUserAllowed(role, builderVerbDelete)
}

// allowed reports whether the grants include the verb.
func (g builderRoleGrants) allowed(verb builderVerb) bool {
	switch verb {
	case builderVerbList:
		return g.canList
	case builderVerbGet:
		return g.canGet
	case builderVerbUpdate:
		return g.canUpdate
	case builderVerbDelete:
		return g.canDelete
	case builderVerbCreate, builderVerbShare:
		return false
	}

	return false
}

// visible reports whether the grants let the caller see the draft when asking
// to perform the verb.
func (g builderRoleGrants) visible(verb builderVerb) bool {
	return g.canList || g.canGet || g.allowed(verb)
}

// level returns the level the grants amount to: update is edit, get or list
// is view.
func (g builderRoleGrants) level() builderLevel {
	switch {
	case g.canUpdate:
		return builderLevelEdit
	case g.canGet || g.canList:
		return builderLevelView
	}

	return builderLevelNone
}

// owner reports whether the caller owns the draft.
func (a builderAccess) owner() bool {
	return a.level == builderLevelOwner
}

// visible reports whether the caller may know that the draft exists when it
// asks to do the verb. This is true when the caller owns the draft, when a
// share names the caller, or when its role grants list, get or the verb.
func (a builderAccess) visible(verb builderVerb) bool {
	return a.owner() || a.via == builderViaShare || a.rbac.visible(verb)
}

// allows reports whether the caller may do the verb. A share grants list
// and get. An edit share also grants update. A share never grants delete or
// share.
func (a builderAccess) allows(verb builderVerb) bool {
	if a.owner() {
		return true
	}

	switch verb {
	case builderVerbList, builderVerbGet:
		return a.via == builderViaShare || a.rbac.allowed(verb)
	case builderVerbUpdate:
		return a.level >= builderLevelEdit
	case builderVerbDelete:
		return a.rbac.canDelete
	case builderVerbCreate, builderVerbShare:
		return false
	}

	return false
}

// denial names why the caller, who may see the draft, may not perform the
// verb, for the security log.
func (a builderAccess) denial(verb builderVerb) string {
	switch {
	case verb == builderVerbShare,
		verb == builderVerbDelete && a.via == builderViaShare:
		return "not-owner"
	case verb == builderVerbUpdate && a.via == builderViaShare:
		return "view-only"
	}

	return "rbac"
}

// name returns the access as responses report it: "owner", "edit", "view",
// or "" for none.
func (a builderAccess) name() string {
	switch a.level {
	case builderLevelOwner:
		return builderAccessOwner
	case builderLevelEdit:
		return string(bapi.ShareEdit)
	case builderLevelView:
		return string(bapi.ShareView)
	case builderLevelNone:
		return ""
	}

	return ""
}

// builderDraftAccess returns the access of the caller to a draft, given
// what its role grants on it. It reads the account of the caller, through
// account, only when a share names the caller. A share applies only while
// the account it names still exists. Thus a share never passes to a new
// account with the same name.
func builderDraftAccess(
	actor builderActor,
	meta *bapi.DraftMetadata,
	grants builderRoleGrants,
	account func() (string, bool, error),
) (builderAccess, error) {
	if meta.Owner == actor.user {
		return builderAccess{
			level: builderLevelOwner,
			via:   "",
			rbac:  builderRoleGrants{canList: false, canGet: false, canUpdate: false, canDelete: false},
			stale: false,
		}, nil
	}

	access := builderAccess{level: grants.level(), via: "", rbac: grants, stale: false}

	if grants.canList || grants.canGet || grants.canUpdate || grants.canDelete {
		access.via = builderViaRole
	}

	entry := meta.ShareFor(actor.user)
	if entry == nil {
		return access, nil
	}

	created, exists, err := account()
	if err != nil {
		return access, err
	}

	if !exists || created != entry.UserCreated {
		access.stale = true

		return access, nil
	}

	access.via = builderViaShare

	share := builderLevelView
	if entry.Access == bapi.ShareEdit {
		share = builderLevelEdit
	}

	access.level = max(access.level, share)

	return access, nil
}

// accountCreated returns metadata.created of the User config of the named
// user, and whether the user has an account a share can be bound to. A name
// that cannot name a config, a missing config and one without a creation
// time are all no account.
func (b *builderAPI) accountCreated(name string) (string, bool, error) {
	if name == "" || strings.Contains(name, "/") {
		return "", false, nil
	}

	account, err := b.getConfig("User/" + name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("reading the account of %s: %w", name, err)
	}

	if account.Metadata.Created == "" {
		return "", false, nil
	}

	return account.Metadata.Created, true, nil
}

// accountOnce returns [builderAPI.accountCreated] for the named user. It
// reads the account at most once, however often it is called.
func (b *builderAPI) accountOnce(name string) func() (string, bool, error) {
	var (
		read    bool
		created string
		exists  bool
		err     error
	)

	return func() (string, bool, error) {
		if !read {
			created, exists, err = b.accountCreated(name)
			read = true
		}

		return created, exists, err
	}
}

// canShare reports whether the caller, the owner of a draft, may change who
// the draft is shared with. This needs config update and a user account.
// When authentication is off, the caller has no other reason to hold an
// account.
func (b *builderAPI) canShare(actor builderActor, account func() (string, bool, error)) (bool, error) {
	if !builderBaseAllowed(actor.role, builderVerbShare) {
		return false, nil
	}

	_, exists, err := account()

	return exists, err
}

// builderDraftName is the RBAC resource name of a draft.
func builderDraftName(owner, draftID string) string {
	return owner + "/" + draftID
}

// builderForbidden returns the 403 of every failed permission check. The
// action is a short description of the attempted operation. The 403 never
// includes request bodies.
func builderForbidden(actor builderActor, action string) *weberror.WebError {
	plog.Warn(
		plog.TypeSecurity,
		"builder request not allowed",
		"user",
		actor.user,
		"action",
		action,
	)

	return builderNotAllowed(actor, action)
}

// builderNotAllowed returns the 403 of [builderForbidden] without
// logging it, for callers that log the refusal themselves.
func builderNotAllowed(actor builderActor, action string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s not allowed for %s", action, actor.user).
		SetStatus(http.StatusForbidden)
}

// builderWarnDenied logs a refused request for the draft of another user.
// The reason identifies the refusal:
//
//   - "no-access" or "stale-share": the caller may not see the draft. The
//     answer is 404.
//   - "view-only", "not-owner" or "rbac": the caller may see the draft but
//     may not change it this way. The answer is 403.
func builderWarnDenied(actor builderActor, owner, action, reason string) {
	plog.Warn(
		plog.TypeSecurity,
		"builder cross-user draft request not allowed",
		"user",
		actor.user,
		"owner",
		owner,
		"action",
		action,
		"reason",
		reason,
	)
}

// builderNotFound returns the 404 for a missing resource and for a
// cross-user request that the caller may not make. Thus the client cannot
// tell the two apart.
func builderNotFound(kind, name string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s %s not found", kind, name).
		SetStatus(http.StatusNotFound)
}

// builderHandler is the handler of a Builder route: [weberror.ErrorHandler]
// answers its errors, as it answers every route's, after
// [builderCodedError] gives each its code and issues.
func builderHandler(handler func(http.ResponseWriter, *http.Request) error) weberror.ErrorHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		return builderCodedError(handler(w, r))
	}
}

// builderCodedError gives the error of a Builder route the code of its
// failure (see [bdoc.Code]), unless the route gave it one. It also attaches
// the issues of the error: those of a package or a document that does not
// validate, or of what only publishing refuses (see [bdoc.ErrorIssues]).
// A write refused for lack of space gets 507 and its code, whatever error
// carries it. It does not change any other error that is not a
// [weberror.WebError]. Such an error gets 500 and no body.
func builderCodedError(err error) error {
	if err == nil {
		return nil
	}

	web := &weberror.WebError{} //nolint:exhaustruct // filled by errors.As

	if !errors.As(err, &web) {
		if errors.Is(err, store.ErrNoSpace) {
			return weberror.NewWebError(err, "%s", store.ErrNoSpace.Error()).
				SetStatus(http.StatusInsufficientStorage).WithCode(string(bdoc.CodeServerStorageFull))
		}

		return err
	}

	issues := bdoc.ErrorIssues(err)

	if web.Code == "" {
		web.Code = string(builderErrorCode(err, web.Status, issues))
	}

	if web.Issues == nil && len(issues) > 0 {
		web.Issues = issues
	}

	return err
}

// builderErrorCode is the failure code for a Builder route that names no
// code itself. It picks the first that applies:
//
//   - a write refused for lack of space
//   - a package that does not decode or validate
//   - a document that does not validate
//   - a template file that does not validate
//   - what only publishing refuses (any other error that holds issues)
//   - the kind of failure that the status gives
func builderErrorCode(err error, status int, issues []bdoc.Issue) bdoc.Code {
	var templateFile *bdoc.TemplateFileError

	switch {
	case errors.Is(err, store.ErrNoSpace):
		return bdoc.CodeServerStorageFull
	case errors.Is(err, bdoc.ErrInvalidPackage):
		return bdoc.CodePackageInvalid
	case errors.Is(err, bdoc.ErrInvalidDocument):
		return bdoc.CodeDocumentInvalid
	case errors.As(err, &templateFile):
		return bdoc.CodeTemplateFileInvalid
	case len(issues) > 0:
		return bdoc.CodePublishBlocked
	}

	switch status {
	case http.StatusForbidden:
		return bdoc.CodeRequestForbidden
	case http.StatusNotFound:
		return bdoc.CodeRequestNotFound
	case http.StatusConflict:
		return bdoc.CodeRequestConflict
	case http.StatusPreconditionFailed:
		return bdoc.CodeRequestStale
	case http.StatusRequestEntityTooLarge:
		return bdoc.CodeRequestTooLarge
	case http.StatusUnprocessableEntity:
		return bdoc.CodeRequestUnprocessable
	case http.StatusServiceUnavailable:
		return bdoc.CodeServerBusy
	case http.StatusInsufficientStorage:
		return bdoc.CodeServerStorageFull
	}

	if status >= http.StatusInternalServerError {
		return bdoc.CodeServerError
	}

	return bdoc.CodeRequestInvalid
}

// builderWebError maps a [phenix/api/builder] error to its HTTP status. The
// caller must handle cleanup failures before it calls this function,
// because they follow a durable, successful mutation. For a write refused
// because etcd is out of space, [weberror.ErrorHandler] sends 507.
func builderWebError(err error, format string, args ...any) *weberror.WebError {
	webErr := weberror.NewWebError(err, format, args...)

	var conflict *bapi.ConflictError

	switch {
	case errors.Is(err, bapi.ErrNotFound):
		return webErr.SetStatus(http.StatusNotFound)
	case errors.As(err, &conflict) && conflict.Kind == bapi.KindDraft:
		// The draft changed between the handler's read and the write.
		return webErr.SetStatus(http.StatusConflict).WithCode(string(bdoc.CodeDraftConflict))
	case errors.Is(err, bapi.ErrConflict):
		return webErr.SetStatus(http.StatusConflict)
	case errors.Is(err, bapi.ErrTooLarge):
		return webErr.SetStatus(http.StatusRequestEntityTooLarge)
	case errors.Is(err, bapi.ErrInvalid):
		return webErr.SetStatus(http.StatusUnprocessableEntity)
	case errors.Is(err, bapi.ErrBusy):
		return webErr.SetStatus(http.StatusServiceUnavailable)
	}

	return webErr.SetStatus(http.StatusInternalServerError)
}

// builderMutation resolves the pair a draft mutation returns.
//
// A mutation can complete its durable write but fail to remove the content
// it replaced. It then returns its updated metadata and an error that
// matches [bapi.ErrCleanup]. The draft is already at its new revision. A
// failed request would only make the client retry with a stale entity tag.
// Thus the caller gets the updated metadata, and a warning reports the
// cleanup failure.
//
// A mutation that returns no result, or any error that is not a cleanup
// failure, is mapped normally. Nothing else is caught.
func builderMutation[T any](
	w http.ResponseWriter,
	result *T,
	err error,
	operation, user, format string,
	args ...any,
) (*T, error) {
	if err == nil {
		return result, nil
	}

	if result == nil || !errors.Is(err, bapi.ErrCleanup) {
		return nil, builderWebError(err, format, args...)
	}

	builderWarnCleanup(w, err, operation, user)

	return result, nil
}

// builderWarnCleanup reports a cleanup failure that followed a durable,
// successful mutation. The request still succeeded, so it does not fail.
// But the failure is never dropped silently. The function logs it with its
// cause, and adds a warning to the response. The warning names only the
// operation, so the client learns nothing about the store.
func builderWarnCleanup(w http.ResponseWriter, err error, operation, user string) {
	plog.Error(
		plog.TypeSystem,
		"builder cleanup failed after a successful mutation",
		"operation",
		operation,
		"user",
		user,
		"err",
		err,
	)

	w.Header().Add(
		"Warning",
		fmt.Sprintf("199 phenix %q", operation+" succeeded but removing superseded content failed"),
	)
}

// builderIfMatch returns the entity tag a mutation must match. Every
// mutation after creation requires exactly one strong, quoted tag. It
// rejects wildcards and weak tags, so a stale client can never overwrite a
// draft it has not seen.
func builderIfMatch(r *http.Request) (string, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))

	if value == "" {
		return "", weberror.NewWebError(nil, "an If-Match header is required for this request").
			SetStatus(http.StatusBadRequest)
	}

	valid := len(value) > 2 &&
		strings.HasPrefix(value, `"`) &&
		strings.HasSuffix(value, `"`) &&
		!strings.Contains(value[1:len(value)-1], `"`)

	if !valid {
		return "", weberror.NewWebError(nil, "the If-Match header must be a single quoted entity tag").
			SetStatus(http.StatusBadRequest)
	}

	return value, nil
}

// builderCheckIfMatch fails the request when the caller's entity tag does
// not name the revision the draft is currently at.
func builderCheckIfMatch(ifMatch string, meta *bapi.DraftMetadata) error {
	if ifMatch == meta.ETag() {
		return nil
	}

	return weberror.NewWebError(nil, "draft %s has changed since it was last read", meta.ID).
		SetStatus(http.StatusPreconditionFailed).WithCode(string(bdoc.CodeDraftStale))
}

// builderDecode strictly decodes a JSON request body into target. It
// rejects unknown fields, trailing content, and bodies larger than
// [builderMaxRequestBytes]. It never logs the body.
func builderDecode(w http.ResponseWriter, r *http.Request, target any) error {
	return builderDecodeLimit(w, r, target, builderMaxRequestBytes)
}

// builderDecodeLimit is [builderDecode] for bodies of at most limit
// bytes.
func builderDecodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	err := putil.DecodeJSONStrict(http.MaxBytesReader(w, r.Body, limit), target)
	if err == nil {
		return nil
	}

	var tooLarge *http.MaxBytesError

	switch {
	case errors.As(err, &tooLarge):
		return weberror.NewWebError(nil, "request body is larger than %d bytes", limit).
			SetStatus(http.StatusRequestEntityTooLarge)
	case errors.Is(err, putil.ErrTrailingJSON):
		return weberror.NewWebError(nil, "request body carries more than one JSON value").
			SetStatus(http.StatusBadRequest)
	default:
		return weberror.NewWebError(err, "request body is not a valid Builder request").
			SetStatus(http.StatusBadRequest)
	}
}

// builderDocumentBytes returns the document bytes of a request. It rejects
// only a missing document. [phenix/api/builder] decodes, validates, and
// canonicalizes the document, and it alone decides what the store may hold.
func builderDocumentBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, weberror.NewWebError(nil, "a builder document is required").
			SetStatus(http.StatusBadRequest)
	}

	return raw, nil
}

// builderWriteJSON writes a JSON response, optionally tagged with the
// entity tag of the draft it represents.
func builderWriteJSON(w http.ResponseWriter, status int, etag string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return weberror.NewWebError(err, "unable to encode the Builder response").
			SetStatus(http.StatusInternalServerError)
	}

	if etag != "" {
		w.Header().Set("ETag", etag)
	}

	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(status)

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// What [builderResponseHeaders] goes by, and what it sets.
const (
	// builderAPIPrefix is the path [Start] mounts the API router at.
	builderAPIPrefix = "/api/v1"

	// builderRoutePrefix starts the path, below the API prefix, of every
	// Builder route but the schema route.
	builderRoutePrefix = "/builder/"

	// builderSchemaPath is the path of the Builder's schema route.
	builderSchemaPath = "/schemas/builder/v1"

	// builderTemplateSchemaPath is the path of the schema route of the
	// template file format.
	builderTemplateSchemaPath = "/schemas/builder/templates/v1"

	// builderPackageSchemaPath is the path of the schema route of the
	// package format.
	builderPackageSchemaPath = "/schemas/builder/package/v1"

	// builderIconsPath is the path of the icon library, whose routes are
	// this path and the paths below it.
	builderIconsPath = builderRoutePrefix + "icons"

	// builderIconPolicy is the Content-Security-Policy of the icon
	// library's responses: load nothing, run nothing, and be framed nowhere.
	builderIconPolicy = "default-src 'none'; frame-ancestors 'none'"
)

// builderResponseHeaders sets the security headers of the responses to
// requests under the Builder paths. These are every path below
// /api/v1/builder/, and the schema routes of the document, template file and
// package formats. It uses the request path, not the route that matched,
// and does not change other paths. [builderAPI.routes] makes it a middleware
// of the API router, so it also covers the answers of the middleware after
// it. It also wraps the router handlers for a request that matches no route,
// or no method of a route. No middleware runs for such a request.
//
// Every response under the Builder paths, whatever its status, tells the
// browser not to guess a content type. Each response is JSON and says so.
// The icon library responses carry images that people uploaded, as base64
// inside that JSON. Thus they also carry a Content-Security-Policy that lets
// a browser load, run and frame nothing, if it ever shows one as a page.
func builderResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if builderPath(r.URL.Path) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}

		if path, ok := strings.CutPrefix(r.URL.Path, builderAPIPrefix); ok &&
			(path == builderIconsPath || strings.HasPrefix(path, builderIconsPath+"/")) {
			w.Header().Set("Content-Security-Policy", builderIconPolicy)
		}

		next.ServeHTTP(w, r)
	})
}

// builderPath reports whether the request path is under the Builder paths:
// below /api/v1/builder/, or one of the schema routes of the document,
// template file and package formats.
func builderPath(path string) bool {
	rest, ok := strings.CutPrefix(path, builderAPIPrefix)

	return ok && (strings.HasPrefix(rest, builderRoutePrefix) || rest == builderSchemaPath ||
		rest == builderTemplateSchemaPath || rest == builderPackageSchemaPath)
}

// builderMethodNotAllowed returns the handler for a request that matches
// the path of a route but none of its methods. It is for a router that has
// no such handler. Under the Builder paths, it answers 405. Allow lists the
// methods that router takes for the path, and the body has the refusal
// code, as every Builder error has. For other paths, it answers with the
// status alone, as the router does.
func builderMethodNotAllowed(router *mux.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !builderPath(r.URL.Path) {
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		if allowed := builderAllowedMethods(router, r); len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}

		refusal := weberror.NewWebError(nil, "the %s method is not allowed for this path", r.Method).
			WithCode(string(bdoc.CodeRequestMethodNotAllowed))

		_ = builderWriteJSON(w, http.StatusMethodNotAllowed, "", refusal)
	}
}

// builderAllowedMethods returns the methods router has a route for at the
// path of r, in the order Allow names them.
func builderAllowedMethods(router *mux.Router, r *http.Request) []string {
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
	allowed := make([]string, 0, len(methods))

	for _, method := range methods {
		probe := r.Clone(r.Context())
		probe.Method = method

		var match mux.RouteMatch

		if router.Match(probe, &match) && match.MatchErr == nil {
			allowed = append(allowed, method)
		}
	}

	return allowed
}

// builderDraftPath is the path of one draft. It is also the start of the
// paths of its snapshots, cursor, shares, publication and preflight checks.
const builderDraftPath = "/builder/drafts/{owner}/{draft}"

// routes registers every Builder route on the given router, one family of
// routes at a time. Each route is also described in
// web/public/docs/openapi.yml, which TestBuilderRoutesDocumented checks.
func (b *builderAPI) routes(router *mux.Router) {
	router.Use(builderResponseHeaders)

	// The router answers a request that matches no route, or no method of
	// one, with these handlers, without running its middleware.
	if router.NotFoundHandler != nil {
		router.NotFoundHandler = builderResponseHeaders(router.NotFoundHandler)
	}

	notAllowed := router.MethodNotAllowedHandler
	if notAllowed == nil {
		notAllowed = builderMethodNotAllowed(router)
	}

	router.MethodNotAllowedHandler = builderResponseHeaders(notAllowed)

	b.schemaRoutes(router)
	b.draftRoutes(router)
	b.publishingRoutes(router)
	b.shareRoutes(router)
	b.sourceRoutes(router)
	b.documentRoutes(router)
	b.iconRoutes(router)
	b.templateRoutes(router)
}

// schemaRoutes registers the schema routes of the document, template file
// and package formats.
func (b *builderAPI) schemaRoutes(router *mux.Router) {
	router.Handle(builderSchemaPath, builderHandler(b.getSchema)).
		Methods("GET", "OPTIONS")
	router.Handle(builderTemplateSchemaPath, builderHandler(b.getTemplateSchema)).
		Methods("GET", "OPTIONS")
	router.Handle(builderPackageSchemaPath, builderHandler(b.getPackageSchema)).
		Methods("GET", "OPTIONS")
}

// draftRoutes registers the routes of drafts, their snapshots and their
// cursor.
func (b *builderAPI) draftRoutes(router *mux.Router) {
	const snapshotsPath = builderDraftPath + "/snapshots"

	router.Handle("/builder/drafts", builderHandler(b.listDrafts)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/drafts", builderHandler(b.createDraft)).
		Methods("POST", "OPTIONS")
	router.Handle(builderDraftPath, builderHandler(b.getDraft)).
		Methods("GET", "OPTIONS")
	router.Handle(builderDraftPath, builderHandler(b.deleteDraft)).
		Methods("DELETE", "OPTIONS")
	router.Handle(snapshotsPath, builderHandler(b.listSnapshots)).
		Methods("GET", "OPTIONS")
	router.Handle(snapshotsPath, builderHandler(b.createSnapshot)).
		Methods("POST", "OPTIONS")
	router.Handle(snapshotsPath+"/{snapshot}", builderHandler(b.getSnapshot)).
		Methods("GET", "OPTIONS")
	router.Handle(snapshotsPath+"/{snapshot}", builderHandler(b.deleteSnapshot)).
		Methods("DELETE", "OPTIONS")
	// Accept PUT and PATCH, so a client that models the cursor as a
	// replaceable sub-resource gets the same handler.
	router.Handle(builderDraftPath+"/cursor", builderHandler(b.updateCursor)).
		Methods("PATCH", "PUT", "OPTIONS")
}

// publishingRoutes registers the routes that publish a draft, show what a
// publish changes (the same route, with dryRun), and run the preflight
// checks of a draft.
func (b *builderAPI) publishingRoutes(router *mux.Router) {
	router.Handle(builderDraftPath+"/publish", builderHandler(b.publishDraft)).
		Methods("POST", "OPTIONS")
	router.Handle(builderDraftPath+"/preflight", builderHandler(b.preflightDraft)).
		Methods("POST", "OPTIONS")
}

// shareRoutes registers the routes of who a draft is shared with.
func (b *builderAPI) shareRoutes(router *mux.Router) {
	router.Handle(builderDraftPath+"/shares", builderHandler(b.getShares)).
		Methods("GET", "OPTIONS")
	router.Handle(builderDraftPath+"/shares", builderHandler(b.putShares)).
		Methods("PUT", "OPTIONS")
	router.Handle(builderDraftPath+"/shares/candidates", builderHandler(b.getShareCandidates)).
		Methods("GET", "OPTIONS")
}

// sourceRoutes registers the routes that list the source configs of a
// document, generate a document, convert a legacy diagram, export a
// topology, and build or resolve a package.
func (b *builderAPI) sourceRoutes(router *mux.Router) {
	router.Handle("/builder/sources", builderHandler(b.listSources)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/generate", builderHandler(b.generateDocument)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/legacy", builderHandler(b.convertLegacy)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/export/topology", builderHandler(b.exportTopology)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/package", builderHandler(b.buildPackage)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/package/resolve", builderHandler(b.resolvePackage)).
		Methods("POST", "OPTIONS")
}

// documentRoutes registers the routes of published documents.
func (b *builderAPI) documentRoutes(router *mux.Router) {
	router.Handle("/builder/documents", builderHandler(b.listDocuments)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/documents/{document}", builderHandler(b.getDocument)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/documents/{document}", builderHandler(b.deleteDocument)).
		Methods("DELETE", "OPTIONS")
	router.Handle("/builder/topologies/{topology}/document", builderHandler(b.getTopologyDocument)).
		Methods("GET", "OPTIONS")
}

// iconRoutes registers the routes of the icon library.
func (b *builderAPI) iconRoutes(router *mux.Router) {
	router.Handle(builderIconsPath, builderHandler(b.listIcons)).
		Methods("GET", "OPTIONS")
	router.Handle(builderIconsPath, builderHandler(b.createIcon)).
		Methods("POST", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.getIcon)).
		Methods("GET", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.renameIcon)).
		Methods("PUT", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.deleteIcon)).
		Methods("DELETE", "OPTIONS")
}

// templateRoutes registers the routes of the template library.
func (b *builderAPI) templateRoutes(router *mux.Router) {
	const libraryPath = builderTemplatesPath + "/{owner}"

	router.Handle(builderTemplatesPath, builderHandler(b.listTemplates)).
		Methods("GET", "OPTIONS")
	router.Handle(builderTemplatesPath+"/candidates", builderHandler(b.getTemplateShareCandidates)).
		Methods("GET", "OPTIONS")
	router.Handle(libraryPath+"/items", builderHandler(b.createTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/items/{template}", builderHandler(b.putTemplate)).
		Methods("PUT", "OPTIONS")
	router.Handle(libraryPath+"/collections", builderHandler(b.createTemplateCollection)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/collections/{collection}", builderHandler(b.putTemplateCollection)).
		Methods("PUT", "OPTIONS")
	router.Handle(libraryPath+"/delete", builderHandler(b.deleteTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/restore", builderHandler(b.restoreTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/share", builderHandler(b.shareTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/publish", builderHandler(b.publishTemplates)).
		Methods("POST", "OPTIONS")
}

// getSchema - GET /schemas/builder/v1.
func (b *builderAPI) getSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetSchema")

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, "getting the builder schema")
	}

	if !actor.role.Allowed("schemas", "get", "builder") {
		return builderForbidden(actor, "getting the builder schema")
	}

	body, err := bdoc.SchemaJSON()
	if err != nil {
		return weberror.NewWebError(err, "unable to build the builder schema").
			SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", mimeJSON)

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// getTemplateSchema - GET /schemas/builder/templates/v1.
//
// The JSON Schema of a template file, under the permission of the document
// schema: schemas get on the resource name builder.
func (b *builderAPI) getTemplateSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetTemplateSchema")

	const action = "getting the builder template file schema"

	actor, ok := builderRequestActor(r)
	if !ok || !actor.role.Allowed("schemas", "get", "builder") {
		return builderForbidden(actor, action)
	}

	body, err := bdoc.TemplateFileSchemaJSON()
	if err != nil {
		return weberror.NewWebError(err, "unable to build the builder template file schema").
			SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", mimeJSON)

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// readDraft returns the request's actor and the draft its path names, as
// [builderAPI.draftAccessFor] loads it for a read.
func (b *builderAPI) readDraft(
	r *http.Request,
	action string,
) (builderActor, *bapi.DraftMetadata, builderAccess, error) {
	actor, ok := builderRequestActor(r)
	if !ok {
		return actor, nil, builderAccess{}, builderForbidden(actor, action)
	}

	meta, access, err := b.draftAccessFor(r, actor, builderVerbGet, action)

	return actor, meta, access, err
}

// draftFor loads the draft named by the request path, and applies both
// authorization layers. These cases all give the same 404: a caller that
// may not see the draft, a draft that does not exist, and a draft whose
// owner does not match the path. A caller that may see the draft but may
// not do the verb gets 403.
func (b *builderAPI) draftFor(
	r *http.Request,
	actor builderActor,
	verb builderVerb,
	action string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.draftAccessFor(r, actor, verb, action)

	return meta, err
}

// draftAccessFor is [builderAPI.draftFor] that also returns the caller's
// access to the draft.
func (b *builderAPI) draftAccessFor(
	r *http.Request,
	actor builderActor,
	verb builderVerb,
	action string,
) (*bapi.DraftMetadata, builderAccess, error) {
	vars := mux.Vars(r)

	return b.namedDraftAccess(r, actor, verb, action, vars["owner"], vars["draft"])
}

// namedDraft is [builderAPI.draftFor] for a draft named by its owner and
// ID rather than by the request path.
func (b *builderAPI) namedDraft(
	r *http.Request,
	actor builderActor,
	verb builderVerb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.namedDraftAccess(r, actor, verb, action, owner, draftID)

	return meta, err
}

// namedDraftAccess is [builderAPI.draftAccessFor] for a draft named by its
// owner and ID rather than by the request path.
//
// The shares of a draft are part of its record, so the function reads the
// record before it authorizes a caller other than the owner. The reply never
// tells a caller who may not see the draft that it exists. For such a
// caller, these cases all give the same 404: a missing draft, the draft of
// another user, and a draft whose record this server cannot read.
func (b *builderAPI) namedDraftAccess(
	r *http.Request,
	actor builderActor,
	verb builderVerb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, builderAccess, error) {
	var none builderAccess

	name := builderDraftName(owner, draftID)

	if !builderBaseAllowed(actor.role, verb) {
		return nil, none, builderForbidden(actor, action)
	}

	if owner == "" || len(owner) > bapi.MaxOwnerLength || !bapi.ValidID(draftID) {
		return nil, none, builderNotFound("draft", name)
	}

	var grants builderRoleGrants
	if owner != actor.user {
		grants = builderRoleGrantsFor(actor.role, name)
	}

	meta, err := b.drafts.GetDraft(r.Context(), draftID)

	// A draft whose metadata no longer validates can only be deleted. Its owner
	// is enough to authorize that. The function never reads the shares of such
	// a record, so shares grant nothing on it. It first compares the stored
	// owner with the path. Thus a caller that names itself, or an owner its role
	// covers, learns nothing about the damaged draft of another user.
	if errors.Is(err, bapi.ErrCorrupt) {
		stored, ownerErr := b.drafts.GetDraftOwner(r.Context(), draftID)

		switch {
		case ownerErr != nil || stored.Owner != owner:
			return nil, none, builderNotFound("draft", name)
		case verb == builderVerbDelete:
			meta, err = stored, nil
		case owner != actor.user && !grants.visible(verb):
			return nil, none, builderNotFound("draft", name)
		}
	}

	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, none, builderNotFound("draft", name)
		}

		return nil, none, builderWebError(err, "unable to get draft %s", name)
	}

	// The draft key is the ID alone, so the owner in the path is authoritative.
	// A mismatch means that the authorization used an owner that does not hold
	// the draft.
	if meta.Owner != owner {
		return nil, none, builderNotFound("draft", name)
	}

	access, err := builderDraftAccess(actor, meta, grants, b.accountOnce(actor.user))
	if err != nil {
		return nil, none, weberror.NewWebError(err, "unable to check access to draft %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	if !access.visible(verb) {
		reason := "no-access"
		if access.stale {
			reason = "stale-share"
		}

		builderWarnDenied(actor, owner, action, reason)

		return nil, none, builderNotFound("draft", name)
	}

	if !access.allows(verb) {
		builderWarnDenied(actor, owner, action, access.denial(verb))

		return nil, none, builderNotAllowed(actor, action)
	}

	return meta, access, nil
}
