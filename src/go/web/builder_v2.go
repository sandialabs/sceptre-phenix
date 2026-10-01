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
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// Builder v2 is the HTTP API of the Vue Flow builder. It is gated behind the
// "builder-v2" feature flag. Draft autosave lives in the generic record store
// (see [phenix/api/builder]); configs are created or updated only by an
// explicit publish, and deleted only by DELETE /builder-v2/documents/{document}.
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
	// builderV2Feature is the feature flag that gates every route in this
	// file.
	builderV2Feature = "builder-v2"

	// builderV2DraftsResource is the RBAC resource authorizing operations on
	// drafts owned by another user. Resource names are "{owner}/{draftID}".
	builderV2DraftsResource = "builder-drafts"

	// builderV2CurrentSnapshot is the snapshot path segment that names
	// whichever snapshot the draft cursor currently points at.
	builderV2CurrentSnapshot = "current"

	// builderV2EnvelopeBytes is the room a Builder v2 request body is
	// allowed on top of the document it carries: titles, summaries, and JSON
	// string escaping.
	builderV2EnvelopeBytes = 1 << 20

	// builderV2MaxRequestBytes bounds every Builder v2 request body.
	// Payloads within the envelope are additionally bound by
	// [bapi.MaxDocumentBytes].
	builderV2MaxRequestBytes = bapi.MaxDocumentBytes + builderV2EnvelopeBytes
)

// builderV2Verb is one of the operations Builder v2 authorizes. It exists
// so the base config permission and the cross-user draft permission of an
// operation are always derived from the same value.
type builderV2Verb string

const (
	builderV2VerbList   builderV2Verb = "list"
	builderV2VerbGet    builderV2Verb = "get"
	builderV2VerbCreate builderV2Verb = "create"
	builderV2VerbUpdate builderV2Verb = "update"
	builderV2VerbDelete builderV2Verb = "delete"
	// builderV2VerbShare changes who a draft is shared with. Its base
	// permission is config update; only the draft owner may do it.
	builderV2VerbShare builderV2Verb = "share"
)

// builderV2Level orders what a caller may do with a draft.
type builderV2Level int

const (
	builderV2LevelNone builderV2Level = iota
	builderV2LevelView
	builderV2LevelEdit
	builderV2LevelOwner
)

// Where access to another user's draft comes from: a share naming the caller,
// or the caller's role. A caller holding both is reported as "share".
const (
	builderV2ViaShare = "share"
	builderV2ViaRole  = "role"
)

// builderV2AccessOwner is how responses report a caller's access to its own
// draft; access through a share is reported as the share's access.
const builderV2AccessOwner = "owner"

// builderV2RoleGrants is what the "builder-drafts" permission of a role
// grants on one draft of another user.
type builderV2RoleGrants struct {
	canList, canGet, canUpdate, canDelete bool
}

// builderV2Access is a caller's access to one draft (see [builderV2API]).
// Level is the strongest of what its ownership, a share and its role grant;
// via is where access to another user's draft comes from; stale is set when a
// share names the caller but no longer matches its account, and so grants
// nothing.
type builderV2Access struct {
	level builderV2Level
	via   string
	rbac  builderV2RoleGrants
	stale bool
}

// builderV2API serves the Builder v2 routes. Config access and publication
// effects are injected so handlers can be exercised without a real store.
type builderV2API struct {
	drafts      *bapi.Service
	listConfigs func(kind string) (store.Configs, error)
	getConfig   func(name string) (*store.Config, error)
	publish     builderV2PublishOps
}

// builderV2Option configures a [builderV2API].
type builderV2Option func(*builderV2API)

// builderV2Actor is the authenticated identity and role of a request.
type builderV2Actor struct {
	user string
	role rbac.Role
}

// newBuilderV2API returns an API bound to the phenix config store, with the
// given options applied.
func newBuilderV2API(opts ...builderV2Option) (*builderV2API, error) {
	service, err := bapi.New()
	if err != nil {
		return nil, err
	}

	api := &builderV2API{
		drafts:      service,
		listConfigs: config.List,
		getConfig:   func(name string) (*store.Config, error) { return config.Get(name, false) },
		publish:     newBuilderV2PublishOps(),
	}

	for _, opt := range opts {
		opt(api)
	}

	api.cleanupStorage()

	return api, nil
}

