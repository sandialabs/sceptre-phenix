package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// Builder Beta is the HTTP API of the Vue Flow builder. It is gated behind the
// "builder-beta" feature flag. Draft autosave lives in the generic record store
// (see [phenix/api/builder]); configs are only mutated by an explicit publish.
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
	// builderBetaFeature is the feature flag that gates every route in this
	// file.
	builderBetaFeature = "builder-beta"

	// builderBetaDraftsResource is the RBAC resource authorizing operations on
	// drafts owned by another user. Resource names are "{owner}/{draftID}".
	builderBetaDraftsResource = "builder-drafts"

	// builderBetaCurrentSnapshot is the snapshot path segment that names
	// whichever snapshot the draft cursor currently points at.
	builderBetaCurrentSnapshot = "current"

	// builderBetaEnvelopeBytes is the room a Builder Beta request body is
	// allowed on top of the document it carries: titles, summaries, and JSON
	// string escaping.
	builderBetaEnvelopeBytes = 1 << 20

	// builderBetaMaxRequestBytes bounds every Builder Beta request body.
	// Payloads within the envelope are additionally bound by
	// [bapi.MaxDocumentBytes].
	builderBetaMaxRequestBytes = bapi.MaxDocumentBytes + builderBetaEnvelopeBytes

	// builderBetaXMLAnnotation is the annotation the legacy XML builder writes.
	// It is reported on sources so the UI can tell legacy configs apart.
	builderBetaXMLAnnotation = "builder-xml"

	// builderBetaMaxOwnerLength bounds the owner path segment so an
	// unauthenticated scan cannot drive arbitrarily large comparisons.
	builderBetaMaxOwnerLength = 256
)

// builderBetaVerb is one of the operations Builder Beta authorizes. It exists
// so the base config permission and the cross-user draft permission of an
// operation are always derived from the same value.
type builderBetaVerb string

const (
	builderBetaVerbList   builderBetaVerb = "list"
	builderBetaVerbGet    builderBetaVerb = "get"
	builderBetaVerbCreate builderBetaVerb = "create"
	builderBetaVerbUpdate builderBetaVerb = "update"
	builderBetaVerbDelete builderBetaVerb = "delete"
	// builderBetaVerbShare changes who a draft is shared with. Its base
	// permission is config update; only the draft owner may do it.
	builderBetaVerbShare builderBetaVerb = "share"
)

// builderBetaLevel orders what a caller may do with a draft.
type builderBetaLevel int

const (
	builderBetaLevelNone builderBetaLevel = iota
	builderBetaLevelView
	builderBetaLevelEdit
	builderBetaLevelOwner
)

// Where access to another user's draft comes from: a share naming the caller,
// or the caller's role. A caller holding both is reported as "share".
const (
	builderBetaViaShare = "share"
	builderBetaViaRole  = "role"
)

// builderBetaAccessOwner is how responses report a caller's access to its own
// draft; access through a share is reported as the share's access.
const builderBetaAccessOwner = "owner"

// builderBetaRoleGrants is what the "builder-drafts" permission of a role
// grants on one draft of another user.
type builderBetaRoleGrants struct {
	canList, canGet, canUpdate, canDelete bool
}

// builderBetaAccess is a caller's access to one draft (see [builderBetaAPI]).
// Level is the strongest of what its ownership, a share and its role grant;
// via is where access to another user's draft comes from; stale is set when a
// share names the caller but no longer matches its account, and so grants
// nothing.
type builderBetaAccess struct {
	level builderBetaLevel
	via   string
	rbac  builderBetaRoleGrants
	stale bool
}

// builderBetaIDPattern mirrors the identifier pattern [phenix/api/builder]
// accepts. Path identifiers that cannot match it can never name a stored
// draft, so they are answered with 404 without touching the store.
var builderBetaIDPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
)

// builderBetaAPI serves the Builder Beta routes. Config access and publication
// effects are injected so handlers can be exercised without a real store.
type builderBetaAPI struct {
	drafts      *bapi.Service
	listConfigs func(kind string) (store.Configs, error)
	getConfig   func(name string) (*store.Config, error)
	publish     builderBetaPublishOps
}

