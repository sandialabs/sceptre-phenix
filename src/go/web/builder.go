package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
// the generic record store (see [phenix/api/builder]); configs are created or
// updated only by an explicit publish, and deleted only by
// DELETE /builder/documents/{document}.
//
// Authorization has two layers:
//
//   - every request needs the base config permission of the operation it
//     performs, so builder access can never exceed a user's config access, and
//   - a request touching a draft needs access to that draft, from one of three
//     sources: owning it; a share from its owner naming the caller, which
//     grants view (list and get) or edit (also update), never delete or
//     changing who it is shared with; or the "builder-drafts" permission of
//     the same verb for the "{owner}/{draftID}" resource name. The strongest
//     source wins.
//
// A caller who cannot see a draft, because no source grants it any access, is
// answered with 404, exactly as for a draft that does not exist, so draft
// existence is never disclosed. A caller who can see a draft but may not
// perform the operation is answered with 403.
const (
	// builderDraftsResource is the RBAC resource authorizing operations on
	// drafts owned by another user. Resource names are "{owner}/{draftID}".
	builderDraftsResource = "builder-drafts"

	// builderCurrentSnapshot is the snapshot path segment that names
	// whichever snapshot the draft cursor currently points at.
	builderCurrentSnapshot = "current"

	// builderEnvelopeBytes is the room a Builder request body is
	// allowed on top of the document it carries: titles, summaries, and JSON
	// string escaping.
	builderEnvelopeBytes = 1 << 20

	// builderMaxRequestBytes bounds every Builder request body.
	// Payloads within the envelope are additionally bound by
	// [bapi.MaxDocumentBytes].
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
	// permission is config update; only the draft owner may do it.
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
// draft; access through a share is reported as the share's access.
const builderAccessOwner = "owner"

// builderRoleGrants is what the "builder-drafts" permission of a role
// grants on one draft of another user.
type builderRoleGrants struct {
	canList, canGet, canUpdate, canDelete bool
}

// builderAccess is a caller's access to one draft (see [builderAPI]).
// Level is the strongest of what its ownership, a share and its role grant;
// via is where access to another user's draft comes from; stale is set when a
// share names the caller but no longer matches its account, and so grants
// nothing.
type builderAccess struct {
	level builderLevel
	via   string
	rbac  builderRoleGrants
	stale bool
}

// builderAPI serves the Builder routes. Config access, publication
// effects and where Builder files are read from are injected so handlers can
// be exercised without a real store or the server's directories.
type builderAPI struct {
	drafts      *bapi.Service
	listConfigs func(kind string) (store.Configs, error)
	getConfig   func(name string) (*store.Config, error)
	publish     builderPublishOps
	// documentFiles returns the directory the Builder files that document
	// references name are read from, and the directories below it they are
	// never read from (see [bapi.ReadDocumentFile]).
	documentFiles func() (string, []string)
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
	}

	for _, opt := range opts {
		opt(api)
	}

	api.cleanupStorage()

	return api, nil
}

// cleanupStorage removes interrupted chunk writes, published documents no
// topology references, and icon records of the per-user layout of earlier
// builds. Document cleanup only runs after a complete topology listing with
// entirely decodable references; otherwise deleting an apparently orphaned
// document could break a topology omitted from the reference set. A
// reference that names only a file names no stored document, and keeps none.
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
// phenix base directory (--base-dir.phenix), except the directory VM file
// systems are mounted in, which holds guest files and can hang a read. The
// store and the settings file, with its signing key, are kept outside the
// base directory. Both are read when a file is, so they are the directories
// the server was started with.
func builderDocumentFiles() (string, []string) {
	return common.PhenixBase, []string{common.MountDir()}
}

// withBuilderDocumentFiles sets the directory Builder files are read from,
// and the directories below it they are never read from.
func withBuilderDocumentFiles(root string, excluded ...string) builderOption {
	return func(api *builderAPI) {
		api.documentFiles = func() (string, []string) { return root, excluded }
	}
}