// cleanupStorage removes interrupted chunk writes and published documents no
// topology references. Document cleanup only runs after a complete topology
// listing with entirely decodable references; otherwise deleting an apparently
// orphaned document could break a topology omitted from the reference set.
func (b *builderV2API) cleanupStorage() {
	topologies, err := b.listConfigs(builderV2KindTopology)
	if err != nil {
		plog.Error(plog.TypeSystem, "listing builder topology references for cleanup", "err", err)
	} else {
		references := make([]bapi.DocumentReference, 0)
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

			references = append(references, reference)
		}

		if complete {
			if _, cleanupErr := b.drafts.CleanupOrphanedDocuments(
				context.Background(),
				references,
			); cleanupErr != nil {
				plog.Error(plog.TypeSystem, "cleaning orphaned builder documents", "err", cleanupErr)
			}
		}
	}

	if _, err := b.drafts.CleanupOrphanedChunks(context.Background()); err != nil {
		plog.Error(plog.TypeSystem, "cleaning orphaned builder chunks", "err", err)
	}
}

// withBuilderV2Service sets the draft service the handlers persist to.
func withBuilderV2Service(service *bapi.Service) builderV2Option {
	return func(api *builderV2API) { api.drafts = service }
}

// withBuilderV2Configs sets the read-only config accessors used by the source
// listing and generation endpoints.
func withBuilderV2Configs(
	list func(kind string) (store.Configs, error),
	get func(name string) (*store.Config, error),
) builderV2Option {
	return func(api *builderV2API) {
		api.listConfigs = list
		api.getConfig = get
	}
}

// withBuilderV2PublishOps replaces explicit publication effects in tests.
func withBuilderV2PublishOps(ops builderV2PublishOps) builderV2Option {
	return func(api *builderV2API) { api.publish = ops }
}

// registerBuilderV2Routes adds the Builder v2 routes to the given API
// router when the "builder-v2" feature is enabled. It is a no-op otherwise,
// so a server started without the feature has no Builder v2 surface at all.
func registerBuilderV2Routes(router *mux.Router, opts ...builderV2Option) error {
	if !o.featured(builderV2Feature) {
		return nil
	}

	api, err := newBuilderV2API(opts...)
	if err != nil {
		return err
	}

	api.routes(router)

	plog.Info(plog.TypeSystem, "Builder v2 API enabled", "feature", builderV2Feature)

	return nil
}

// builderV2RequestActor returns the authenticated identity of a request. The
// second return value is false when the request carries no usable identity,
// which the auth middleware normally prevents.
func builderV2RequestActor(r *http.Request) (builderV2Actor, bool) {
	ctx := r.Context()

	actor := builderV2Actor{
		user: middleware.UserFromContext(ctx),
		role: middleware.RoleFromContext(ctx),
	}

	if actor.user == "" || actor.role.Spec == nil {
		return actor, false
	}

	return actor, true
}

// builderV2Authorize returns the request's actor when it holds the base
// config permission of verb, and otherwise the 403 of [builderV2Forbidden].
func builderV2Authorize(r *http.Request, verb builderV2Verb, action string) (builderV2Actor, error) {
	actor, ok := builderV2RequestActor(r)
	if !ok || !builderV2BaseAllowed(actor.role, verb) {
		return actor, builderV2Forbidden(actor, action)
	}

	return actor, nil
}

// builderV2BaseAllowed reports whether the role holds the base config
// permission every Builder v2 operation of this kind requires. The checks are
// written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
func builderV2BaseAllowed(role rbac.Role, verb builderV2Verb, names ...string) bool {
	switch verb {
	case builderV2VerbList:
		return role.Allowed("configs", "list", names...)
	case builderV2VerbGet:
		return role.Allowed("configs", "get", names...)
	case builderV2VerbCreate:
		return role.Allowed("configs", "create", names...)
	case builderV2VerbUpdate, builderV2VerbShare:
		return role.Allowed("configs", "update", names...)
	case builderV2VerbDelete:
		return role.Allowed("configs", "delete", names...)
	}

	return false
}

