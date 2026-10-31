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

// Limits and names of the Builder routes.
const (
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

// builderVerb is a config operation that a Builder route needs the config
// permission of (see [builderConfigAllowed]).
type builderVerb string

const (
	builderVerbList   builderVerb = "list"
	builderVerbGet    builderVerb = "get"
	builderVerbCreate builderVerb = "create"
	builderVerbUpdate builderVerb = "update"
	builderVerbDelete builderVerb = "delete"
)

// builderAPI serves the Builder routes, the HTTP API of the Vue Flow builder.
// Only an explicit publish creates or updates configs. Every route needs RBAC
// "use" on "builder" (see [builderAllowed]), and a route that reads or writes
// configs also needs their permissions. Config access, publication effects and
// the file directories are injected, so tests need no real store or directory.
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
	// sweep is when the server removes orphaned Builder storage after its
	// start (see [withBuilderSweep]).
	sweep builderSweep
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
		getConfig:     config.Get,
		publish:       newBuilderPublishOps(),
		documentFiles: builderDocumentFiles,
		templateFiles: common.BuilderTemplatesBase,
		disks:         newBuilderDiskLister(builderDiskImages, time.Now),
		appNames:      builderAppNames,
		preflight:     defaultBuilderPreflightSources(),
		sweep:         builderSweep{ctx: nil, every: 0},
	}

	for _, opt := range opts {
		opt(api)
	}

	api.sweepStorage(context.Background())

	if api.sweep.ctx != nil {
		go api.sweepEvery(api.sweep.ctx, api.sweep.every)
	}

	// Read the template collections of the server here, one time. A change to
	// the directory shows at the next start.
	api.drafts.LoadServerTemplates(context.Background(), api.templateFiles)

	return api, nil
}

// builderDocumentFiles returns where this server reads Builder files from: the phenix base directory.
// It excludes the VM mount directory, because guest files there can hang a read.
func builderDocumentFiles() (string, []string) {
	return common.PhenixBase, []string{common.MountDir()}
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

// builderAuthorize returns the request's actor when it may use the Builder,
// and otherwise the 403 of [builderForbidden].
func builderAuthorize(r *http.Request, action string) (builderActor, error) {
	actor, ok := builderRequestActor(r)
	if !ok || !builderAllowed(actor.role) {
		return actor, builderForbidden(actor, action)
	}

	return actor, nil
}

// builderAuthorizeConfig is [builderAuthorize] for a route that also needs
// the config permission of verb.
func builderAuthorizeConfig(r *http.Request, verb builderVerb, action string) (builderActor, error) {
	actor, err := builderAuthorize(r, action)
	if err == nil && !builderConfigAllowed(actor.role, verb) {
		return actor, builderForbidden(actor, action)
	}

	return actor, err
}

// builderAllowed reports whether the role may use the Builder: read and
// change every draft, template, icon and layer set. It is the only check of
// the builder resource, and a literal call so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records it.
func builderAllowed(role rbac.Role) bool {
	return role.Allowed("builder", "use")
}

// builderConfigAllowed reports whether the role holds the config permission
// of verb, for the named configs or for any. Each check is a literal call so
// the RBAC policy generator records it.
func builderConfigAllowed(role rbac.Role, verb builderVerb, names ...string) bool {
	switch verb {
	case builderVerbList:
		return role.Allowed("configs", "list", names...)
	case builderVerbGet:
		return role.Allowed("configs", "get", names...)
	case builderVerbCreate:
		return role.Allowed("configs", "create", names...)
	case builderVerbUpdate:
		return role.Allowed("configs", "update", names...)
	case builderVerbDelete:
		return role.Allowed("configs", "delete", names...)
	}

	return false
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
	return weberror.NewWebError(nil, "%s: user %s does not have permission", action, actor.user).
		SetStatus(http.StatusForbidden)
}

// builderNotFound returns the 404 for a missing resource, and for a config
// the caller may not read. Thus the client cannot tell the two apart.
func builderNotFound(kind, name string) *weberror.WebError {
	return weberror.NewWebError(nil, "phenix cannot find %s %s", kind, name).
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

// builderCodedError gives a Builder route error its failure code (see [bdoc.Code]) and its issues, unless it has a code.
// It does not change an error that is not a [weberror.WebError]. Such an error gets 500 and no body.
func builderCodedError(err error) error {
	if err == nil {
		return nil
	}

	web := &weberror.WebError{} //nolint:exhaustruct // filled by errors.As

	if !errors.As(err, &web) {
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

// builderErrorCode is the failure code for a Builder route that names no code itself.
// It picks the first that applies: a package, document, template file or layer set error, other issues, then the status.
func builderErrorCode(err error, status int, issues []bdoc.Issue) bdoc.Code {
	var (
		templateFile *bdoc.TemplateFileError
		layerSet     *bdoc.LayerSetError
	)

	switch {
	case errors.Is(err, bdoc.ErrInvalidPackage):
		return bdoc.CodePackageInvalid
	case errors.Is(err, bdoc.ErrInvalidDocument):
		return bdoc.CodeDocumentInvalid
	case errors.As(err, &templateFile):
		return bdoc.CodeTemplateFileInvalid
	case errors.As(err, &layerSet):
		return bdoc.CodeLayerSetInvalid
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
	}

	if status >= http.StatusInternalServerError {
		return bdoc.CodeServerError
	}

	return bdoc.CodeRequestInvalid
}

// builderWebError maps a [phenix/api/builder] error to its HTTP status. The
// caller must handle cleanup failures before it calls this function,
// because they follow a durable, successful mutation.
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
		fmt.Sprintf("199 phenix %q", operation+" succeeded, but phenix could not remove the superseded content"),
	)
}

// builderIfMatch returns the entity tag a mutation must match. Every
// mutation after creation requires exactly one strong, quoted tag. It
// rejects wildcards and weak tags, so a stale client can never overwrite a
// draft it has not seen.
func builderIfMatch(r *http.Request) (string, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))

	if value == "" {
		return "", weberror.NewWebError(nil, "this request needs an If-Match header").
			SetStatus(http.StatusBadRequest)
	}

	if !builderStrongETag(value) {
		return "", weberror.NewWebError(nil, "the If-Match header must be a single quoted entity tag").
			SetStatus(http.StatusBadRequest)
	}

	return value, nil
}

