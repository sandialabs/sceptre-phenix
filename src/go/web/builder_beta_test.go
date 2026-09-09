package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const (
	builderBetaTestOwner = "alice"
	builderBetaTestPeer  = "bob"
)

// builderBetaSPABody is what the router level NotFoundHandler serves in
// production: the SPA index. A Builder Beta request must never reach it.
const builderBetaSPABody = "<!doctype html><title>phenix</title>"

// builderBetaHarness bundles a router serving the Builder Beta routes with the
// fakes backing it.
type builderBetaHarness struct {
	t                 *testing.T
	router            *mux.Router
	api               *mux.Router
	service           *bapi.Service
	store             *builderBetaFakeStore
	configs           store.Configs
	failConfigKind    string
	failBroadcastKind string
	failExperiment    bool
	configWrites      int
	experimentWrites  int
}

// newBuilderBetaRouter returns a router wired the way [Start] wires the real
// one: a root router whose NotFoundHandler serves the SPA index, and an
// "/api/v1" subrouter whose NotFoundHandler answers with JSON.
func newBuilderBetaRouter() (*mux.Router, *mux.Router) {
	router := mux.NewRouter().StrictSlash(true)

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(builderBetaSPABody))
	})

	api := router.PathPrefix("/api/v1").Subrouter()
	api.NotFoundHandler = apiNotFoundHandler()

	return router, api
}

// builderBetaRequest describes a request to the harness.
type builderBetaRequest struct {
	method  string
	path    string
	body    string
	user    string
	role    *rbac.Role
	ifMatch string
}

// newBuilderBetaHarness returns a harness whose routes are registered with the
// "builder-beta" feature enabled. Tests using it cannot run in parallel: route
// registration reads the package level server options.
func newBuilderBetaHarness(t *testing.T, configs ...store.Config) *builderBetaHarness {
	t.Helper()

	builderBetaSetFeatures(t, builderBetaFeature)

	var (
		fake  = newBuilderBetaFakeStore()
		clock = new(atomic.Int64)
		ids   = new(atomic.Int64)
	)

	service, err := bapi.New(
		bapi.WithStore(fake),
		bapi.WithChunkSize(1024),
		bapi.WithClock(func() time.Time { return builderBetaFakeTime(clock.Add(1)) }),
		bapi.WithIDSource(func() (string, error) {
			return "id-" + strconv.FormatInt(ids.Add(1), 10), nil
		}),
	)
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	router, api := newBuilderBetaRouter()

	harness := &builderBetaHarness{
		t:                 t,
		router:            router,
		api:               api,
		service:           service,
		store:             fake,
		configs:           configs,
		failConfigKind:    "",
		failBroadcastKind: "",
		failExperiment:    false,
		configWrites:      0,
		experimentWrites:  0,
	}

	options := []builderBetaOption{
		withBuilderBetaService(service),
		withBuilderBetaConfigs(harness.listConfigs, harness.getConfig),
		withBuilderBetaPublishOps(harness.publishOps()),
	}

	if err := registerBuilderBetaRoutes(harness.api, options...); err != nil {
		t.Fatalf("registerBuilderBetaRoutes returned error: %v", err)
	}

	return harness
}

func (h *builderBetaHarness) publishOps() builderBetaPublishOps {
	return builderBetaPublishOps{
		createConfig: func(config *store.Config) (*store.Config, error) {
			if config.Kind == h.failConfigKind {
				return nil, errors.New("injected config write failure")
			}

			if _, err := h.getConfig(config.FullName()); err == nil {
				return nil, store.ErrExist
			}

			h.configs = append(h.configs, *cloneBuilderConfig(config))
			h.configWrites++

			return config, nil
		},
		updateConfig: func(name string, config *store.Config) error {
			if config.Kind == h.failConfigKind {
				return errors.New("injected config write failure")
			}

			for i := range h.configs {
				if h.configs[i].FullName() == name {
					h.configs[i] = *cloneBuilderConfig(config)
					h.configWrites++

					return nil
				}
			}

			return store.ErrNotExist
		},
		createExperiment: func(
			_ context.Context,
			name, topology, scenario string,
			aliases map[string]int,
		) error {
			if h.failExperiment {
				return errors.New("injected experiment write failure")
			}

			config, err := store.NewConfig("Experiment/" + name)
			if err != nil {
				return err
			}

			config.Metadata.Annotations = store.Annotations{"topology": topology}
			if scenario != "" {
				config.Metadata.Annotations["scenario"] = scenario
			}
			config.Spec = map[string]any{"vlans": map[string]any{"aliases": aliases}}
			h.configs = append(h.configs, *config)
			h.experimentWrites++

			return nil
		},
		lockExperiment:   func(string, string) error { return nil },
		unlockExperiment: func(string) {},
		broadcastConfig: func(config *store.Config, _ string) error {
			if config.Kind == h.failBroadcastKind {
				return errors.New("injected broadcast failure")
			}

			return nil
		},
		broadcastExperiment: func(string, string) error { return nil },
	}
}