// builderV2CrossUserAllowed reports whether the role may operate on a draft
// owned by another user. Creation is missing on purpose: a draft is always
// created for the authenticated user, never on somebody else's behalf. So is
// sharing: only the owner changes who a draft is shared with.
func builderV2CrossUserAllowed(role rbac.Role, verb builderV2Verb, names ...string) bool {
	switch verb {
	case builderV2VerbList:
		return role.Allowed("builder-drafts", "list", names...)
	case builderV2VerbGet:
		return role.Allowed("builder-drafts", "get", names...)
	case builderV2VerbUpdate:
		return role.Allowed("builder-drafts", "update", names...)
	case builderV2VerbDelete:
		return role.Allowed("builder-drafts", "delete", names...)
	case builderV2VerbCreate, builderV2VerbShare:
		return false
	}

	return false
}

// builderV2RoleGrantsFor returns what the role grants on the draft of
// another user with the given resource name.
func builderV2RoleGrantsFor(role rbac.Role, name string) builderV2RoleGrants {
	return builderV2RoleGrants{
		canList:   builderV2CrossUserAllowed(role, builderV2VerbList, name),
		canGet:    builderV2CrossUserAllowed(role, builderV2VerbGet, name),
		canUpdate: builderV2CrossUserAllowed(role, builderV2VerbUpdate, name),
		canDelete: builderV2CrossUserAllowed(role, builderV2VerbDelete, name),
	}
}

// builderV2HoldsRoleGrants reports whether the role grants anything on any
// draft of another user, so callers can skip the checks by name otherwise.
func builderV2HoldsRoleGrants(role rbac.Role) bool {
	return builderV2CrossUserAllowed(role, builderV2VerbList) ||
		builderV2CrossUserAllowed(role, builderV2VerbGet) ||
		builderV2CrossUserAllowed(role, builderV2VerbUpdate) ||
		builderV2CrossUserAllowed(role, builderV2VerbDelete)
}

// allowed reports whether the grants include the verb.
func (g builderV2RoleGrants) allowed(verb builderV2Verb) bool {
	switch verb {
	case builderV2VerbList:
		return g.canList
	case builderV2VerbGet:
		return g.canGet
	case builderV2VerbUpdate:
		return g.canUpdate
	case builderV2VerbDelete:
		return g.canDelete
	case builderV2VerbCreate, builderV2VerbShare:
		return false
	}

	return false
}

// visible reports whether the grants let the caller see the draft when asking
// to perform the verb.
func (g builderV2RoleGrants) visible(verb builderV2Verb) bool {
	return g.canList || g.canGet || g.allowed(verb)
}

// level returns the level the grants amount to: update is edit, get or list
// is view.
func (g builderV2RoleGrants) level() builderV2Level {
	switch {
	case g.canUpdate:
		return builderV2LevelEdit
	case g.canGet || g.canList:
		return builderV2LevelView
	}

	return builderV2LevelNone
}

// owner reports whether the caller owns the draft.
func (a builderV2Access) owner() bool {
	return a.level == builderV2LevelOwner
}

// visible reports whether the caller may know the draft exists when asking to
// perform the verb: it owns the draft, a share names it, or its role grants
// list, get or the verb itself.
func (a builderV2Access) visible(verb builderV2Verb) bool {
	return a.owner() || a.via == builderV2ViaShare || a.rbac.visible(verb)
}

// allows reports whether the caller may perform the verb. A share grants list
// and get, and update when it is an edit share; it never grants delete or
// sharing.
func (a builderV2Access) allows(verb builderV2Verb) bool {
	if a.owner() {
		return true
	}

	switch verb {
	case builderV2VerbList, builderV2VerbGet:
		return a.via == builderV2ViaShare || a.rbac.allowed(verb)
	case builderV2VerbUpdate:
		return a.level >= builderV2LevelEdit
	case builderV2VerbDelete:
		return a.rbac.canDelete
	case builderV2VerbCreate, builderV2VerbShare:
		return false
	}

	return false
}

// denial names why the caller, who may see the draft, may not perform the
// verb, for the security log.
func (a builderV2Access) denial(verb builderV2Verb) string {
	switch {
	case verb == builderV2VerbShare,
		verb == builderV2VerbDelete && a.via == builderV2ViaShare:
		return "not-owner"
	case verb == builderV2VerbUpdate && a.via == builderV2ViaShare:
		return "view-only"
	}

	return "rbac"
}