// builderStrongETag reports whether value is one strong, quoted entity tag,
// as [builderIfMatch] requires.
func builderStrongETag(value string) bool {
	return len(value) > 2 &&
		strings.HasPrefix(value, `"`) &&
		strings.HasSuffix(value, `"`) &&
		!strings.Contains(value[1:len(value)-1], `"`)
}

// builderCheckIfMatch fails the request when the caller's entity tag does
// not name the revision the draft is currently at. The 412 carries the
// current ETag, as the caller may read the draft.
func builderCheckIfMatch(w http.ResponseWriter, ifMatch string, meta *bapi.DraftMetadata) error {
	if ifMatch == meta.ETag() {
		return nil
	}

	w.Header().Set("ETag", meta.ETag())

	return weberror.NewWebError(nil, "draft %s changed after you last read it", meta.ID).
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
		return nil, weberror.NewWebError(nil, "the request needs a Builder document").
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

	// builderCacheControl is the Cache-Control of every response under the
	// Builder paths: no browser or proxy keeps a copy.
	builderCacheControl = "no-store"
)

// builderResponseHeaders sets the security headers of every response under the Builder paths, by request path.
// Responses get nosniff and no-store, because drafts change and a cached copy can outlive its session.
// Icon library responses carry uploaded images, so they also get a Content-Security-Policy that allows nothing.
func builderResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if builderPath(r.URL.Path) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", builderCacheControl)
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
// template file, package and layer set file formats.
func builderPath(path string) bool {
	rest, ok := strings.CutPrefix(path, builderAPIPrefix)

	return ok && (strings.HasPrefix(rest, builderRoutePrefix) || rest == builderSchemaPath ||
		rest == builderTemplateSchemaPath || rest == builderPackageSchemaPath || rest == builderLayerSetSchemaPath)
}

// builderMethodNotAllowed answers a request that matches a route path but none of its methods.
// Under the Builder paths it answers 405 with Allow and a refusal code. Other paths get the status alone.
func builderMethodNotAllowed(router *mux.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !builderPath(r.URL.Path) {
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		if allowed := builderAllowedMethods(router, r); len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}

		refusal := weberror.NewWebError(nil, "this path does not allow the %s method", r.Method).
			WithCode(string(bdoc.CodeRequestMethodNotAllowed))

		_ = builderWriteJSON(w, http.StatusMethodNotAllowed, "", refusal)
	}
}

// builderAllowedMethods returns the methods router has a route for at the
// path of r, in the order Allow names them. When there is one, it also
// returns OPTIONS, which [builderOptions] answers for every such path.
func builderAllowedMethods(router *mux.Router, r *http.Request) []string {
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete,
	}
	allowed := make([]string, 0, len(methods)+1)

	for _, method := range methods {
		probe := r.Clone(r.Context())
		probe.Method = method

		var match mux.RouteMatch

		if router.Match(probe, &match) && match.MatchErr == nil {
			allowed = append(allowed, method)
		}
	}

	if len(allowed) == 0 {
		return allowed
	}

	return append(allowed, http.MethodOptions)
}