// builderBetaSetFeatures replaces the package level server options for the
// duration of a test.
func builderBetaSetFeatures(t *testing.T, features ...string) {
	t.Helper()

	previous := o

	t.Cleanup(func() { o = previous })

	o = newServerOptions(ServeWithFeatures(features))
}

// listConfigs is the read-only config lister the API is given.
func (h *builderBetaHarness) listConfigs(kind string) (store.Configs, error) {
	canonical := store.ConfigFullName(kind, "name")
	if canonical == "" {
		return nil, fmt.Errorf("unknown config kind %q", kind)
	}

	wanted := strings.SplitN(canonical, "/", 2)[0]
	configs := store.Configs{}

	for i := range h.configs {
		if h.configs[i].Kind == wanted {
			configs = append(configs, h.configs[i])
		}
	}

	return configs, nil
}

// getConfig is the read-only config getter the API is given.
func (h *builderBetaHarness) getConfig(name string) (*store.Config, error) {
	for i := range h.configs {
		if h.configs[i].FullName() == name {
			config := h.configs[i]

			return &config, nil
		}
	}

	return nil, store.ErrNotExist
}

// do performs a request against the harness router.
func (h *builderBetaHarness) do(request builderBetaRequest) *httptest.ResponseRecorder {
	h.t.Helper()

	var body io.Reader

	if request.body != "" {
		body = strings.NewReader(request.body)
	}

	req := httptest.NewRequest(request.method, "/api/v1"+request.path, body)

	if request.user != "" {
		role := builderBetaFullRole()
		if request.role != nil {
			role = *request.role
		}

		ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, request.user)
		ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
		req = req.WithContext(ctx)
	}

	if request.ifMatch != "" {
		req.Header.Set("If-Match", request.ifMatch)
	}

	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, req)

	return recorder
}

// decode unmarshals a recorded response body.
func (h *builderBetaHarness) decode(recorder *httptest.ResponseRecorder, target any) {
	h.t.Helper()

	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		h.t.Fatalf("decoding response: %v (body %d bytes)", err, recorder.Body.Len())
	}
}

// createDraft creates a draft owned by the given user and returns it.
func (h *builderBetaHarness) createDraft(user, name string) builderDraftResponse {
	h.t.Helper()

	recorder := h.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"title":"` + name + `","document":` + string(builderBetaDocument(h.t, name)) + `}`,
		user:   user,
	})

	if recorder.Code != http.StatusCreated {
		h.t.Fatalf("creating draft: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	var draft builderDraftResponse

	h.decode(recorder, &draft)

	return draft
}

// builderBetaRole returns a role holding exactly the given policies.
func builderBetaRole(policies ...*v1.PolicySpec) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Name: "test", Policies: policies}}
}

// builderBetaPolicy returns one policy.
func builderBetaPolicy(resources, names, verbs []string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
}

// builderBetaFullRole may do everything, including operating on the drafts of
// other users. It mirrors the wildcards the global-admin role config uses.
func builderBetaFullRole() rbac.Role {
	return builderBetaRole(builderBetaPolicy(
		[]string{"*", "*/*"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	))
}

// builderBetaOwnerRole may do everything with its own drafts and configs, but
// holds no cross-user draft permission at all. The resources are enumerated so
// no wildcard can grant "builder-drafts" by accident.
func builderBetaOwnerRole() rbac.Role {
	return builderBetaRole(builderBetaPolicy(
		[]string{"configs", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	))
}

// builderBetaDocument returns a small, valid builder document.
func builderBetaDocument(t *testing.T, name string) []byte {
	t.Helper()

	document := bdoc.NewDocument(name)

	document.Nodes = append(document.Nodes, bdoc.Node{
		ID:       bdoc.NoteNodeID(name),
		Kind:     bdoc.NodeKindNote,
		Position: bdoc.Position{X: 0, Y: 0},
		Note:     &bdoc.Note{Text: name, Color: ""},
	})

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	return data
}

func TestBuilderBetaFeatureGate(t *testing.T) { //nolint:paralleltest // mutates package options
	paths := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/schemas/builder/v1"},
		{method: http.MethodGet, path: "/builder/drafts"},
		{method: http.MethodPost, path: "/builder/drafts"},
		{method: http.MethodGet, path: "/builder/drafts/alice/id-1"},
		{method: http.MethodDelete, path: "/builder/drafts/alice/id-1"},
		{method: http.MethodGet, path: "/builder/drafts/alice/id-1/snapshots"},
		{method: http.MethodPost, path: "/builder/drafts/alice/id-1/snapshots"},
		{method: http.MethodGet, path: "/builder/drafts/alice/id-1/snapshots/current"},
		{method: http.MethodPatch, path: "/builder/drafts/alice/id-1/cursor"},
		{method: http.MethodPut, path: "/builder/drafts/alice/id-1/cursor"},
		{method: http.MethodGet, path: "/builder/sources"},
		{method: http.MethodPost, path: "/builder/generate"},
		{method: http.MethodGet, path: "/builder/documents"},
		{method: http.MethodGet, path: "/builder/documents/doc-1"},
		// Publishing is not implemented yet and must not appear to be.
		{method: http.MethodPost, path: "/builder/drafts/alice/id-1/publish"},
	}

	builderBetaSetFeatures(t)

	router, api := newBuilderBetaRouter()

	if err := registerBuilderBetaRoutes(api); err != nil {
		t.Fatalf("registerBuilderBetaRoutes returned error: %v", err)
	}

	for _, tt := range paths {
		req := httptest.NewRequest(tt.method, "/api/v1"+tt.path, nil)
		ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, builderBetaTestOwner)
		ctx = context.WithValue(ctx, middleware.ContextKeyRole, builderBetaFullRole())

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req.WithContext(ctx))

		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want %d with the feature off",
				tt.method, tt.path, recorder.Code, http.StatusNotFound)
		}

		// The SPA index must never answer an API request: a client would read
		// 200 HTML as a working, enabled route.
		if body := recorder.Body.String(); strings.Contains(body, builderBetaSPABody) {
			t.Errorf("%s %s: the SPA index answered an API request", tt.method, tt.path)
		}

		if contentType := recorder.Header().Get("Content-Type"); contentType != mimeJSON {
			t.Errorf("%s %s: Content-Type = %q, want %q",
				tt.method, tt.path, contentType, mimeJSON)
		}
	}

	// The SPA fallback still answers everything outside the API.
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/builder-beta", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), builderBetaSPABody) {
		t.Errorf("UI route: status = %d, want the SPA index", recorder.Code)
	}
}

func TestBuilderBetaPublishTopology(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID + "/publish",
		body:    `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)

	if response.Status != "succeeded" || len(response.Stages) != 3 {
		t.Fatalf("publish response = %#v", response)
	}

	topology, err := harness.getConfig("Topology/topo")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	reference, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("builder-doc annotation invalid: %v", err)
	}

	if reference.SnapshotID != draft.SnapshotID {
		t.Fatalf("published snapshot = %q, want %q", reference.SnapshotID, draft.SnapshotID)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Dirty() || meta.Publication == nil || meta.Publication.DocumentID != reference.ID {
		t.Fatalf("draft publication = %#v", meta.Publication)
	}
}