// name returns the access as responses report it: "owner", "edit", "view",
// or "" for none.
func (a builderV2Access) name() string {
	switch a.level {
	case builderV2LevelOwner:
		return builderV2AccessOwner
	case builderV2LevelEdit:
		return string(bapi.ShareEdit)
	case builderV2LevelView:
		return string(bapi.ShareView)
	case builderV2LevelNone:
		return ""
	}

	return ""
}

// builderV2DraftAccess returns the caller's access to a draft, given what
// its role grants on it. The caller's account is read, through account, only
// when a share names the caller: a share applies only while the account it
// was granted to still exists, so it never passes to a new account created
// under the same name.
func builderV2DraftAccess(
	actor builderV2Actor,
	meta *bapi.DraftMetadata,
	grants builderV2RoleGrants,
	account func() (string, bool, error),
) (builderV2Access, error) {
	if meta.Owner == actor.user {
		return builderV2Access{
			level: builderV2LevelOwner,
			via:   "",
			rbac:  builderV2RoleGrants{canList: false, canGet: false, canUpdate: false, canDelete: false},
			stale: false,
		}, nil
	}

	access := builderV2Access{level: grants.level(), via: "", rbac: grants, stale: false}

	if grants.canList || grants.canGet || grants.canUpdate || grants.canDelete {
		access.via = builderV2ViaRole
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

	access.via = builderV2ViaShare

	share := builderV2LevelView
	if entry.Access == bapi.ShareEdit {
		share = builderV2LevelEdit
	}

	access.level = max(access.level, share)

	return access, nil
}

// accountCreated returns metadata.created of the User config of the named
// user, and whether the user has an account a share can be bound to. A name
// that cannot name a config, a missing config and one without a creation
// time are all no account.
func (b *builderV2API) accountCreated(name string) (string, bool, error) {
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

// accountOnce returns [builderV2API.accountCreated] for the named user,
// read at most once however often it is called.
func (b *builderV2API) accountOnce(name string) func() (string, bool, error) {
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
func (b *builderV2API) canShare(actor builderV2Actor, account func() (string, bool, error)) (bool, error) {
	if !builderV2BaseAllowed(actor.role, builderV2VerbShare) {
		return false, nil
	}

	_, exists, err := account()

	return exists, err
}

// builderV2DraftName is the RBAC resource name of a draft.
func builderV2DraftName(owner, draftID string) string {
	return owner + "/" + draftID
}

// builderV2Forbidden returns the 403 every failed permission check answers
// with. The action is a short description of the attempted operation; request
// bodies are never included.
func builderV2Forbidden(actor builderV2Actor, action string) *weberror.WebError {
	plog.Warn(
		plog.TypeSecurity,
		"builder v2 request not allowed",
		"user",
		actor.user,
		"action",
		action,
	)

	return builderV2NotAllowed(actor, action)
}

// builderV2NotAllowed returns the 403 of [builderV2Forbidden] without
// logging it, for callers that log the refusal themselves.
func builderV2NotAllowed(actor builderV2Actor, action string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s not allowed for %s", action, actor.user).
		SetStatus(http.StatusForbidden)
}

// builderV2WarnDenied logs a refused request for another user's draft. The
// reason tells refusals apart: "no-access" or "stale-share" for a draft the
// caller may not see, answered with 404, and "view-only", "not-owner" or
// "rbac" for one it may see but not change this way, answered with 403.
func builderV2WarnDenied(actor builderV2Actor, owner, action, reason string) {
	plog.Warn(
		plog.TypeSecurity,
		"builder v2 cross-user draft request not allowed",
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

// builderV2NotFound returns the 404 answering both a missing resource and a
// cross-user request the caller is not allowed to make, so the two are
// indistinguishable to the client.
func builderV2NotFound(kind, name string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s %s not found", kind, name).
		SetStatus(http.StatusNotFound)
}

// builderV2WebError maps a [phenix/api/builder] error to the HTTP status it
// corresponds to. Cleanup failures must be handled by the caller before this is
// reached: they follow a durable, successful mutation. A write refused because
// etcd is out of space is answered with 507 by [weberror.ErrorHandler].
func builderV2WebError(err error, format string, args ...any) *weberror.WebError {
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

// builderV2Mutation resolves the pair a draft mutation returns.
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
func builderV2Mutation[T any](
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
		return nil, builderV2WebError(err, format, args...)
	}

	builderV2WarnCleanup(w, err, operation, user)

	return result, nil
}

// builderV2WarnCleanup reports a cleanup failure that followed a durable,
// successful mutation. The request still succeeded, so it is not failed, but
// the failure is never dropped silently: it is logged with its cause and
// announced on the response with a warning that names the operation only, so
// nothing about the store is disclosed to the client.
func builderV2WarnCleanup(w http.ResponseWriter, err error, operation, user string) {
	plog.Error(
		plog.TypeSystem,
		"builder v2 cleanup failed after a successful mutation",
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

// builderV2IfMatch returns the entity tag a mutation must match. Every
// mutation after creation requires exactly one strong, quoted tag: wildcards
// and weak tags are rejected so a stale client can never overwrite a draft it
// has not seen.
func builderV2IfMatch(r *http.Request) (string, error) {
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

// builderV2CheckIfMatch fails the request when the caller's entity tag does
// not name the revision the draft is currently at.
func builderV2CheckIfMatch(ifMatch string, meta *bapi.DraftMetadata) error {
	if ifMatch == meta.ETag() {
		return nil
	}

	return weberror.NewWebError(nil, "draft %s has changed since it was last read", meta.ID).
		SetStatus(http.StatusPreconditionFailed)
}

// builderV2Decode strictly decodes a JSON request body into target. Unknown
// fields, trailing content, and bodies beyond [builderV2MaxRequestBytes] are
// rejected. The body itself is never logged.
func builderV2Decode(w http.ResponseWriter, r *http.Request, target any) error {
	return builderV2DecodeLimit(w, r, target, builderV2MaxRequestBytes)
}

// builderV2DecodeLimit is [builderV2Decode] for bodies of at most limit
// bytes.
func builderV2DecodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
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
		return weberror.NewWebError(err, "request body is not a valid Builder v2 request").
			SetStatus(http.StatusBadRequest)
	}
}

// builderV2DocumentBytes returns the document bytes of a request. The
// document itself is decoded, validated, and canonicalized by
// [phenix/api/builder], which is the only component that decides what may be
// stored, so this only rejects a missing document.
func builderV2DocumentBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, weberror.NewWebError(nil, "a builder document is required").
			SetStatus(http.StatusBadRequest)
	}

	return raw, nil
}

// builderV2WriteJSON writes a JSON response, optionally tagged with the
// entity tag of the draft it represents.
func builderV2WriteJSON(w http.ResponseWriter, status int, etag string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return weberror.NewWebError(err, "unable to encode the Builder v2 response").
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

// routes registers every Builder v2 route on the given router. Each route
// is also described in web/public/docs/openapi.yml, which
// TestBuilderV2RoutesDocumented checks.
func (b *builderV2API) routes(router *mux.Router) {
	const (
		draftPath     = "/builder-v2/drafts/{owner}/{draft}"
		snapshotsPath = draftPath + "/snapshots"
	)

	router.Handle("/schemas/builder-v2/v1", weberror.ErrorHandler(b.getSchema)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder-v2/drafts", weberror.ErrorHandler(b.listDrafts)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder-v2/drafts", weberror.ErrorHandler(b.createDraft)).
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
	router.Handle("/builder-v2/sources", weberror.ErrorHandler(b.listSources)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder-v2/generate", weberror.ErrorHandler(b.generateDocument)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder-v2/export/topology", weberror.ErrorHandler(b.exportTopology)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder-v2/documents", weberror.ErrorHandler(b.listDocuments)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder-v2/documents/{document}", weberror.ErrorHandler(b.getDocument)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder-v2/documents/{document}", weberror.ErrorHandler(b.deleteDocument)).
		Methods("DELETE", "OPTIONS")
}

// getSchema - GET /schemas/builder-v2/v1.
func (b *builderV2API) getSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2GetSchema")

	actor, ok := builderV2RequestActor(r)
	if !ok {
		return builderV2Forbidden(actor, "getting the builder schema")
	}

	if !actor.role.Allowed("schemas", "get", "builder-v2") {
		return builderV2Forbidden(actor, "getting the builder schema")
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
// [builderV2API.draftAccessFor] loads it for a read.
func (b *builderV2API) readDraft(
	r *http.Request,
	action string,
) (builderV2Actor, *bapi.DraftMetadata, builderV2Access, error) {
	actor, ok := builderV2RequestActor(r)
	if !ok {
		return actor, nil, builderV2Access{}, builderV2Forbidden(actor, action)
	}

	meta, access, err := b.draftAccessFor(r, actor, builderV2VerbGet, action)

	return actor, meta, access, err
}

// draftFor loads the draft named by the request path, enforcing both
// authorization layers. A caller that may not see the draft, a draft that
// does not exist, and a draft whose owner does not match the path all produce
// the same 404; a caller that may see the draft but not perform the verb gets
// 403.
func (b *builderV2API) draftFor(
	r *http.Request,
	actor builderV2Actor,
	verb builderV2Verb,
	action string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.draftAccessFor(r, actor, verb, action)

	return meta, err
}

// draftAccessFor is [builderV2API.draftFor] that also returns the caller's
// access to the draft.
func (b *builderV2API) draftAccessFor(
	r *http.Request,
	actor builderV2Actor,
	verb builderV2Verb,
	action string,
) (*bapi.DraftMetadata, builderV2Access, error) {
	vars := mux.Vars(r)

	return b.namedDraftAccess(r, actor, verb, action, vars["owner"], vars["draft"])
}

// namedDraft is [builderV2API.draftFor] for a draft named by its owner and
// ID rather than by the request path.
func (b *builderV2API) namedDraft(
	r *http.Request,
	actor builderV2Actor,
	verb builderV2Verb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.namedDraftAccess(r, actor, verb, action, owner, draftID)

	return meta, err
}

// namedDraftAccess is [builderV2API.draftAccessFor] for a draft named by its
// owner and ID rather than by the request path.
//
// Who a draft is shared with is part of its record, so the record is read
// before a caller other than the owner is authorized. The reply never tells a
// caller who may not see the draft that it exists: a missing draft, a draft
// of someone else, and a draft whose record this server cannot read all
// produce the same 404 for such a caller.
func (b *builderV2API) namedDraftAccess(
	r *http.Request,
	actor builderV2Actor,
	verb builderV2Verb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, builderV2Access, error) {
	var none builderV2Access

	name := builderV2DraftName(owner, draftID)

	if !builderV2BaseAllowed(actor.role, verb) {
		return nil, none, builderV2Forbidden(actor, action)
	}

	if owner == "" || len(owner) > bapi.MaxOwnerLength || !bapi.ValidID(draftID) {
		return nil, none, builderV2NotFound("draft", name)
	}

	var grants builderV2RoleGrants
	if owner != actor.user {
		grants = builderV2RoleGrantsFor(actor.role, name)
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
			return nil, none, builderV2NotFound("draft", name)
		case verb == builderV2VerbDelete:
			meta, err = stored, nil
		case owner != actor.user && !grants.visible(verb):
			return nil, none, builderV2NotFound("draft", name)
		}
	}

	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, none, builderV2NotFound("draft", name)
		}

		return nil, none, builderV2WebError(err, "unable to get draft %s", name)
	}

	// Drafts are keyed by ID alone, so the owner in the path is authoritative:
	// a mismatch means the caller was authorized against an owner that does not
	// hold the draft.
	if meta.Owner != owner {
		return nil, none, builderV2NotFound("draft", name)
	}

	access, err := builderV2DraftAccess(actor, meta, grants, b.accountOnce(actor.user))
	if err != nil {
		return nil, none, weberror.NewWebError(err, "unable to check access to draft %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	if !access.visible(verb) {
		reason := "no-access"
		if access.stale {
			reason = "stale-share"
		}

		builderV2WarnDenied(actor, owner, action, reason)

		return nil, none, builderV2NotFound("draft", name)
	}

	if !access.allows(verb) {
		builderV2WarnDenied(actor, owner, action, access.denial(verb))

		return nil, none, builderV2NotAllowed(actor, action)
	}

	return meta, access, nil
}