// registerBuilderRoutes adds the Builder routes to the given API
// router, whose NotFoundHandler is set already: the Builder's response
// headers are added to it (see [builderResponseHeaders]).
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
// permission every Builder operation of this kind requires. The checks are
// written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
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
// owned by another user. Creation is missing on purpose: a draft is always
// created for the authenticated user, never on somebody else's behalf. So is
// sharing: only the owner changes who a draft is shared with.
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

// visible reports whether the caller may know the draft exists when asking to
// perform the verb: it owns the draft, a share names it, or its role grants
// list, get or the verb itself.
func (a builderAccess) visible(verb builderVerb) bool {
	return a.owner() || a.via == builderViaShare || a.rbac.visible(verb)
}

// allows reports whether the caller may perform the verb. A share grants list
// and get, and update when it is an edit share; it never grants delete or
// sharing.
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

// builderDraftAccess returns the caller's access to a draft, given what
// its role grants on it. The caller's account is read, through account, only
// when a share names the caller: a share applies only while the account it
// was granted to still exists, so it never passes to a new account created
// under the same name.
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

// accountOnce returns [builderAPI.accountCreated] for the named user,
// read at most once however often it is called.
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
// it is shared with: that takes config update, and a user account, which the
// caller has no other reason to hold when authentication is off.
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

// builderForbidden returns the 403 every failed permission check answers
// with. The action is a short description of the attempted operation; request
// bodies are never included.
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

// builderWarnDenied logs a refused request for another user's draft. The
// reason tells refusals apart: "no-access" or "stale-share" for a draft the
// caller may not see, answered with 404, and "view-only", "not-owner" or
// "rbac" for one it may see but not change this way, answered with 403.
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

// builderNotFound returns the 404 answering both a missing resource and a
// cross-user request the caller is not allowed to make, so the two are
// indistinguishable to the client.
func builderNotFound(kind, name string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s %s not found", kind, name).
		SetStatus(http.StatusNotFound)
}

// builderWebError maps a [phenix/api/builder] error to the HTTP status it
// corresponds to. Cleanup failures must be handled by the caller before this is
// reached: they follow a durable, successful mutation. A write refused because
// etcd is out of space is answered with 507 by [weberror.ErrorHandler].
func builderWebError(err error, format string, args ...any) *weberror.WebError {
	webErr := weberror.NewWebError(err, format, args...)

	switch {
	case errors.Is(err, bapi.ErrNotFound):
		return webErr.SetStatus(http.StatusNotFound)
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
// A mutation whose durable write succeeded but which could not remove the
// content it superseded returns its updated metadata together with an error
// matching [bapi.ErrCleanup]. The draft is already at its new revision, so
// failing such a request would only push the client into retrying with an
// entity tag that is stale: the caller is given the updated metadata, and the
// cleanup failure is warned about instead.
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
// successful mutation. The request still succeeded, so it is not failed, but
// the failure is never dropped silently: it is logged with its cause and
// announced on the response with a warning that names the operation only, so
// nothing about the store is disclosed to the client.
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
// mutation after creation requires exactly one strong, quoted tag: wildcards
// and weak tags are rejected so a stale client can never overwrite a draft it
// has not seen.
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
		SetStatus(http.StatusPreconditionFailed)
}

// builderDecode strictly decodes a JSON request body into target. Unknown
// fields, trailing content, and bodies beyond [builderMaxRequestBytes] are
// rejected. The body itself is never logged.
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

// builderDocumentBytes returns the document bytes of a request. The
// document itself is decoded, validated, and canonicalized by
// [phenix/api/builder], which is the only component that decides what may be
// stored, so this only rejects a missing document.
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

	// builderIconsPath is the path of the icon library, whose routes are
	// this path and the paths below it.
	builderIconsPath = builderRoutePrefix + "icons"

	// builderIconPolicy is the Content-Security-Policy of the icon
	// library's responses: load nothing, run nothing, and be framed nowhere.
	builderIconPolicy = "default-src 'none'; frame-ancestors 'none'"
)