func TestBuilderBetaSchema(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/schemas/builder/v1",
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var schema map[string]any

	harness.decode(recorder, &schema)

	if len(schema) == 0 {
		t.Fatal("schema is empty")
	}
}

func TestBuilderBetaUnauthenticated(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	requests := []builderBetaRequest{
		{method: http.MethodGet, path: "/schemas/builder/v1"},
		{method: http.MethodGet, path: "/builder/drafts"},
		{method: http.MethodPost, path: "/builder/drafts", body: `{}`},
		{method: http.MethodGet, path: "/builder/drafts/alice/id-1"},
		{method: http.MethodGet, path: "/builder/sources"},
		{method: http.MethodPost, path: "/builder/generate", body: `{}`},
		{method: http.MethodGet, path: "/builder/documents"},
	}

	for _, request := range requests {
		recorder := harness.do(request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d without an identity",
				request.method, request.path, recorder.Code, http.StatusForbidden)
		}
	}
}

func TestBuilderBetaUnauthorized(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	// A role holding no config permission at all.
	empty := builderBetaRole()

	requests := []builderBetaRequest{
		{method: http.MethodGet, path: "/builder/drafts"},
		{method: http.MethodPost, path: "/builder/drafts", body: `{}`},
		{method: http.MethodGet, path: "/builder/drafts/alice/id-1"},
		{method: http.MethodGet, path: "/builder/sources"},
		{method: http.MethodPost, path: "/builder/generate", body: `{}`},
		{method: http.MethodGet, path: "/builder/documents"},
	}

	for _, request := range requests {
		request.user = builderBetaTestOwner
		request.role = &empty

		recorder := harness.do(request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d without config permission",
				request.method, request.path, recorder.Code, http.StatusForbidden)
		}
	}
}

func TestBuilderBetaCreateDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	document := builderBetaDocument(t, "topo")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"title":"fallback","sourceToken":"Topology/foo","document":` + string(document) + `}`,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var draft builderDraftResponse

	harness.decode(recorder, &draft)

	if draft.Owner != builderBetaTestOwner {
		t.Errorf("owner = %q, want %q", draft.Owner, builderBetaTestOwner)
	}

	// The document names itself, so the request title is only a fallback.
	if draft.Title != "topo" {
		t.Errorf("title = %q, want %q", draft.Title, "topo")
	}

	if draft.ETag == "" || !strings.HasPrefix(draft.ETag, `"`) {
		t.Errorf("etag = %q, want a quoted entity tag", draft.ETag)
	}

	if header := recorder.Header().Get("ETag"); header != draft.ETag {
		t.Errorf("ETag header = %q, want %q", header, draft.ETag)
	}

	if location := recorder.Header().Get("Location"); !strings.HasSuffix(location, draft.ID) {
		t.Errorf("Location = %q, want it to name draft %q", location, draft.ID)
	}
}