// builderOptionsRequest returns the matcher of the one route that answers
// OPTIONS under the Builder paths: an OPTIONS request for a path that router
// has a route for. It checks the method first, so other requests cost
// nothing more.
func builderOptionsRequest(router *mux.Router) mux.MatcherFunc {
	return func(r *http.Request, _ *mux.RouteMatch) bool {
		return r.Method == http.MethodOptions && builderPath(r.URL.Path) &&
			len(builderAllowedMethods(router, r)) > 0
	}
}

// builderOptions answers OPTIONS for a Builder path with 204 and the Allow
// header of the path. No Builder route has OPTIONS among its methods, so no
// handler runs for an OPTIONS request: it reads nothing, writes nothing, and
// checks no permission.
func builderOptions(router *mux.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", strings.Join(builderAllowedMethods(router, r), ", "))
		w.WriteHeader(http.StatusNoContent)
	}
}

// builderDraftPath is the path of one draft. It is also the start of the
// paths of its snapshots, cursor, publication and preflight checks.
const builderDraftPath = "/builder/drafts/{draft}"

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

	// It comes first, so OPTIONS never reaches a route registered after it,
	// such as /schemas/{kind}/{version} for /schemas/builder/v1.
	router.MatcherFunc(builderOptionsRequest(router)).Handler(builderOptions(router))

	b.schemaRoutes(router)
	b.draftRoutes(router)
	b.publishingRoutes(router)
	b.sourceRoutes(router)
	b.documentRoutes(router)
	b.iconRoutes(router)
	b.templateRoutes(router)
	b.layerSetRoutes(router)
	b.storageRoutes(router)
}

// schemaRoutes registers the schema routes of the document, template file,
// package and layer set file formats.
func (b *builderAPI) schemaRoutes(router *mux.Router) {
	schemas := []struct {
		path, handler, name string
		schema              func() ([]byte, error)
	}{
		{builderSchemaPath, "BuilderGetSchema", "Builder schema", bdoc.SchemaJSON},
		{builderTemplateSchemaPath, "BuilderGetTemplateSchema", "schema of Builder template files", bdoc.TemplateFileSchemaJSON},
		{builderPackageSchemaPath, "BuilderGetPackageSchema", "schema of Builder packages", bdoc.PackageSchemaJSON},
		{builderLayerSetSchemaPath, "BuilderGetLayerSetSchema", "schema of Builder layer set files", bdoc.LayerSetFileSchemaJSON},
	}

	for _, s := range schemas {
		router.Handle(s.path, builderHandler(builderSchemaHandler(s.handler, s.name, s.schema))).
			Methods("GET")
	}
}