// builderResponseHeaders sets the security headers of the responses to
// requests under the Builder's paths: every path below /api/v1/builder/, and
// the schema route. It goes by the request's path, not by the route it
// matched, and leaves every other path alone. [builderAPI.routes] makes it a
// middleware of the API router, so it also covers what the middleware after
// it answers, and wraps the router's handlers of a request that matches no
// route, or no method of one, which no middleware runs for.
//
// Every response under the Builder's paths, whatever its status, tells the
// browser not to guess a content type: each is JSON and says so. The
// responses of the icon library carry images people uploaded, as base64
// inside that JSON, so they also carry a Content-Security-Policy that lets a
// browser load, run and frame nothing, should it ever show one as a page.
func builderResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path, ok := strings.CutPrefix(r.URL.Path, builderAPIPrefix); ok {
			if strings.HasPrefix(path, builderRoutePrefix) || path == builderSchemaPath {
				w.Header().Set("X-Content-Type-Options", "nosniff")
			}

			if path == builderIconsPath || strings.HasPrefix(path, builderIconsPath+"/") {
				w.Header().Set("Content-Security-Policy", builderIconPolicy)
			}
		}

		next.ServeHTTP(w, r)
	})
}

// builderMethodNotAllowed answers a request that matches a route's path but
// none of its methods, as the router does when it has no handler for that.
func builderMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// routes registers every Builder route on the given router. Each route
// is also described in web/public/docs/openapi.yml, which
// TestBuilderRoutesDocumented checks.
func (b *builderAPI) routes(router *mux.Router) {
	const (
		draftPath     = "/builder/drafts/{owner}/{draft}"
		snapshotsPath = draftPath + "/snapshots"
		libraryPath   = builderTemplatesPath + "/{owner}"
	)

	router.Use(builderResponseHeaders)

	// The router answers a request that matches no route, or no method of
	// one, with these handlers, without running its middleware.
	if router.NotFoundHandler != nil {
		router.NotFoundHandler = builderResponseHeaders(router.NotFoundHandler)
	}

	notAllowed := router.MethodNotAllowedHandler
	if notAllowed == nil {
		notAllowed = http.HandlerFunc(builderMethodNotAllowed)
	}

	router.MethodNotAllowedHandler = builderResponseHeaders(notAllowed)

	router.Handle(builderSchemaPath, weberror.ErrorHandler(b.getSchema)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/drafts", weberror.ErrorHandler(b.listDrafts)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/drafts", weberror.ErrorHandler(b.createDraft)).
		Methods("POST", "OPTIONS")
	router.Handle(draftPath, weberror.ErrorHandler(b.getDraft)).
		Methods("GET", "OPTIONS")
	router.Handle(draftPath, weberror.ErrorHandler(b.deleteDraft)).
		Methods("DELETE", "OPTIONS")
	router.Handle(snapshotsPath, weberror.ErrorHandler(b.listSnapshots)).
		Methods("GET", "OPTIONS")
	router.Handle(snapshotsPath, weberror.ErrorHandler(b.createSnapshot)).
		Methods("POST", "OPTIONS")
	router.Handle(snapshotsPath+"/{snapshot}", weberror.ErrorHandler(b.getSnapshot)).
		Methods("GET", "OPTIONS")
	router.Handle(snapshotsPath+"/{snapshot}", weberror.ErrorHandler(b.deleteSnapshot)).
		Methods("DELETE", "OPTIONS")
	// PUT is accepted alongside PATCH so a client that models the cursor as a
	// replaceable sub-resource reaches the same handler.
	router.Handle(draftPath+"/cursor", weberror.ErrorHandler(b.updateCursor)).
		Methods("PATCH", "PUT", "OPTIONS")
	router.Handle(draftPath+"/publish", weberror.ErrorHandler(b.publishDraft)).
		Methods("POST", "OPTIONS")
	router.Handle(draftPath+"/shares", weberror.ErrorHandler(b.getShares)).
		Methods("GET", "OPTIONS")
	router.Handle(draftPath+"/shares", weberror.ErrorHandler(b.putShares)).
		Methods("PUT", "OPTIONS")
	router.Handle(draftPath+"/shares/candidates", weberror.ErrorHandler(b.getShareCandidates)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/sources", weberror.ErrorHandler(b.listSources)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/generate", weberror.ErrorHandler(b.generateDocument)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/legacy", weberror.ErrorHandler(b.convertLegacy)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/export/topology", weberror.ErrorHandler(b.exportTopology)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/documents", weberror.ErrorHandler(b.listDocuments)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/documents/{document}", weberror.ErrorHandler(b.getDocument)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/documents/{document}", weberror.ErrorHandler(b.deleteDocument)).
		Methods("DELETE", "OPTIONS")
	router.Handle("/builder/topologies/{topology}/document", weberror.ErrorHandler(b.getTopologyDocument)).
		Methods("GET", "OPTIONS")
	router.Handle(builderIconsPath, weberror.ErrorHandler(b.listIcons)).
		Methods("GET", "OPTIONS")
	router.Handle(builderIconsPath, weberror.ErrorHandler(b.createIcon)).
		Methods("POST", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", weberror.ErrorHandler(b.getIcon)).
		Methods("GET", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", weberror.ErrorHandler(b.renameIcon)).
		Methods("PUT", "OPTIONS")
	router.Handle(builderIconsPath+"/{icon}", weberror.ErrorHandler(b.deleteIcon)).
		Methods("DELETE", "OPTIONS")
	router.Handle(builderTemplatesPath, weberror.ErrorHandler(b.listTemplates)).
		Methods("GET", "OPTIONS")
	router.Handle(builderTemplatesPath+"/candidates", weberror.ErrorHandler(b.getTemplateShareCandidates)).
		Methods("GET", "OPTIONS")
	router.Handle(libraryPath+"/items", weberror.ErrorHandler(b.createTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/items/{template}", weberror.ErrorHandler(b.putTemplate)).
		Methods("PUT", "OPTIONS")
	router.Handle(libraryPath+"/collections", weberror.ErrorHandler(b.createTemplateCollection)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/collections/{collection}", weberror.ErrorHandler(b.putTemplateCollection)).
		Methods("PUT", "OPTIONS")
	router.Handle(libraryPath+"/delete", weberror.ErrorHandler(b.deleteTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/share", weberror.ErrorHandler(b.shareTemplates)).
		Methods("POST", "OPTIONS")
	router.Handle(libraryPath+"/publish", weberror.ErrorHandler(b.publishTemplates)).
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

// draftFor loads the draft named by the request path, enforcing both
// authorization layers. A caller that may not see the draft, a draft that
// does not exist, and a draft whose owner does not match the path all produce
// the same 404; a caller that may see the draft but not perform the verb gets
// 403.
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
// Who a draft is shared with is part of its record, so the record is read
// before a caller other than the owner is authorized. The reply never tells a
// caller who may not see the draft that it exists: a missing draft, a draft
// of someone else, and a draft whose record this server cannot read all
// produce the same 404 for such a caller.
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

	// A draft whose metadata no longer validates can only be deleted; reading
	// just its owner is enough to authorize that. Who it is shared with is
	// never read from such a record, so shares grant nothing on it. The stored
	// owner is checked against the path first, so a caller naming itself or an
	// owner its role covers learns nothing about another user's damaged draft.
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

	// Drafts are keyed by ID alone, so the owner in the path is authoritative:
	// a mismatch means the caller was authorized against an owner that does not
	// hold the draft.
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