func TestBuilderBetaCreateDraftForOtherUser(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	document := builderBetaDocument(t, "topo")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"owner":"` + builderBetaTestPeer + `","document":` + string(document) + `}`,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}

	if count := harness.store.count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}
}

func TestBuilderBetaCreateDraftRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	document := string(builderBetaDocument(t, "topo"))

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "missing document", body: `{"title":"a"}`, status: http.StatusBadRequest},
		{name: "unknown field", body: `{"nope":1}`, status: http.StatusBadRequest},
		{name: "not json", body: `nope`, status: http.StatusBadRequest},
		{
			name:   "trailing value",
			body:   `{"document":` + document + `} {"document":` + document + `}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "invalid document",
			body:   `{"document":{"apiVersion":"builder/v1","kind":"nope"}}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			name:   "oversized body",
			body:   `{"title":"` + strings.Repeat("x", builderBetaMaxRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/drafts",
				body:   tt.body,
				user:   builderBetaTestOwner,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}

	if count := harness.store.count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}
}

func TestBuilderBetaGetDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if etag := recorder.Header().Get("ETag"); etag != draft.ETag {
		t.Errorf("ETag = %q, want %q", etag, draft.ETag)
	}

	var response builderDraftResponse
	harness.decode(recorder, &response)
	if len(response.Document) == 0 || len(response.History) != 1 || response.ReadOnly {
		t.Fatalf("draft envelope is incomplete: %#v", response)
	}
}