// builderSchemaHandler returns the handler of a GET route that sends the
// JSON Schema that schema builds. name is the schema's name in messages.
func builderSchemaHandler(handler, name string, schema func() ([]byte, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", handler)

		if _, err := builderAuthorize(r, "getting the "+name); err != nil {
			return err
		}

		body, err := schema()
		if err != nil {
			return weberror.NewWebError(err, "unable to build the %s", name).
				SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", mimeJSON)

		_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

		return nil
	}
}

// draftRoutes registers the routes of drafts, their snapshots and their
// cursor.
func (b *builderAPI) draftRoutes(router *mux.Router) {
	const snapshotsPath = builderDraftPath + "/snapshots"

	router.Handle("/builder/drafts", builderHandler(b.listDrafts)).
		Methods("GET")
	router.Handle("/builder/drafts", builderHandler(b.createDraft)).
		Methods("POST")
	router.Handle(builderDraftPath, builderHandler(b.getDraft)).
		Methods("GET")
	router.Handle(builderDraftPath, builderHandler(b.deleteDraft)).
		Methods("DELETE")
	router.Handle(snapshotsPath, builderHandler(b.listSnapshots)).
		Methods("GET")
	router.Handle(snapshotsPath, builderHandler(b.createSnapshot)).
		Methods("POST")
	router.Handle(snapshotsPath+"/{snapshot}", builderHandler(b.getSnapshot)).
		Methods("GET")
	router.Handle(snapshotsPath+"/{snapshot}", builderHandler(b.deleteSnapshot)).
		Methods("DELETE")
	router.Handle(builderDraftPath+"/cursor", builderHandler(b.updateCursor)).
		Methods("PATCH")
}

// publishingRoutes registers the routes that publish a draft, show what a
// publish changes (the same route, with dryRun), and run the preflight
// checks of a draft.
func (b *builderAPI) publishingRoutes(router *mux.Router) {
	router.Handle(builderDraftPath+"/publish", builderHandler(b.publishDraft)).
		Methods("POST")
	router.Handle(builderDraftPath+"/preflight", builderHandler(b.preflightDraft)).
		Methods("POST")
}

// sourceRoutes registers the routes that list the source configs of a
// document, generate a document, convert a legacy diagram, export a
// topology, and build or resolve a package.
func (b *builderAPI) sourceRoutes(router *mux.Router) {
	router.Handle("/builder/sources", builderHandler(b.listSources)).
		Methods("GET")
	router.Handle("/builder/generate", builderHandler(b.generateDocument)).
		Methods("POST")
	router.Handle("/builder/legacy", builderHandler(b.convertLegacy)).
		Methods("POST")
	router.Handle("/builder/export/topology", builderHandler(b.exportTopology)).
		Methods("POST")
	router.Handle("/builder/package", builderHandler(b.buildPackage)).
		Methods("POST")
	router.Handle("/builder/package/resolve", builderHandler(b.resolvePackage)).
		Methods("POST")
}

// documentRoutes registers the routes of published documents.
func (b *builderAPI) documentRoutes(router *mux.Router) {
	router.Handle("/builder/documents", builderHandler(b.listDocuments)).
		Methods("GET")
	router.Handle("/builder/documents/{document}", builderHandler(b.getDocument)).
		Methods("GET")
	router.Handle("/builder/documents/{document}", builderHandler(b.deleteDocument)).
		Methods("DELETE")
	router.Handle("/builder/topologies/{topology}/document", builderHandler(b.getTopologyDocument)).
		Methods("GET")
}

// iconRoutes registers the routes of the icon library.
func (b *builderAPI) iconRoutes(router *mux.Router) {
	router.Handle(builderIconsPath, builderHandler(b.listIcons)).
		Methods("GET")
	router.Handle(builderIconsPath, builderHandler(b.createIcon)).
		Methods("POST")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.getIcon)).
		Methods("GET")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.renameIcon)).
		Methods("PUT")
	router.Handle(builderIconsPath+"/{icon}", builderHandler(b.deleteIcon)).
		Methods("DELETE")
}

// templateRoutes registers the routes of the template library of the server.
func (b *builderAPI) templateRoutes(router *mux.Router) {
	router.Handle(builderTemplatesPath, builderHandler(b.listTemplates)).
		Methods("GET")
	router.Handle(builderTemplatesPath+"/items", builderHandler(b.createTemplates)).
		Methods("POST")
	router.Handle(builderTemplatesPath+"/items/{template}", builderHandler(b.putTemplate)).
		Methods("PUT")
	router.Handle(builderTemplatesPath+"/collections", builderHandler(b.createTemplateCollection)).
		Methods("POST")
	router.Handle(builderTemplatesPath+"/collections/{collection}", builderHandler(b.putTemplateCollection)).
		Methods("PUT")
	router.Handle(builderTemplatesPath+"/delete", builderHandler(b.deleteTemplates)).
		Methods("POST")
}

// draftFor returns the request's actor and the draft its path names. Every
// Builder user may read and change every draft, so the only checks are the
// Builder permission and that the draft exists.
func (b *builderAPI) draftFor(r *http.Request, action string) (builderActor, *bapi.DraftMetadata, error) {
	actor, err := builderAuthorize(r, action)
	if err != nil {
		return actor, nil, err
	}

	meta, err := b.pathDraft(r, false)

	return actor, meta, err
}

// pathDraft loads the draft that the request path names (see
// [builderAPI.namedDraft]).
func (b *builderAPI) pathDraft(r *http.Request, damaged bool) (*bapi.DraftMetadata, error) {
	return b.namedDraft(r.Context(), mux.Vars(r)["draft"], damaged)
}

// namedDraft loads the draft with the given ID. When damaged is set, it also
// loads a draft whose metadata this server can no longer read (see
// [bapi.Service.GetDamagedDraft]), for a delete, which is all that is left
// for such a draft. A missing draft and an ID that cannot name one are both
// 404.
func (b *builderAPI) namedDraft(ctx context.Context, draftID string, damaged bool) (*bapi.DraftMetadata, error) {
	if !bapi.ValidID(draftID) {
		return nil, builderNotFound("draft", draftID)
	}

	meta, err := b.drafts.GetDraft(ctx, draftID)
	if damaged && errors.Is(err, bapi.ErrCorrupt) {
		meta, err = b.drafts.GetDamagedDraft(ctx, draftID)
	}

	switch {
	case err == nil:
		return meta, nil
	case errors.Is(err, bapi.ErrNotFound), errors.Is(err, bapi.ErrInvalid):
		return nil, builderNotFound("draft", draftID)
	}

	return nil, builderWebError(err, "unable to get draft %s", draftID)
}