// builderBetaOption configures a [builderBetaAPI].
type builderBetaOption func(*builderBetaAPI)

// builderBetaActor is the authenticated identity and role of a request.
type builderBetaActor struct {
	user string
	role rbac.Role
}

// newBuilderBetaAPI returns an API bound to the phenix config store, with the
// given options applied.
func newBuilderBetaAPI(opts ...builderBetaOption) (*builderBetaAPI, error) {
	service, err := bapi.New()
	if err != nil {
		return nil, err
	}

	api := &builderBetaAPI{
		drafts:      service,
		listConfigs: config.List,
		getConfig:   func(name string) (*store.Config, error) { return config.Get(name, false) },
		publish:     newBuilderBetaPublishOps(),
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
func (b *builderBetaAPI) cleanupStorage() {
	topologies, err := b.listConfigs(builderBetaKindTopology)
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

// withBuilderBetaService sets the draft service the handlers persist to.
func withBuilderBetaService(service *bapi.Service) builderBetaOption {
	return func(api *builderBetaAPI) { api.drafts = service }
}

// withBuilderBetaConfigs sets the read-only config accessors used by the source
// listing and generation endpoints.
func withBuilderBetaConfigs(
	list func(kind string) (store.Configs, error),
	get func(name string) (*store.Config, error),
) builderBetaOption {
	return func(api *builderBetaAPI) {
		api.listConfigs = list
		api.getConfig = get
	}
}

// withBuilderBetaPublishOps replaces explicit publication effects in tests.
func withBuilderBetaPublishOps(ops builderBetaPublishOps) builderBetaOption {
	return func(api *builderBetaAPI) { api.publish = ops }
}

// registerBuilderBetaRoutes adds the Builder Beta routes to the given API
// router when the "builder-beta" feature is enabled. It is a no-op otherwise,
// so a server started without the feature has no Builder Beta surface at all.
func registerBuilderBetaRoutes(router *mux.Router, opts ...builderBetaOption) error {
	if !o.featured(builderBetaFeature) {
		return nil
	}

	api, err := newBuilderBetaAPI(opts...)
	if err != nil {
		return err
	}

	api.routes(router)

	plog.Info(plog.TypeSystem, "Builder Flow API enabled", "feature", builderBetaFeature)

	return nil
}

// builderBetaRequestActor returns the authenticated identity of a request. The
// second return value is false when the request carries no usable identity,
// which the auth middleware normally prevents.
func builderBetaRequestActor(r *http.Request) (builderBetaActor, bool) {
	ctx := r.Context()

	actor := builderBetaActor{
		user: middleware.UserFromContext(ctx),
		role: middleware.RoleFromContext(ctx),
	}

	if actor.user == "" || actor.role.Spec == nil {
		return actor, false
	}

	return actor, true
}

// builderBetaBaseAllowed reports whether the role holds the base config
// permission every Builder Beta operation of this kind requires. The checks are
// written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
func builderBetaBaseAllowed(role rbac.Role, verb builderBetaVerb, names ...string) bool {
	switch verb {
	case builderBetaVerbList:
		return role.Allowed("configs", "list", names...)
	case builderBetaVerbGet:
		return role.Allowed("configs", "get", names...)
	case builderBetaVerbCreate:
		return role.Allowed("configs", "create", names...)
	case builderBetaVerbUpdate, builderBetaVerbShare:
		return role.Allowed("configs", "update", names...)
	case builderBetaVerbDelete:
		return role.Allowed("configs", "delete", names...)
	}

	return false
}

// builderBetaCrossUserAllowed reports whether the role may operate on a draft
// owned by another user. Creation is missing on purpose: a draft is always
// created for the authenticated user, never on somebody else's behalf. So is
// sharing: only the owner changes who a draft is shared with.
func builderBetaCrossUserAllowed(role rbac.Role, verb builderBetaVerb, names ...string) bool {
	switch verb {
	case builderBetaVerbList:
		return role.Allowed("builder-drafts", "list", names...)
	case builderBetaVerbGet:
		return role.Allowed("builder-drafts", "get", names...)
	case builderBetaVerbUpdate:
		return role.Allowed("builder-drafts", "update", names...)
	case builderBetaVerbDelete:
		return role.Allowed("builder-drafts", "delete", names...)
	case builderBetaVerbCreate, builderBetaVerbShare:
		return false
	}

	return false
}

// builderBetaRoleGrantsFor returns what the role grants on the draft of
// another user with the given resource name.
func builderBetaRoleGrantsFor(role rbac.Role, name string) builderBetaRoleGrants {
	return builderBetaRoleGrants{
		canList:   builderBetaCrossUserAllowed(role, builderBetaVerbList, name),
		canGet:    builderBetaCrossUserAllowed(role, builderBetaVerbGet, name),
		canUpdate: builderBetaCrossUserAllowed(role, builderBetaVerbUpdate, name),
		canDelete: builderBetaCrossUserAllowed(role, builderBetaVerbDelete, name),
	}
}

// builderBetaHoldsRoleGrants reports whether the role grants anything on any
// draft of another user, so callers can skip the checks by name otherwise.
func builderBetaHoldsRoleGrants(role rbac.Role) bool {
	return builderBetaCrossUserAllowed(role, builderBetaVerbList) ||
		builderBetaCrossUserAllowed(role, builderBetaVerbGet) ||
		builderBetaCrossUserAllowed(role, builderBetaVerbUpdate) ||
		builderBetaCrossUserAllowed(role, builderBetaVerbDelete)
}

// allowed reports whether the grants include the verb.
func (g builderBetaRoleGrants) allowed(verb builderBetaVerb) bool {
	switch verb {
	case builderBetaVerbList:
		return g.canList
	case builderBetaVerbGet:
		return g.canGet
	case builderBetaVerbUpdate:
		return g.canUpdate
	case builderBetaVerbDelete:
		return g.canDelete
	case builderBetaVerbCreate, builderBetaVerbShare:
		return false
	}

	return false
}

// visible reports whether the grants let the caller see the draft when asking
// to perform the verb.
func (g builderBetaRoleGrants) visible(verb builderBetaVerb) bool {
	return g.canList || g.canGet || g.allowed(verb)
}

// level returns the level the grants amount to: update is edit, get or list
// is view.
func (g builderBetaRoleGrants) level() builderBetaLevel {
	switch {
	case g.canUpdate:
		return builderBetaLevelEdit
	case g.canGet || g.canList:
		return builderBetaLevelView
	}

	return builderBetaLevelNone
}

// owner reports whether the caller owns the draft.
func (a builderBetaAccess) owner() bool {
	return a.level == builderBetaLevelOwner
}

// visible reports whether the caller may know the draft exists when asking to
// perform the verb: it owns the draft, a share names it, or its role grants
// list, get or the verb itself.
func (a builderBetaAccess) visible(verb builderBetaVerb) bool {
	return a.owner() || a.via == builderBetaViaShare || a.rbac.visible(verb)
}

// allows reports whether the caller may perform the verb. A share grants list
// and get, and update when it is an edit share; it never grants delete or
// sharing.
func (a builderBetaAccess) allows(verb builderBetaVerb) bool {
	if a.owner() {
		return true
	}

	switch verb {
	case builderBetaVerbList, builderBetaVerbGet:
		return a.via == builderBetaViaShare || a.rbac.allowed(verb)
	case builderBetaVerbUpdate:
		return a.level >= builderBetaLevelEdit
	case builderBetaVerbDelete:
		return a.rbac.canDelete
	case builderBetaVerbCreate, builderBetaVerbShare:
		return false
	}

	return false
}

// denial names why the caller, who may see the draft, may not perform the
// verb, for the security log.
func (a builderBetaAccess) denial(verb builderBetaVerb) string {
	switch {
	case verb == builderBetaVerbShare,
		verb == builderBetaVerbDelete && a.via == builderBetaViaShare:
		return "not-owner"
	case verb == builderBetaVerbUpdate && a.via == builderBetaViaShare:
		return "view-only"
	}

	return "rbac"
}

// name returns the access as responses report it: "owner", "edit", "view",
// or "" for none.
func (a builderBetaAccess) name() string {
	switch a.level {
	case builderBetaLevelOwner:
		return builderBetaAccessOwner
	case builderBetaLevelEdit:
		return string(bapi.ShareEdit)
	case builderBetaLevelView:
		return string(bapi.ShareView)
	case builderBetaLevelNone:
		return ""
	}

	return ""
}

// builderBetaDraftAccess returns the caller's access to a draft, given what
// its role grants on it. The caller's account is read, through account, only
// when a share names the caller: a share applies only while the account it
// was granted to still exists, so it never passes to a new account created
// under the same name.
func builderBetaDraftAccess(
	actor builderBetaActor,
	meta *bapi.DraftMetadata,
	grants builderBetaRoleGrants,
	account func() (string, bool, error),
) (builderBetaAccess, error) {
	if meta.Owner == actor.user {
		return builderBetaAccess{
			level: builderBetaLevelOwner,
			via:   "",
			rbac:  builderBetaRoleGrants{canList: false, canGet: false, canUpdate: false, canDelete: false},
			stale: false,
		}, nil
	}

	access := builderBetaAccess{level: grants.level(), via: "", rbac: grants, stale: false}

	if grants.canList || grants.canGet || grants.canUpdate || grants.canDelete {
		access.via = builderBetaViaRole
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

	access.via = builderBetaViaShare

	share := builderBetaLevelView
	if entry.Access == bapi.ShareEdit {
		share = builderBetaLevelEdit
	}

	access.level = max(access.level, share)

	return access, nil
}

// accountCreated returns metadata.created of the User config of the named
// user, and whether the user has an account a share can be bound to. A name
// that cannot name a config, a missing config and one without a creation
// time are all no account.
func (b *builderBetaAPI) accountCreated(name string) (string, bool, error) {
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

// accountOnce returns [builderBetaAPI.accountCreated] for the named user,
// read at most once however often it is called.
func (b *builderBetaAPI) accountOnce(name string) func() (string, bool, error) {
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
func (b *builderBetaAPI) canShare(actor builderBetaActor, account func() (string, bool, error)) (bool, error) {
	if !builderBetaBaseAllowed(actor.role, builderBetaVerbShare) {
		return false, nil
	}

	_, exists, err := account()

	return exists, err
}

// builderBetaDraftName is the RBAC resource name of a draft.
func builderBetaDraftName(owner, draftID string) string {
	return owner + "/" + draftID
}

// builderBetaForbidden returns the 403 every failed permission check answers
// with. The action is a short description of the attempted operation; request
// bodies are never included.
func builderBetaForbidden(actor builderBetaActor, action string) *weberror.WebError {
	plog.Warn(
		plog.TypeSecurity,
		"builder flow request not allowed",
		"user",
		actor.user,
		"action",
		action,
	)

	return builderBetaNotAllowed(actor, action)
}

// builderBetaNotAllowed returns the 403 of [builderBetaForbidden] without
// logging it, for callers that log the refusal themselves.
func builderBetaNotAllowed(actor builderBetaActor, action string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s not allowed for %s", action, actor.user).
		SetStatus(http.StatusForbidden)
}

// builderBetaWarnDenied logs a refused request for another user's draft. The
// reason tells refusals apart: "no-access" or "stale-share" for a draft the
// caller may not see, answered with 404, and "view-only", "not-owner" or
// "rbac" for one it may see but not change this way, answered with 403.
func builderBetaWarnDenied(actor builderBetaActor, owner, action, reason string) {
	plog.Warn(
		plog.TypeSecurity,
		"builder flow cross-user draft request not allowed",
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

// builderBetaNotFound returns the 404 answering both a missing resource and a
// cross-user request the caller is not allowed to make, so the two are
// indistinguishable to the client.
func builderBetaNotFound(kind, name string) *weberror.WebError {
	return weberror.NewWebError(nil, "%s %s not found", kind, name).
		SetStatus(http.StatusNotFound)
}

// builderBetaWebError maps a [phenix/api/builder] error to the HTTP status it
// corresponds to. Cleanup failures must be handled by the caller before this is
// reached: they follow a durable, successful mutation. A write refused because
// etcd is out of space is answered with 507 by [weberror.ErrorHandler].
func builderBetaWebError(err error, format string, args ...any) *weberror.WebError {
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

// builderBetaMutation resolves the pair a draft mutation returns.
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
func builderBetaMutation[T any](
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
		return nil, builderBetaWebError(err, format, args...)
	}

	builderBetaWarnCleanup(w, err, operation, user)

	return result, nil
}

// builderBetaWarnCleanup reports a cleanup failure that followed a durable,
// successful mutation. The request still succeeded, so it is not failed, but
// the failure is never dropped silently: it is logged with its cause and
// announced on the response with a warning that names the operation only, so
// nothing about the store is disclosed to the client.
func builderBetaWarnCleanup(w http.ResponseWriter, err error, operation, user string) {
	plog.Error(
		plog.TypeSystem,
		"builder flow cleanup failed after a successful mutation",
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

// builderBetaIfMatch returns the entity tag a mutation must match. Every
// mutation after creation requires exactly one strong, quoted tag: wildcards
// and weak tags are rejected so a stale client can never overwrite a draft it
// has not seen.
func builderBetaIfMatch(r *http.Request) (string, error) {
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

// builderBetaCheckIfMatch fails the request when the caller's entity tag does
// not name the revision the draft is currently at.
func builderBetaCheckIfMatch(ifMatch string, meta *bapi.DraftMetadata) error {
	if ifMatch == meta.ETag() {
		return nil
	}

	return weberror.NewWebError(nil, "draft %s has changed since it was last read", meta.ID).
		SetStatus(http.StatusPreconditionFailed)
}

// builderBetaDecode strictly decodes a JSON request body into target. Unknown
// fields, trailing content, and bodies beyond [builderBetaMaxRequestBytes] are
// rejected. The body itself is never logged.
func builderBetaDecode(w http.ResponseWriter, r *http.Request, target any) error {
	return builderBetaDecodeLimit(w, r, target, builderBetaMaxRequestBytes)
}

// builderBetaDecodeLimit is [builderBetaDecode] for bodies of at most limit
// bytes.
func builderBetaDecodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError

		if errors.As(err, &tooLarge) {
			return weberror.NewWebError(nil, "request body is larger than %d bytes", limit).
				SetStatus(http.StatusRequestEntityTooLarge)
		}

		return weberror.NewWebError(err, "request body is not a valid Builder Flow request").
			SetStatus(http.StatusBadRequest)
	}

	if decoder.More() {
		return weberror.NewWebError(nil, "request body carries more than one JSON value").
			SetStatus(http.StatusBadRequest)
	}

	return nil
}

// builderBetaDocumentBytes returns the document bytes of a request. The
// document itself is decoded, validated, and canonicalized by
// [phenix/api/builder], which is the only component that decides what may be
// stored, so this only rejects a missing document.
func builderBetaDocumentBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, weberror.NewWebError(nil, "a builder document is required").
			SetStatus(http.StatusBadRequest)
	}

	return raw, nil
}

// builderBetaWriteJSON writes a JSON response, optionally tagged with the
// entity tag of the draft it represents.
func builderBetaWriteJSON(w http.ResponseWriter, status int, etag string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return weberror.NewWebError(err, "unable to encode the Builder Flow response").
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

// routes registers every Builder Beta route on the given router. Each route
// is also described in web/public/docs/openapi.yml, which
// TestBuilderBetaRoutesDocumented checks.
func (b *builderBetaAPI) routes(router *mux.Router) {
	const (
		draftPath     = "/builder/drafts/{owner}/{draft}"
		snapshotsPath = draftPath + "/snapshots"
	)

	router.Handle("/schemas/builder/v1", weberror.ErrorHandler(b.getSchema)).
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
	router.Handle("/builder/sources", weberror.ErrorHandler(b.listSources)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/generate", weberror.ErrorHandler(b.generateDocument)).
		Methods("POST", "OPTIONS")
	router.Handle("/builder/documents", weberror.ErrorHandler(b.listDocuments)).
		Methods("GET", "OPTIONS")
	router.Handle("/builder/documents/{document}", weberror.ErrorHandler(b.getDocument)).
		Methods("GET", "OPTIONS")
}

// getSchema - GET /schemas/builder/v1.
func (b *builderBetaAPI) getSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGetSchema")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "getting the builder schema")
	}

	if !actor.role.Allowed("schemas", "get", "builder") {
		return builderBetaForbidden(actor, "getting the builder schema")
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

// draftFor loads the draft named by the request path, enforcing both
// authorization layers. A caller that may not see the draft, a draft that
// does not exist, and a draft whose owner does not match the path all produce
// the same 404; a caller that may see the draft but not perform the verb gets
// 403.
func (b *builderBetaAPI) draftFor(
	r *http.Request,
	actor builderBetaActor,
	verb builderBetaVerb,
	action string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.draftAccessFor(r, actor, verb, action)

	return meta, err
}

// draftAccessFor is [builderBetaAPI.draftFor] that also returns the caller's
// access to the draft.
func (b *builderBetaAPI) draftAccessFor(
	r *http.Request,
	actor builderBetaActor,
	verb builderBetaVerb,
	action string,
) (*bapi.DraftMetadata, builderBetaAccess, error) {
	vars := mux.Vars(r)

	return b.namedDraftAccess(r, actor, verb, action, vars["owner"], vars["draft"])
}

// namedDraft is [builderBetaAPI.draftFor] for a draft named by its owner and
// ID rather than by the request path.
func (b *builderBetaAPI) namedDraft(
	r *http.Request,
	actor builderBetaActor,
	verb builderBetaVerb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, error) {
	meta, _, err := b.namedDraftAccess(r, actor, verb, action, owner, draftID)

	return meta, err
}

// namedDraftAccess is [builderBetaAPI.draftAccessFor] for a draft named by its
// owner and ID rather than by the request path.
//
// Who a draft is shared with is part of its record, so the record is read
// before a caller other than the owner is authorized. The reply never tells a
// caller who may not see the draft that it exists: a missing draft, a draft
// of someone else, and a draft whose record this server cannot read all
// produce the same 404 for such a caller.
func (b *builderBetaAPI) namedDraftAccess(
	r *http.Request,
	actor builderBetaActor,
	verb builderBetaVerb,
	action, owner, draftID string,
) (*bapi.DraftMetadata, builderBetaAccess, error) {
	var none builderBetaAccess

	name := builderBetaDraftName(owner, draftID)

	if !builderBetaBaseAllowed(actor.role, verb) {
		return nil, none, builderBetaForbidden(actor, action)
	}

	if owner == "" || len(owner) > builderBetaMaxOwnerLength || !builderBetaIDPattern.MatchString(draftID) {
		return nil, none, builderBetaNotFound("draft", name)
	}

	var grants builderBetaRoleGrants
	if owner != actor.user {
		grants = builderBetaRoleGrantsFor(actor.role, name)
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
			return nil, none, builderBetaNotFound("draft", name)
		case verb == builderBetaVerbDelete:
			meta, err = stored, nil
		case owner != actor.user && !grants.visible(verb):
			return nil, none, builderBetaNotFound("draft", name)
		}
	}

	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, none, builderBetaNotFound("draft", name)
		}

		return nil, none, builderBetaWebError(err, "unable to get draft %s", name)
	}

	// Drafts are keyed by ID alone, so the owner in the path is authoritative:
	// a mismatch means the caller was authorized against an owner that does not
	// hold the draft.
	if meta.Owner != owner {
		return nil, none, builderBetaNotFound("draft", name)
	}

	access, err := builderBetaDraftAccess(actor, meta, grants, b.accountOnce(actor.user))
	if err != nil {
		return nil, none, weberror.NewWebError(err, "unable to check access to draft %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	if !access.visible(verb) {
		reason := "no-access"
		if access.stale {
			reason = "stale-share"
		}

		builderBetaWarnDenied(actor, owner, action, reason)

		return nil, none, builderBetaNotFound("draft", name)
	}

	if !access.allows(verb) {
		builderBetaWarnDenied(actor, owner, action, access.denial(verb))

		return nil, none, builderBetaNotAllowed(actor, action)
	}

	return meta, access, nil
}