func TestBuilderBetaGetDraftNotFound(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")

	paths := []string{
		"/builder/drafts/" + builderBetaTestOwner + "/id-missing",
		// The draft exists, but not under this owner.
		"/builder/drafts/" + builderBetaTestPeer + "/" + draft.ID,
		// Identifiers that cannot name a draft never reach the store.
		"/builder/drafts/" + builderBetaTestOwner + "/bad%20id",
		"/builder/drafts/" + builderBetaTestOwner + "/" + strings.Repeat("x", 200),
	}

	for _, path := range paths {
		recorder := harness.do(builderBetaRequest{
			method: http.MethodGet,
			path:   path,
			user:   builderBetaTestOwner,
		})

		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

// TestBuilderBetaCrossUserNoLeak asserts a user without the "builder-drafts"
// permission cannot tell another user's draft apart from one that does not
// exist, on any route.
func TestBuilderBetaCrossUserNoLeak(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestPeer, "topo")

	owner := builderBetaOwnerRole()
	base := "/builder/drafts/" + builderBetaTestPeer + "/"
	document := string(builderBetaDocument(t, "other"))

	requests := []builderBetaRequest{
		{method: http.MethodGet, path: base + draft.ID},
		{method: http.MethodGet, path: base + draft.ID + "/snapshots"},
		{method: http.MethodGet, path: base + draft.ID + "/snapshots/current"},
		{
			method:  http.MethodPost,
			path:    base + draft.ID + "/snapshots",
			body:    `{"document":` + document + `}`,
			ifMatch: draft.ETag,
		},
		{
			method:  http.MethodPatch,
			path:    base + draft.ID + "/cursor",
			body:    `{"index":0}`,
			ifMatch: draft.ETag,
		},
		{
			method:  http.MethodPost,
			path:    base + draft.ID + "/publish",
			body:    `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
			ifMatch: draft.ETag,
		},
		{method: http.MethodDelete, path: base + draft.ID, ifMatch: draft.ETag},
		// The same requests against an identifier that does not exist must be
		// answered identically.
		{method: http.MethodGet, path: base + "id-missing"},
		{method: http.MethodDelete, path: base + "id-missing", ifMatch: draft.ETag},
	}

	for _, request := range requests {
		request.user = builderBetaTestOwner
		request.role = &owner

		recorder := harness.do(request)

		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want %d",
				request.method, request.path, recorder.Code, http.StatusNotFound)
		}

		if strings.Contains(recorder.Body.String(), builderBetaTestPeer+"'s") {
			t.Errorf("%s %s: response describes the draft owner", request.method, request.path)
		}
	}

	// The draft is untouched.
	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   base + draft.ID,
		user:   builderBetaTestPeer,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("owner GET status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var after builderDraftResponse

	harness.decode(recorder, &after)

	if after.ETag != draft.ETag {
		t.Errorf("etag = %q, want the unchanged %q", after.ETag, draft.ETag)
	}
}

func TestBuilderBetaListDrafts(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	mine := harness.createDraft(builderBetaTestOwner, "mine")
	theirs := harness.createDraft(builderBetaTestPeer, "theirs")

	type listing struct {
		Drafts []builderDraftResponse `json:"drafts"`
		Shared []builderDraftResponse `json:"shared"`
	}

	owner := builderBetaOwnerRole()

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/drafts",
		user:   builderBetaTestOwner,
		role:   &owner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var restricted listing

	harness.decode(recorder, &restricted)

	if len(restricted.Drafts) != 1 || restricted.Drafts[0].ID != mine.ID {
		t.Errorf("drafts = %+v, want only %q", restricted.Drafts, mine.ID)
	}

	if len(restricted.Shared) != 0 {
		t.Errorf("shared = %+v, want none without cross-user permission", restricted.Shared)
	}

	if strings.Contains(recorder.Body.String(), theirs.ID) {
		t.Error("listing leaks another user's draft identifier")
	}

	// A caller holding the cross-user permission sees the shared draft.
	recorder = harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/drafts",
		user:   builderBetaTestOwner,
	})

	var full listing

	harness.decode(recorder, &full)

	if len(full.Shared) != 1 || full.Shared[0].ID != theirs.ID {
		t.Errorf("shared = %+v, want only %q", full.Shared, theirs.ID)
	}
	if full.Shared[0].ReadOnly {
		t.Error("shared draft is read only despite cross-user update permission")
	}
}

// TestBuilderBetaSharedDraftsAreNamed asserts cross-user listing authorizes
// each draft by its "{owner}/{draftID}" name rather than in bulk.
func TestBuilderBetaSharedDraftsAreNamed(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	visible := harness.createDraft(builderBetaTestPeer, "visible")
	hidden := harness.createDraft("carol", "hidden")

	role := builderBetaRole(
		builderBetaPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderBetaPolicy(
			[]string{builderBetaDraftsResource},
			[]string{builderBetaDraftName(builderBetaTestPeer, visible.ID)},
			[]string{"list", "get"},
		),
	)

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/drafts",
		user:   builderBetaTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response struct {
		Drafts []builderDraftResponse `json:"drafts"`
		Shared []builderDraftResponse `json:"shared"`
	}
	harness.decode(recorder, &response)

	if len(response.Shared) != 1 || response.Shared[0].ID != visible.ID {
		t.Fatalf("shared = %+v, want only %q", response.Shared, visible.ID)
	}
	if !response.Shared[0].ReadOnly {
		t.Error("shared draft is writable without cross-user update permission")
	}
	if strings.Contains(recorder.Body.String(), hidden.ID) {
		t.Error("listing leaks a draft the caller may not see")
	}
}

func TestBuilderBetaSnapshotRoundTrip(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "first")

	path := "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID
	second := string(builderBetaDocument(t, "second"))

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"summary":"second","document":` + second + `}`,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var saved builderDraftResponse

	harness.decode(recorder, &saved)

	if saved.ETag == draft.ETag {
		t.Error("etag did not change after appending a snapshot")
	}

	if saved.Snapshots != 2 {
		t.Errorf("snapshots = %d, want 2", saved.Snapshots)
	}

	if saved.Cursor != 1 || !saved.CanUndo || saved.CanRedo {
		t.Errorf("cursor = %d, canUndo = %t, canRedo = %t; want 1, true, false",
			saved.Cursor, saved.CanUndo, saved.CanRedo)
	}

	// The current snapshot carries the document that was just saved.
	recorder = harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   path + "/snapshots/current",
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var snapshot builderSnapshotDocumentResponse

	harness.decode(recorder, &snapshot)

	if !snapshot.Snapshot.Current {
		t.Error("current snapshot is not marked current")
	}

	if !strings.Contains(string(snapshot.Document), "second") {
		t.Error("current snapshot does not carry the saved document")
	}

	// Undo moves the cursor back without discarding the snapshot.
	recorder = harness.do(builderBetaRequest{
		method:  http.MethodPatch,
		path:    path + "/cursor",
		body:    `{"index":0}`,
		user:    builderBetaTestOwner,
		ifMatch: saved.ETag,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("cursor status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var moved builderDraftResponse

	harness.decode(recorder, &moved)

	if moved.Cursor != 0 || moved.CanUndo || !moved.CanRedo {
		t.Errorf("cursor = %d, canUndo = %t, canRedo = %t; want 0, false, true",
			moved.Cursor, moved.CanUndo, moved.CanRedo)
	}

	if moved.Snapshots != 2 {
		t.Errorf("snapshots = %d, want 2", moved.Snapshots)
	}
}

func TestBuilderBetaCursorRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")
	path := "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID + "/cursor"

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "neither", body: `{}`, status: http.StatusBadRequest},
		{name: "both", body: `{"index":0,"snapshotId":"id-2"}`, status: http.StatusBadRequest},
		{name: "out of range", body: `{"index":9}`, status: http.StatusUnprocessableEntity},
		{name: "unknown snapshot", body: `{"snapshotId":"nope"}`, status: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderBetaRequest{
				method:  http.MethodPatch,
				path:    path,
				body:    tt.body,
				user:    builderBetaTestOwner,
				ifMatch: draft.ETag,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}
}

// TestBuilderBetaIfMatchRequired asserts every mutation after creation refuses
// to run without a usable entity tag.
func TestBuilderBetaIfMatchRequired(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")

	var (
		path     = "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID
		document = string(builderBetaDocument(t, "second"))
	)

	mutations := []builderBetaRequest{
		{method: http.MethodPost, path: path + "/snapshots", body: `{"document":` + document + `}`},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`},
		{method: http.MethodDelete, path: path},
	}

	tags := []struct {
		name    string
		ifMatch string
		status  int
	}{
		{name: "missing", ifMatch: "", status: http.StatusBadRequest},
		{name: "unquoted", ifMatch: "12", status: http.StatusBadRequest},
		{name: "wildcard", ifMatch: "*", status: http.StatusBadRequest},
		{name: "weak", ifMatch: `W/"12"`, status: http.StatusBadRequest},
		{name: "list", ifMatch: `"12", "13"`, status: http.StatusBadRequest},
		{name: "stale", ifMatch: `"1"`, status: http.StatusPreconditionFailed},
	}

	for _, mutation := range mutations {
		for _, tag := range tags {
			request := mutation
			request.user = builderBetaTestOwner
			request.ifMatch = tag.ifMatch

			recorder := harness.do(request)

			if recorder.Code != tag.status {
				t.Errorf("%s %s with a %s tag: status = %d, want %d",
					request.method, request.path, tag.name, recorder.Code, tag.status)
			}
		}
	}

	// Nothing above changed the draft.
	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   path,
		user:   builderBetaTestOwner,
	})

	if etag := recorder.Header().Get("ETag"); etag != draft.ETag {
		t.Errorf("etag = %q, want the unchanged %q", etag, draft.ETag)
	}
}

// TestBuilderBetaStaleSnapshotIsRejected asserts a second writer holding an
// outdated entity tag cannot overwrite the first writer's save.
func TestBuilderBetaStaleSnapshotIsRejected(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "first")

	path := "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID + "/snapshots"

	first := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path,
		body:    `{"document":` + string(builderBetaDocument(t, "second")) + `}`,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if first.Code != http.StatusCreated {
		t.Fatalf("first save status = %d, want %d", first.Code, http.StatusCreated)
	}

	second := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path,
		body:    `{"document":` + string(builderBetaDocument(t, "third")) + `}`,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if second.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale save status = %d, want %d", second.Code, http.StatusPreconditionFailed)
	}
}

func TestBuilderBetaDeleteDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")
	path := "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodDelete,
		path:    path,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if count := harness.store.count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}

	if count := harness.store.count(bapi.NamespaceChunks); count != 0 {
		t.Errorf("stored chunks = %d, want 0", count)
	}

	recorder = harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   path,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status after delete = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestBuilderBetaDraftVerbsAreDistinct asserts each draft operation authorizes
// its own verb rather than a single blanket permission.
func TestBuilderBetaDraftVerbsAreDistinct(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "topo")

	var (
		path     = "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID
		document = string(builderBetaDocument(t, "second"))
		readOnly = builderBetaRole(
			builderBetaPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		)
	)

	mutations := []builderBetaRequest{
		{
			method:  http.MethodPost,
			path:    path + "/snapshots",
			body:    `{"document":` + document + `}`,
			ifMatch: draft.ETag,
		},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, ifMatch: draft.ETag},
		{method: http.MethodDelete, path: path, ifMatch: draft.ETag},
	}

	for _, mutation := range mutations {
		request := mutation
		request.user = builderBetaTestOwner
		request.role = &readOnly

		recorder := harness.do(request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d for a read-only role",
				request.method, request.path, recorder.Code, http.StatusForbidden)
		}
	}

	// Reading still works with the same role.
	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   path,
		user:   builderBetaTestOwner,
		role:   &readOnly,
	})

	if recorder.Code != http.StatusOK {
		t.Errorf("GET status = %d, want %d for a read-only role", recorder.Code, http.StatusOK)
	}
}

// TestBuilderBetaPublishedDocumentsRequireCurrentConfigReference ensures stale
// immutable records never remain visible after their config reference changes.
func TestBuilderBetaPublishedDocumentsRequireCurrentConfigReference(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	listDocuments := func() []builderDocumentResponse {
		t.Helper()

		recorder := harness.do(builderBetaRequest{
			method: http.MethodGet,
			path:   "/builder/documents",
			user:   builderBetaTestOwner,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("listing documents: status = %d, want %d", recorder.Code, http.StatusOK)
		}

		var response struct {
			Documents []builderDocumentResponse `json:"documents"`
		}

		harness.decode(recorder, &response)

		return response.Documents
	}

	getStatus := func(id string) int {
		t.Helper()

		return harness.do(builderBetaRequest{
			method: http.MethodGet,
			path:   "/builder/documents/" + id,
			user:   builderBetaTestOwner,
		}).Code
	}

	published, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:   "current-topology",
		Kind:     builderBetaKindTopology,
		Actor:    builderBetaTestOwner,
		Document: builderBetaDocument(t, "current"),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	reference, err := published.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	topology, err := store.NewConfig("Topology/current-topology")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, *topology)

	listed := listDocuments()
	if len(listed) != 1 || listed[0].ID != published.ID {
		t.Fatalf("listed documents = %#v, want current document %s", listed, published.ID)
	}

	if status := getStatus(published.ID); status != http.StatusOK {
		t.Fatalf("getting current document: status = %d, want %d", status, http.StatusOK)
	}

	// Missing annotations hide an otherwise valid immutable record.
	delete(harness.configs[0].Metadata.Annotations, bapi.DocumentAnnotation)
	if listed = listDocuments(); len(listed) != 0 {
		t.Fatalf("listed unreferenced documents = %#v, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Fatalf("getting unreferenced document: status = %d, want %d", status, http.StatusNotFound)
	}

	// Malformed references fail closed without making the listing unavailable.
	harness.configs[0].Metadata.Annotations[bapi.DocumentAnnotation] = "{"
	if listed = listDocuments(); len(listed) != 0 {
		t.Fatalf("listed document with malformed reference = %#v, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Fatalf("getting document with malformed reference: status = %d, want %d",
			status, http.StatusNotFound)
	}

	// Deleting the target config makes the document an inaccessible orphan.
	harness.configs = nil
	if listed = listDocuments(); len(listed) != 0 {
		t.Fatalf("listed document for deleted config = %#v, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Fatalf("getting document for deleted config: status = %d, want %d",
			status, http.StatusNotFound)
	}

	// Replacing the annotation exposes only the new immutable document.
	replacement, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:   "current-topology",
		Kind:     builderBetaKindTopology,
		Actor:    builderBetaTestOwner,
		Document: builderBetaDocument(t, "replacement"),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument replacement returned error: %v", err)
	}

	replacementReference, err := replacement.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference replacement returned error: %v", err)
	}

	topology.Metadata.Annotations[bapi.DocumentAnnotation] = replacementReference
	harness.configs = append(harness.configs, *topology)

	listed = listDocuments()
	if len(listed) != 1 || listed[0].ID != replacement.ID {
		t.Fatalf("listed superseded documents = %#v, want only %s", listed, replacement.ID)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Fatalf("getting superseded document: status = %d, want %d", status, http.StatusNotFound)
	}
	if status := getStatus(replacement.ID); status != http.StatusOK {
		t.Fatalf("getting replacement document: status = %d, want %d", status, http.StatusOK)
	}
}

// TestBuilderBetaErrorStatuses asserts every sentinel [phenix/api/builder]
// documents is mapped to a status, so a new service error can never be answered
// with a misleading one.
func TestBuilderBetaErrorStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "not found", err: bapi.ErrNotFound, status: http.StatusNotFound},
		{name: "conflict", err: bapi.ErrConflict, status: http.StatusConflict},
		{name: "too large", err: bapi.ErrTooLarge, status: http.StatusRequestEntityTooLarge},
		{name: "invalid", err: bapi.ErrInvalid, status: http.StatusUnprocessableEntity},
		{name: "corrupt", err: bapi.ErrCorrupt, status: http.StatusInternalServerError},
		{name: "cleanup", err: bapi.ErrCleanup, status: http.StatusInternalServerError},
		{
			name:   "wrapped",
			err:    fmt.Errorf("appending snapshot: %w", bapi.ErrTooLarge),
			status: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			webErr := builderBetaWebError(tt.err, "unable to do the thing")

			if webErr.Status != tt.status {
				t.Fatalf("status = %d, want %d", webErr.Status, tt.status)
			}
		})
	}
}

// TestBuilderBetaSchemaRoutePrecedence asserts the exact "/schemas/builder/v1"
// route wins over the generic "/schemas/{kind}/{version}" route. gorilla/mux
// matches in registration order, so the generic route would capture the builder
// schema if it were registered first.
func TestBuilderBetaSchemaRoutePrecedence(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	// Registered after the Builder Beta routes, exactly as [Start] does.
	harness.api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	want, err := bdoc.SchemaJSON()
	if err != nil {
		t.Fatalf("SchemaJSON returned error: %v", err)
	}

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/schemas/builder/v1",
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Fatal("/schemas/builder/v1 was answered by the generic schema handler")
	}

	// Other kinds still reach the generic handler.
	other := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/schemas/topology/v1",
		user:   builderBetaTestOwner,
	})

	if bytes.Equal(other.Body.Bytes(), want) {
		t.Error("the builder schema handler captured another kind")
	}
}

// TestBuilderBetaRegisteredBeforeGenericSchema guards the registration order in
// server.go itself, which the behavioural test above cannot: it builds its own
// router and so cannot notice the two registrations being swapped in [Start].
func TestBuilderBetaRegisteredBeforeGenericSchema(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	var (
		beta    = bytes.Index(source, []byte("registerBuilderBetaRoutes(api)"))
		generic = bytes.Index(source, []byte(`"/schemas/{kind}/{version}"`))
	)

	if beta < 0 {
		t.Fatal("server.go does not register the Builder Beta routes")
	}

	if generic < 0 {
		t.Fatal("server.go does not register the generic schema route")
	}

	if beta > generic {
		t.Fatal("the Builder Beta routes must be registered before /schemas/{kind}/{version}")
	}
}

// builderBetaWarning returns the cleanup warning a response carries, if any.
func builderBetaWarning(recorder *httptest.ResponseRecorder) string {
	return recorder.Header().Get("Warning")
}

// TestBuilderBetaSnapshotCleanupSucceeds asserts a snapshot that was written
// durably but whose superseded content could not be removed is still reported
// as a success, with the new entity tag, so the client never retries with a
// tag that is already stale.
func TestBuilderBetaSnapshotCleanupSucceeds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "first")

	path := "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID

	// Append a second snapshot, then move the cursor back so appending again
	// discards it: that discarded content is what cleanup fails to remove.
	appended := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderBetaDocument(t, "second")) + `}`,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if appended.Code != http.StatusCreated {
		t.Fatalf("appending: status = %d, want %d: %s",
			appended.Code, http.StatusCreated, appended.Body)
	}

	var second builderDraftResponse

	harness.decode(appended, &second)

	moved := harness.do(builderBetaRequest{
		method:  http.MethodPatch,
		path:    path + "/cursor",
		body:    `{"index":0}`,
		user:    builderBetaTestOwner,
		ifMatch: second.ETag,
	})

	if moved.Code != http.StatusOK {
		t.Fatalf("moving cursor: status = %d, want %d: %s", moved.Code, http.StatusOK, moved.Body)
	}

	var rewound builderDraftResponse

	harness.decode(moved, &rewound)

	harness.store.failPrefixDelete = true

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderBetaDocument(t, "third")) + `}`,
		user:    builderBetaTestOwner,
		ifMatch: rewound.ETag,
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	if warning := builderBetaWarning(recorder); !strings.Contains(warning, "append snapshot") {
		t.Errorf("warning = %q, want it to name the operation", warning)
	}

	var saved builderDraftResponse

	harness.decode(recorder, &saved)

	if saved.ETag == rewound.ETag || saved.ETag == "" {
		t.Fatalf("etag = %q, want the new revision", saved.ETag)
	}

	harness.store.failPrefixDelete = false

	// The returned tag is the current one: the next mutation is accepted.
	next := harness.do(builderBetaRequest{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderBetaDocument(t, "fourth")) + `}`,
		user:    builderBetaTestOwner,
		ifMatch: saved.ETag,
	})

	if next.Code != http.StatusCreated {
		t.Fatalf("reusing the returned etag: status = %d, want %d: %s",
			next.Code, http.StatusCreated, next.Body)
	}
}

// TestBuilderBetaDeleteCleanupSucceeds asserts a draft whose record is gone but
// whose chunks could not be removed is reported as deleted.
func TestBuilderBetaDeleteCleanupSucceeds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "doomed")

	harness.store.failPrefixDelete = true

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodDelete,
		path:    "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if warning := builderBetaWarning(recorder); !strings.Contains(warning, "delete draft") {
		t.Errorf("warning = %q, want it to name the operation", warning)
	}

	harness.store.failPrefixDelete = false

	// The draft really is gone.
	after := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID,
		user:   builderBetaTestOwner,
	})

	if after.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", after.Code, http.StatusNotFound, after.Body)
	}
}

// TestBuilderBetaCleanupWarningOmitsCause asserts the warning names only the
// operation: nothing about the store reaches the client.
func TestBuilderBetaCleanupWarningOmitsCause(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "doomed")

	harness.store.failPrefixDelete = true

	recorder := harness.do(builderBetaRequest{
		method:  http.MethodDelete,
		path:    "/builder/drafts/" + builderBetaTestOwner + "/" + draft.ID,
		user:    builderBetaTestOwner,
		ifMatch: draft.ETag,
	})

	warning := builderBetaWarning(recorder)

	if strings.Contains(warning, errBuilderBetaPrefixDelete.Error()) ||
		strings.Contains(warning, draft.ID) {
		t.Errorf("warning = %q, want no cause or identifier", warning)
	}

	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want none", recorder.Body)
	}
}
