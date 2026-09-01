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
//   - a request touching a draft owned by somebody else additionally needs the
//     "builder-drafts" permission for the "{owner}/{draftID}" resource name.
//
// A cross-user request that fails the second check is answered with 404 rather
// than 403, so draft existence is never disclosed to a user that may not see
// it.
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
)

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

	plog.Info(plog.TypeSystem, "Builder Beta API enabled", "feature", builderBetaFeature)

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
	case builderBetaVerbUpdate:
		return role.Allowed("configs", "update", names...)
	case builderBetaVerbDelete:
		return role.Allowed("configs", "delete", names...)
	}

	return false
}

// builderBetaCrossUserAllowed reports whether the role may operate on a draft
// owned by another user. Creation is missing on purpose: a draft is always
// created for the authenticated user, never on somebody else's behalf.
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
	case builderBetaVerbCreate:
		return false
	}

	return false
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
		"builder beta request not allowed",
		"user",
		actor.user,
		"action",
		action,
	)

	return weberror.NewWebError(nil, "%s not allowed for %s", action, actor.user).
		SetStatus(http.StatusForbidden)
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
// reached: they follow a durable, successful mutation.
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
		"builder beta cleanup failed after a successful mutation",
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, builderBetaMaxRequestBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError

		if errors.As(err, &tooLarge) {
			return weberror.NewWebError(nil, "request body is larger than %d bytes", builderBetaMaxRequestBytes).
				SetStatus(http.StatusRequestEntityTooLarge)
		}

		return weberror.NewWebError(err, "request body is not a valid Builder Beta request").
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
		return weberror.NewWebError(err, "unable to encode the Builder Beta response").
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

// routes registers every Builder Beta route on the given router.
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
// authorization layers. A caller that may not see another user's draft, a draft
// that does not exist, and a draft whose owner does not match the path all
// produce the same 404.
func (b *builderBetaAPI) draftFor(
	r *http.Request,
	actor builderBetaActor,
	verb builderBetaVerb,
	action string,
) (*bapi.DraftMetadata, error) {
	var (
		vars    = mux.Vars(r)
		owner   = vars["owner"]
		draftID = vars["draft"]
		name    = builderBetaDraftName(owner, draftID)
	)

	if !builderBetaBaseAllowed(actor.role, verb) {
		return nil, builderBetaForbidden(actor, action)
	}

	if owner == "" || len(owner) > builderBetaMaxOwnerLength || !builderBetaIDPattern.MatchString(draftID) {
		return nil, builderBetaNotFound("draft", name)
	}

	if owner != actor.user && !builderBetaCrossUserAllowed(actor.role, verb, name) {
		plog.Warn(
			plog.TypeSecurity,
			"builder beta cross-user draft request not allowed",
			"user",
			actor.user,
			"owner",
			owner,
			"action",
			action,
		)

		return nil, builderBetaNotFound("draft", name)
	}

	meta, err := b.drafts.GetDraft(r.Context(), draftID)
	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrInvalid) {
			return nil, builderBetaNotFound("draft", name)
		}

		return nil, builderBetaWebError(err, "unable to get draft %s", name)
	}

	// Drafts are keyed by ID alone, so the owner in the path is authoritative:
	// a mismatch means the caller was authorized against an owner that does not
	// hold the draft.
	if meta.Owner != owner {
		return nil, builderBetaNotFound("draft", name)
	}

	return meta, nil
}
