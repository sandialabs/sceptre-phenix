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
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const (
	builderV2TestOwner = "alice"
	builderV2TestPeer  = "bob"
)

// builderV2SPABody is what the router level NotFoundHandler serves in
// production: the SPA index. A Builder v2 request must never reach it.
const builderV2SPABody = "<!doctype html><title>phenix</title>"

// builderV2Harness bundles a router serving the Builder v2 routes with the
// fakes backing it.
type builderV2Harness struct {
	t                 *testing.T
	router            *mux.Router
	api               *mux.Router
	service           *bapi.Service
	store             *memrecord.Store
	configs           store.Configs
	failConfigKind    string
	failConfigErr     error
	failBroadcastKind string
	failExperiment    bool
	configWrites      int
	experimentWrites  int
	// failReconfigure fails the configure stage of an experiment update,
	// reconfigured lists the experiments it ran for, and lockedExperiment,
	// when set, runs once the experiment lock is held.
	failReconfigure  bool
	reconfigured     []string
	lockedExperiment func(string)
	// configuring, when set, runs as the configure stage does, before
	// failReconfigure fails it.
	configuring func(string)
	// configLists names the kind of each config listing the API made, and
	// configGets counts the configs it got one at a time.
	configLists []string
	configGets  int
	// files is the directory the API reads Builder files from, a directory
	// of the test's own, whose "mounts" directory is excluded. fileReads
	// counts the Builder files the API went to read.
	files     string
	fileReads int
}

// newBuilderV2Router returns a router wired the way [Start] wires the real
// one: a root router whose NotFoundHandler serves the SPA index, and an
// "/api/v1" subrouter whose NotFoundHandler answers with JSON.
func newBuilderV2Router() (*mux.Router, *mux.Router) {
	router := mux.NewRouter().StrictSlash(true)

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(builderV2SPABody))
	})

	api := router.PathPrefix("/api/v1").Subrouter()
	api.NotFoundHandler = apiNotFoundHandler()

	return router, api
}

// builderV2Request describes a request to the harness.
type builderV2Request struct {
	method  string
	path    string
	body    string
	user    string
	role    *rbac.Role
	ifMatch string
	// contentType is the Content-Type of body, which the config routes read.
	contentType string
}

// newBuilderV2Harness returns a harness whose routes are registered with the
// "builder-v2" feature enabled. Tests using it cannot run in parallel: route
// registration reads the package level server options.
func newBuilderV2Harness(t *testing.T, configs ...store.Config) *builderV2Harness {
	t.Helper()

	builderV2SetFeatures(t, builderV2Feature)

	var (
		fake  = memrecord.New()
		clock = new(atomic.Int64)
		ids   = new(atomic.Int64)
	)

	service, err := bapi.New(
		bapi.WithStore(fake),
		bapi.WithChunkSize(1024),
		bapi.WithClock(func() time.Time { return memrecord.Time(clock.Add(1)) }),
		bapi.WithIDSource(func() (string, error) {
			return "id-" + strconv.FormatInt(ids.Add(1), 10), nil
		}),
	)
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	router, api := newBuilderV2Router()

	harness := &builderV2Harness{
		t:                 t,
		router:            router,
		api:               api,
		service:           service,
		store:             fake,
		configs:           configs,
		failConfigKind:    "",
		failConfigErr:     nil,
		failBroadcastKind: "",
		failExperiment:    false,
		configWrites:      0,
		experimentWrites:  0,
		failReconfigure:   false,
		reconfigured:      nil,
		lockedExperiment:  nil,
		configuring:       nil,
		configLists:       nil,
		configGets:        0,
		files:             filepath.Join(t.TempDir(), "phenix"),
		fileReads:         0,
	}

	options := []builderV2Option{
		withBuilderV2Service(service),
		withBuilderV2Configs(harness.listConfigs, harness.getConfig),
		withBuilderV2PublishOps(harness.publishOps()),
		func(api *builderV2API) {
			api.documentFiles = func() (string, []string) {
				harness.fileReads++

				return harness.files, []string{filepath.Join(harness.files, "mounts")}
			}
		},
	}

	if err := registerBuilderV2Routes(harness.api, options...); err != nil {
		t.Fatalf("registerBuilderV2Routes returned error: %v", err)
	}

	return harness
}

// configWriteFailure is what a config write of failConfigKind fails with:
// failConfigErr, when set.
func (h *builderV2Harness) configWriteFailure() error {
	if h.failConfigErr != nil {
		return h.failConfigErr
	}

	return errors.New("injected config write failure")
}

func (h *builderV2Harness) publishOps() builderV2PublishOps {
	return builderV2PublishOps{
		createConfig: func(config *store.Config) (*store.Config, error) {
			if config.Kind == h.failConfigKind {
				return nil, h.configWriteFailure()
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
				return h.configWriteFailure()
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
		lockExperiment: func(name, _ string) error {
			if h.lockedExperiment != nil {
				h.lockedExperiment(name)
			}

			return nil
		},
		unlockExperiment: func(string) {},
		broadcastConfig: func(config *store.Config, _ string) error {
			if config.Kind == h.failBroadcastKind {
				return errors.New("injected broadcast failure")
			}

			return nil
		},
		broadcastExperiment: func(string, string) error { return nil },
		decodeTopology:      h.decodeTopology,
		reconfigureExperiment: func(name string) error {
			if h.configuring != nil {
				h.configuring(name)
			}

			if h.failReconfigure {
				return errors.New("injected configure failure")
			}

			h.reconfigured = append(h.reconfigured, name)

			return nil
		},
		annotateConfig: func(config *store.Config) error {
			for i := range h.configs {
				if h.configs[i].FullName() == config.FullName() {
					h.configs[i] = *cloneBuilderConfig(config)

					return nil
				}
			}

			return store.ErrNotExist
		},
		deleteConfig: func(name string) error {
			for i := range h.configs {
				if h.configs[i].FullName() == name {
					h.configs = append(h.configs[:i:i], h.configs[i+1:]...)

					return nil
				}
			}

			return store.ErrNotExist
		},
	}
}

// decodeTopology merges included topologies from the harness configs, the
// way types.DecodeTopologyFromConfig merges them from the phenix store.
func (h *builderV2Harness) decodeTopology( //nolint:ireturn // stands in for types.DecodeTopologyFromConfig
	config store.Config,
) (ifaces.TopologySpec, error) {
	var spec v1.TopologySpec

	if err := mapstructure.WeakDecode(config.Spec, &spec); err != nil {
		return nil, err
	}

	for _, name := range spec.IncludeTopologiesF {
		included, err := h.getConfig(builderV2KindTopology + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("loading included topology %s: %w", name, err)
		}

		merged, err := h.decodeTopology(*included)
		if err != nil {
			return nil, err
		}

		child, ok := merged.(*v1.TopologySpec)
		if !ok {
			return nil, fmt.Errorf("included topology %s is not v1", name)
		}

		spec.NodesF = append(spec.NodesF, child.NodesF...)
	}

	return &spec, nil
}

// builderV2SetFeatures replaces the package level server options for the
// duration of a test.
func builderV2SetFeatures(t *testing.T, features ...string) {
	t.Helper()

	previous := o

	t.Cleanup(func() { o = previous })

	o = newServerOptions(ServeWithFeatures(features))
}

// listConfigs is the read-only config lister the API is given.
func (h *builderV2Harness) listConfigs(kind string) (store.Configs, error) {
	canonical := store.ConfigFullName(kind, "name")
	if canonical == "" {
		return nil, fmt.Errorf("unknown config kind %q", kind)
	}

	wanted, _, _ := strings.Cut(canonical, "/")
	configs := store.Configs{}
	h.configLists = append(h.configLists, wanted)

	for i := range h.configs {
		if h.configs[i].Kind == wanted {
			configs = append(configs, h.configs[i])
		}
	}

	return configs, nil
}

// getConfig is the read-only config getter the API is given.
func (h *builderV2Harness) getConfig(name string) (*store.Config, error) {
	h.configGets++

	for i := range h.configs {
		if h.configs[i].FullName() == name {
			config := h.configs[i]

			return &config, nil
		}
	}

	return nil, store.ErrNotExist
}

// do performs a request against the harness router.
func (h *builderV2Harness) do(request builderV2Request) *httptest.ResponseRecorder {
	h.t.Helper()

	var body io.Reader

	if request.body != "" {
		body = strings.NewReader(request.body)
	}

	req := httptest.NewRequest(request.method, "/api/v1"+request.path, body)

	if request.user != "" {
		role := builderV2FullRole()
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

	if request.contentType != "" {
		req.Header.Set("Content-Type", request.contentType)
	}

	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, req)

	return recorder
}

// decode unmarshals a recorded response body.
func (h *builderV2Harness) decode(recorder *httptest.ResponseRecorder, target any) {
	h.t.Helper()

	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		h.t.Fatalf("decoding response: %v (body %d bytes)", err, recorder.Body.Len())
	}
}

// createDraft creates a draft owned by the given user and returns it.
func (h *builderV2Harness) createDraft(user, name string) builderDraftResponse {
	h.t.Helper()

	recorder := h.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/drafts",
		body:   `{"title":"` + name + `","document":` + string(builderV2Document(h.t, name)) + `}`,
		user:   user,
	})

	if recorder.Code != http.StatusCreated {
		h.t.Fatalf("creating draft: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	var draft builderDraftResponse

	h.decode(recorder, &draft)

	return draft
}

// builderV2Role returns a role holding exactly the given policies.
func builderV2Role(policies ...*v1.PolicySpec) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Name: "test", Policies: policies}}
}

// builderV2Policy returns one policy.
func builderV2Policy(resources, names, verbs []string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
}

// builderV2FullRole may do everything, including operating on the drafts of
// other users. It mirrors the wildcards the global-admin role config uses.
func builderV2FullRole() rbac.Role {
	return builderV2Role(builderV2Policy(
		[]string{"*", "*/*"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	))
}

// builderV2OwnerRole may do everything with its own drafts and configs, but
// holds no cross-user draft permission at all. The resources are enumerated so
// no wildcard can grant "builder-drafts" by accident.
func builderV2OwnerRole() rbac.Role {
	return builderV2Role(builderV2Policy(
		[]string{"configs", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	))
}

// builderV2Document returns a small, valid builder document.
func builderV2Document(t *testing.T, name string) []byte {
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

func TestBuilderV2FeatureGate(t *testing.T) { //nolint:paralleltest // mutates package options
	paths := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/schemas/builder-v2/v1"},
		{method: http.MethodGet, path: "/builder-v2/drafts"},
		{method: http.MethodPost, path: "/builder-v2/drafts"},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1"},
		{method: http.MethodDelete, path: "/builder-v2/drafts/alice/id-1"},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1/snapshots"},
		{method: http.MethodPost, path: "/builder-v2/drafts/alice/id-1/snapshots"},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1/snapshots/current"},
		{method: http.MethodPatch, path: "/builder-v2/drafts/alice/id-1/cursor"},
		{method: http.MethodPut, path: "/builder-v2/drafts/alice/id-1/cursor"},
		{method: http.MethodGet, path: "/builder-v2/sources"},
		{method: http.MethodPost, path: "/builder-v2/generate"},
		{method: http.MethodPost, path: "/builder-v2/export/topology"},
		{method: http.MethodGet, path: "/builder-v2/documents"},
		{method: http.MethodGet, path: "/builder-v2/documents/doc-1"},
		{method: http.MethodDelete, path: "/builder-v2/documents/doc-1"},
		// Publishing is gated like every other Builder v2 route.
		{method: http.MethodPost, path: "/builder-v2/drafts/alice/id-1/publish"},
	}

	builderV2SetFeatures(t)

	router, api := newBuilderV2Router()

	if err := registerBuilderV2Routes(api); err != nil {
		t.Fatalf("registerBuilderV2Routes returned error: %v", err)
	}

	// Registered after the Builder v2 routes, exactly as [Start] does, so
	// /schemas/builder-v2/v1 falls through to the generic schema handler.
	api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	for _, tt := range paths {
		req := httptest.NewRequest(tt.method, "/api/v1"+tt.path, nil)
		ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, builderV2TestOwner)
		ctx = context.WithValue(ctx, middleware.ContextKeyRole, builderV2FullRole())

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req.WithContext(ctx))

		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want %d with the feature off",
				tt.method, tt.path, recorder.Code, http.StatusNotFound)
		}

		// The SPA index must never answer an API request: a client would read
		// 200 HTML as a working, enabled route.
		if body := recorder.Body.String(); strings.Contains(body, builderV2SPABody) {
			t.Errorf("%s %s: the SPA index answered an API request", tt.method, tt.path)
		}

		if contentType := recorder.Header().Get("Content-Type"); contentType != mimeJSON {
			t.Errorf("%s %s: Content-Type = %q, want %q",
				tt.method, tt.path, contentType, mimeJSON)
		}
	}

	// The SPA fallback still answers everything outside the API.
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/builder-v2", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), builderV2SPABody) {
		t.Errorf("UI route: status = %d, want the SPA index", recorder.Code)
	}
}

func TestBuilderV2PublishTopology(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)

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

	// The annotation holds the document's digest and ID and nothing else:
	// which draft and snapshot it was published from is on its record.
	published, err := harness.service.GetPublishedDocument(context.Background(), reference.StoredID("topo"))
	if err != nil {
		t.Fatalf("the published document: %v", err)
	}

	if reference != published.Reference() || reference.Digest != draft.Digest || reference.Path != "" {
		t.Fatalf("builder-doc annotation = %+v, want the digest and the ID of %+v", reference, published)
	}

	if published.SnapshotID != draft.SnapshotID || published.DraftID != draft.ID || published.Schema != bdoc.SchemaURI {
		t.Fatalf("published document = %+v, want snapshot %q of draft %q", published, draft.SnapshotID, draft.ID)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Dirty() || meta.Publication == nil || meta.Publication.DocumentID != reference.ID {
		t.Fatalf("draft publication = %#v", meta.Publication)
	}
}

func TestBuilderV2Unauthenticated(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	requests := []builderV2Request{
		{method: http.MethodGet, path: "/schemas/builder-v2/v1"},
		{method: http.MethodGet, path: "/builder-v2/drafts"},
		{method: http.MethodPost, path: "/builder-v2/drafts", body: `{}`},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1"},
		// Refused before the missing If-Match is.
		{method: http.MethodDelete, path: "/builder-v2/drafts/alice/id-1"},
		{method: http.MethodPost, path: "/builder-v2/drafts/alice/id-1/snapshots", body: `{}`},
		{method: http.MethodDelete, path: "/builder-v2/drafts/alice/id-1/snapshots/s-1"},
		{method: http.MethodPatch, path: "/builder-v2/drafts/alice/id-1/cursor", body: `{}`},
		{method: http.MethodPost, path: "/builder-v2/drafts/alice/id-1/publish", body: `{}`},
		{method: http.MethodPut, path: "/builder-v2/drafts/alice/id-1/shares", body: `{}`},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1/shares/candidates"},
		{method: http.MethodGet, path: "/builder-v2/sources"},
		{method: http.MethodPost, path: "/builder-v2/generate", body: `{}`},
		{method: http.MethodPost, path: "/builder-v2/export/topology", body: `{}`},
		{method: http.MethodGet, path: "/builder-v2/documents"},
	}

	for _, request := range requests {
		recorder := harness.do(request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d without an identity",
				request.method, request.path, recorder.Code, http.StatusForbidden)
		}
	}
}

func TestBuilderV2Unauthorized(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	// A role holding no config permission at all.
	empty := builderV2Role()

	requests := []builderV2Request{
		{method: http.MethodGet, path: "/builder-v2/drafts"},
		{method: http.MethodPost, path: "/builder-v2/drafts", body: `{}`},
		{method: http.MethodGet, path: "/builder-v2/drafts/alice/id-1"},
		{method: http.MethodGet, path: "/builder-v2/sources"},
		{method: http.MethodPost, path: "/builder-v2/generate", body: `{}`},
		{method: http.MethodPost, path: "/builder-v2/export/topology", body: `{}`},
		{method: http.MethodGet, path: "/builder-v2/documents"},
	}

	for _, request := range requests {
		request.user = builderV2TestOwner
		request.role = &empty

		recorder := harness.do(request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want %d without config permission",
				request.method, request.path, recorder.Code, http.StatusForbidden)
		}
	}
}

func TestBuilderV2CreateDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	document := builderV2Document(t, "topo")

	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/drafts",
		body:   `{"title":"fallback","sourceToken":"Topology/foo","document":` + string(document) + `}`,
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var draft builderDraftResponse

	harness.decode(recorder, &draft)

	if draft.Owner != builderV2TestOwner {
		t.Errorf("owner = %q, want %q", draft.Owner, builderV2TestOwner)
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

	want := "/api/v1/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID
	if location := recorder.Header().Get("Location"); location != want {
		t.Errorf("Location = %q, want %q", location, want)
	}
}

func TestBuilderV2CreateDraftForOtherUser(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	document := builderV2Document(t, "topo")

	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/drafts",
		body:   `{"owner":"` + builderV2TestPeer + `","document":` + string(document) + `}`,
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}
}

// TestBuilderV2CreateDraftFromPublishedDocument asserts a new draft may name
// a published document as its source only when the caller may get the config
// it was published to. Any other document is answered as one that does not
// exist, and no draft is created.
func TestBuilderV2CreateDraftFromPublishedDocument(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	secret := builderV2Publish(t, harness, "secret")
	public := builderV2Publish(t, harness, "public")

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get", "create"},
	))

	document := string(builderV2Document(t, "topo"))

	create := func(token string) *httptest.ResponseRecorder {
		return harness.do(builderV2Request{
			method: http.MethodPost,
			path:   "/builder-v2/drafts",
			body:   `{"sourceToken":"` + token + `","document":` + document + `}`,
			user:   builderV2TestOwner,
			role:   &role,
		})
	}

	for _, id := range []string{secret.ID, "missing"} {
		recorder := create(builderDocTokenPrefix + id)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("document %s: status = %d, want %d: %s", id, recorder.Code, http.StatusNotFound, recorder.Body)
		}

		if want := "document " + id + " not found"; !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("document %s: body = %s, want %q", id, recorder.Body, want)
		}
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Fatalf("stored drafts = %d, want 0", count)
	}

	recorder := create(builderDocTokenPrefix + public.ID)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var draft builderDraftResponse

	harness.decode(recorder, &draft)

	if draft.SourceToken != builderDocTokenPrefix+public.ID {
		t.Errorf("source token = %q, want %q", draft.SourceToken, builderDocTokenPrefix+public.ID)
	}
}

func TestBuilderV2CreateDraftRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	document := string(builderV2Document(t, "topo"))

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
			body:   `{"title":"` + strings.Repeat("x", builderV2MaxRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderV2Request{
				method: http.MethodPost,
				path:   "/builder-v2/drafts",
				body:   tt.body,
				user:   builderV2TestOwner,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}
}

func TestBuilderV2GetDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID,
		user:   builderV2TestOwner,
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

func TestBuilderV2GetDraftNotFound(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")

	paths := []string{
		"/builder-v2/drafts/" + builderV2TestOwner + "/id-missing",
		// The draft exists, but not under this owner.
		"/builder-v2/drafts/" + builderV2TestPeer + "/" + draft.ID,
		// Identifiers that cannot name a draft never reach the store.
		"/builder-v2/drafts/" + builderV2TestOwner + "/bad%20id",
		"/builder-v2/drafts/" + builderV2TestOwner + "/" + strings.Repeat("x", 200),
	}

	for _, path := range paths {
		recorder := harness.do(builderV2Request{
			method: http.MethodGet,
			path:   path,
			user:   builderV2TestOwner,
		})

		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestBuilderV2ListDrafts(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	mine := harness.createDraft(builderV2TestOwner, "mine")
	theirs := harness.createDraft(builderV2TestPeer, "theirs")

	type listing struct {
		Drafts []builderDraftResponse `json:"drafts"`
		Shared []builderDraftResponse `json:"shared"`
	}

	owner := builderV2OwnerRole()

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/drafts",
		user:   builderV2TestOwner,
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
	recorder = harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/drafts",
		user:   builderV2TestOwner,
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

// TestBuilderV2SharedDraftsAreNamed asserts cross-user listing authorizes
// each draft by its "{owner}/{draftID}" name rather than in bulk.
func TestBuilderV2SharedDraftsAreNamed(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	visible := harness.createDraft(builderV2TestPeer, "visible")
	hidden := harness.createDraft("carol", "hidden")

	role := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderV2Policy(
			[]string{builderV2DraftsResource},
			[]string{builderV2DraftName(builderV2TestPeer, visible.ID)},
			[]string{"list", "get"},
		),
	)

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/drafts",
		user:   builderV2TestOwner,
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

func TestBuilderV2SnapshotRoundTrip(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "first")

	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID
	second := string(builderV2Document(t, "second"))

	recorder := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"summary":"second","document":` + second + `,"opId":"op-2"}`,
		user:    builderV2TestOwner,
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
	recorder = harness.do(builderV2Request{
		method: http.MethodGet,
		path:   path + "/snapshots/current",
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var snapshot builderSnapshotDocumentResponse

	harness.decode(recorder, &snapshot)

	if !snapshot.Snapshot.Current {
		t.Error("current snapshot is not marked current")
	}

	// The client's id for the save comes back with the snapshot, so a client
	// that never saw the response can tell it was stored.
	if snapshot.Snapshot.OpID != "op-2" {
		t.Errorf("snapshot opId = %q, want %q", snapshot.Snapshot.OpID, "op-2")
	}

	if !strings.Contains(string(snapshot.Document), "second") {
		t.Error("current snapshot does not carry the saved document")
	}

	// Undo moves the cursor back without discarding the snapshot.
	recorder = harness.do(builderV2Request{
		method:  http.MethodPatch,
		path:    path + "/cursor",
		body:    `{"index":0}`,
		user:    builderV2TestOwner,
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

// TestBuilderV2DeleteSnapshot deletes versions from a draft's history,
// among them one holding the same document as the current version, which
// stays readable. The current version itself cannot be deleted.
func TestBuilderV2DeleteSnapshot(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "first")
	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID
	etag := draft.ETag

	// The first and third versions hold the same document.
	for _, name := range []string{"second", "first"} {
		recorder := harness.do(builderV2Request{
			method:  http.MethodPost,
			path:    path + "/snapshots",
			body:    `{"document":` + string(builderV2Document(t, name)) + `}`,
			user:    builderV2TestOwner,
			ifMatch: etag,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("saving %s: status = %d: %s", name, recorder.Code, recorder.Body)
		}

		var saved builderDraftResponse

		harness.decode(recorder, &saved)
		etag = saved.ETag
	}

	var history struct {
		Snapshots []builderSnapshotResponse `json:"snapshots"`
		Cursor    int                       `json:"cursor"`
	}

	harness.decode(harness.do(builderV2Request{
		method: http.MethodGet, path: path + "/snapshots", user: builderV2TestOwner,
	}), &history)

	if len(history.Snapshots) != 3 || history.Cursor != 2 {
		t.Fatalf("history = %+v, want 3 snapshots at cursor 2", history)
	}

	// The Draft History table shows who saved each version, and when.
	for _, snapshot := range history.Snapshots {
		if snapshot.CreatedBy != builderV2TestOwner || snapshot.CreatedAt.IsZero() {
			t.Fatalf("snapshot %s created by %q at %v, want %q and a time",
				snapshot.ID, snapshot.CreatedBy, snapshot.CreatedAt, builderV2TestOwner)
		}
	}

	remove := func(snapshot, ifMatch string) *httptest.ResponseRecorder {
		return harness.do(builderV2Request{
			method: http.MethodDelete, path: path + "/snapshots/" + snapshot,
			user: builderV2TestOwner, ifMatch: ifMatch,
		})
	}

	for _, snapshot := range []string{builderV2CurrentSnapshot, history.Snapshots[2].ID} {
		recorder := remove(snapshot, etag)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("deleting %s: status = %d, want %d: %s", snapshot, recorder.Code, http.StatusConflict, recorder.Body)
		}

		var refused weberror.WebError

		harness.decode(recorder, &refused)

		if refused.Message != "The current version cannot be deleted." {
			t.Fatalf("deleting %s: message = %q", snapshot, refused.Message)
		}
	}

	if recorder := remove("id-missing", etag); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleting an unknown snapshot: status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	first := history.Snapshots[0]

	recorder := remove(first.ID, etag)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var deleted builderDraftResponse

	harness.decode(recorder, &deleted)

	if deleted.Snapshots != 2 || deleted.Cursor != 1 || deleted.SnapshotID != history.Snapshots[2].ID ||
		deleted.ETag == etag || recorder.Header().Get("ETag") != deleted.ETag {
		t.Fatalf("deleted = %+v with ETag %q, want 2 snapshots at cursor 1 and a new ETag",
			deleted, recorder.Header().Get("ETag"))
	}

	if recorder := remove(history.Snapshots[1].ID, etag); recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("deleting with a stale ETag: status = %d, want %d", recorder.Code, http.StatusPreconditionFailed)
	}

	// Snapshots never share content: the current version holds the same
	// document as the deleted one and is still readable.
	recorder = harness.do(builderV2Request{
		method: http.MethodGet, path: path + "/snapshots/current", user: builderV2TestOwner,
	})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"first"`) {
		t.Fatalf("current snapshot: status = %d: %s", recorder.Code, recorder.Body)
	}

	recorder = harness.do(builderV2Request{
		method: http.MethodGet, path: path + "/snapshots/" + first.ID, user: builderV2TestOwner,
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted snapshot: status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestBuilderV2CursorRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")
	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID + "/cursor"

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
			recorder := harness.do(builderV2Request{
				method:  http.MethodPatch,
				path:    path,
				body:    tt.body,
				user:    builderV2TestOwner,
				ifMatch: draft.ETag,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}
}

// TestBuilderV2IfMatchRequired asserts every mutation after creation refuses
// to run without a usable entity tag.
func TestBuilderV2IfMatchRequired(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")

	var (
		path     = "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID
		document = string(builderV2Document(t, "second"))
	)

	mutations := []builderV2Request{
		{method: http.MethodPost, path: path + "/snapshots", body: `{"document":` + document + `}`},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`},
		{method: http.MethodDelete, path: path + "/snapshots/current"},
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
			request.user = builderV2TestOwner
			request.ifMatch = tag.ifMatch

			recorder := harness.do(request)

			if recorder.Code != tag.status {
				t.Errorf("%s %s with a %s tag: status = %d, want %d",
					request.method, request.path, tag.name, recorder.Code, tag.status)
			}
		}
	}

	// Nothing above changed the draft.
	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   path,
		user:   builderV2TestOwner,
	})

	if etag := recorder.Header().Get("ETag"); etag != draft.ETag {
		t.Errorf("etag = %q, want the unchanged %q", etag, draft.ETag)
	}
}

// TestBuilderV2StaleSnapshotIsRejected asserts a second writer holding an
// outdated entity tag cannot overwrite the first writer's save.
func TestBuilderV2StaleSnapshotIsRejected(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "first")

	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID + "/snapshots"

	first := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path,
		body:    `{"document":` + string(builderV2Document(t, "second")) + `}`,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	if first.Code != http.StatusCreated {
		t.Fatalf("first save status = %d, want %d", first.Code, http.StatusCreated)
	}

	second := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path,
		body:    `{"document":` + string(builderV2Document(t, "third")) + `}`,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	if second.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale save status = %d, want %d", second.Code, http.StatusPreconditionFailed)
	}
}

func TestBuilderV2DeleteDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "topo")
	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID

	recorder := harness.do(builderV2Request{
		method:  http.MethodDelete,
		path:    path,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}

	if count := harness.store.Count(bapi.NamespaceChunks); count != 0 {
		t.Errorf("stored chunks = %d, want 0", count)
	}

	recorder = harness.do(builderV2Request{
		method: http.MethodGet,
		path:   path,
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status after delete = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestBuilderV2PublishedDocumentsRequireCurrentConfigReference ensures stale
// immutable records never remain visible after their config reference changes.
func TestBuilderV2PublishedDocumentsRequireCurrentConfigReference(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	listDocuments := func() []builderDocumentResponse {
		t.Helper()

		recorder := harness.do(builderV2Request{
			method: http.MethodGet,
			path:   "/builder-v2/documents",
			user:   builderV2TestOwner,
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

		return harness.do(builderV2Request{
			method: http.MethodGet,
			path:   "/builder-v2/documents/" + id,
			user:   builderV2TestOwner,
		}).Code
	}

	published, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:   "current-topology",
		Kind:     builderV2KindTopology,
		Actor:    builderV2TestOwner,
		Document: builderV2Document(t, "current"),
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
		Kind:     builderV2KindTopology,
		Actor:    builderV2TestOwner,
		Document: builderV2Document(t, "replacement"),
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

// TestBuilderV2ErrorStatuses asserts every sentinel [phenix/api/builder]
// documents is mapped to a status, so a new service error can never be answered
// with a misleading one.
func TestBuilderV2ErrorStatuses(t *testing.T) {
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

			webErr := builderV2WebError(tt.err, "unable to do the thing")

			if webErr.Status != tt.status {
				t.Fatalf("status = %d, want %d", webErr.Status, tt.status)
			}
		})
	}
}

// TestBuilderV2SaveWhenEtcdIsOutOfSpace asserts a save etcd refused for lack
// of space says so plainly, and that the same save works once space is freed.
func TestBuilderV2SaveWhenEtcdIsOutOfSpace(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "first")

	save := func() *httptest.ResponseRecorder {
		return harness.do(builderV2Request{
			method:  http.MethodPost,
			path:    "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID + "/snapshots",
			body:    `{"document":` + string(builderV2Document(t, "second")) + `}`,
			user:    builderV2TestOwner,
			role:    nil,
			ifMatch: draft.ETag,
		})
	}

	// What the Etcd store returns once etcd is out of space.
	harness.store.BeforeUpdate = func(namespace, key string) error {
		return fmt.Errorf(
			"updating record %s/%s in Etcd: %w: %w", namespace, key,
			store.ErrNoSpace, errors.New("etcdserver: mvcc: database space exceeded"),
		)
	}

	recorder := save()
	if recorder.Code != http.StatusInsufficientStorage {
		t.Fatalf("save: status = %d, want %d: %s", recorder.Code, http.StatusInsufficientStorage, recorder.Body)
	}

	var refused weberror.WebError

	harness.decode(recorder, &refused)

	if refused.Message != store.ErrNoSpace.Error() {
		t.Fatalf("save: message = %q, want %q", refused.Message, store.ErrNoSpace.Error())
	}

	harness.store.BeforeUpdate = nil

	if recorder := save(); recorder.Code != http.StatusCreated {
		t.Fatalf("save after freeing space: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}
}

// TestBuilderV2SchemaRoutePrecedence asserts the exact "/schemas/builder-v2/v1"
// route wins over the generic "/schemas/{kind}/{version}" route. gorilla/mux
// matches in registration order, so the generic route would capture the builder
// schema if it were registered first.
func TestBuilderV2SchemaRoutePrecedence(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	// Registered after the Builder v2 routes, exactly as [Start] does.
	harness.api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	want, err := bdoc.SchemaJSON()
	if err != nil {
		t.Fatalf("SchemaJSON returned error: %v", err)
	}

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/schemas/builder-v2/v1",
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Fatal("/schemas/builder-v2/v1 was answered by the generic schema handler")
	}

	// Other kinds still reach the generic handler.
	other := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/schemas/topology/v1",
		user:   builderV2TestOwner,
	})

	if other.Code != http.StatusOK || bytes.Equal(other.Body.Bytes(), want) {
		t.Errorf("/schemas/topology/v1: status = %d, want the generic Topology schema", other.Code)
	}

	// The generic handler answers a kind or version it has no schema for with
	// 404, not as a server failure.
	for _, path := range []string{"/schemas/nope/v1", "/schemas/topology/v9"} {
		recorder := harness.do(builderV2Request{method: http.MethodGet, path: path, user: builderV2TestOwner})

		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d: %s", path, recorder.Code, http.StatusNotFound, recorder.Body)
		}
	}
}

// TestBuilderV2RegisteredBeforeGenericSchema guards the registration order in
// server.go itself, which the behavioural test above cannot: it builds its own
// router and so cannot notice the two registrations being swapped in [Start].
func TestBuilderV2RegisteredBeforeGenericSchema(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	var (
		builderV2 = bytes.Index(source, []byte("registerBuilderV2Routes(api)"))
		generic   = bytes.Index(source, []byte(`"/schemas/{kind}/{version}"`))
	)

	if builderV2 < 0 {
		t.Fatal("server.go does not register the Builder v2 routes")
	}

	if generic < 0 {
		t.Fatal("server.go does not register the generic schema route")
	}

	if builderV2 > generic {
		t.Fatal("the Builder v2 routes must be registered before /schemas/{kind}/{version}")
	}
}

// TestBuilderV2RoutesDocumented asserts every Builder v2 route and method
// is described in the OpenAPI document the server publishes under /docs/.
func TestBuilderV2RoutesDocumented(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	source, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("reading openapi.yml: %v", err)
	}

	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}

	if err := yaml.Unmarshal(source, &spec); err != nil {
		t.Fatalf("parsing openapi.yml: %v", err)
	}

	operations := 0

	err = harness.api.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, templateErr := route.GetPathTemplate()
		methods, methodsErr := route.GetMethods()

		if templateErr != nil || methodsErr != nil {
			return nil //nolint:nilerr // routes without a path or methods are not operations
		}

		path := strings.TrimPrefix(template, "/api/v1")

		for _, method := range methods {
			if method == http.MethodOptions {
				continue
			}

			operations++

			if _, ok := spec.Paths[path][strings.ToLower(method)]; !ok {
				t.Errorf("openapi.yml does not document %s %s", method, path)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the routes: %v", err)
	}

	if operations == 0 {
		t.Fatal("no Builder v2 routes were registered")
	}
}

// TestBuilderV2OutOfSpaceDocumented asserts every Builder v2 route that
// writes draft records documents the 507 answer etcd running out of space
// gets (see weberror.ErrorHandler).
func TestBuilderV2OutOfSpaceDocumented(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("reading openapi.yml: %v", err)
	}

	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}

	if err := yaml.Unmarshal(source, &spec); err != nil {
		t.Fatalf("parsing openapi.yml: %v", err)
	}

	// yaml.v2 decodes nested maps with keys of any type.
	documents507 := func(path, method string) bool {
		operation, _ := spec.Paths[path][method].(map[any]any)
		responses, _ := operation["responses"].(map[any]any)
		_, ok := responses["507"]

		return ok
	}

	draft := "/builder-v2/drafts/{owner}/{draft}"

	for _, operation := range []struct{ path, method string }{
		{"/builder-v2/drafts", "post"},
		{draft, "delete"},
		{draft + "/snapshots", "post"},
		{draft + "/snapshots/{snapshot}", "delete"},
		{draft + "/cursor", "patch"},
		{draft + "/cursor", "put"},
		{draft + "/publish", "post"},
		{draft + "/shares", "put"},
	} {
		if !documents507(operation.path, operation.method) {
			t.Errorf("openapi.yml does not document 507 for %s %s", operation.method, operation.path)
		}
	}
}

// builderV2Warning returns the cleanup warning a response carries, if any.
func builderV2Warning(recorder *httptest.ResponseRecorder) string {
	return recorder.Header().Get("Warning")
}

// errBuilderV2PrefixDelete is what the harness store answers a prefix
// deletion [builderV2RefusePrefixDelete] refuses.
var errBuilderV2PrefixDelete = errors.New("prefix deletion is unavailable")

// builderV2RefusePrefixDelete, as the harness store's FailPrefixDelete, fails
// every prefix deletion, which is how the service is driven into reporting a
// cleanup failure after a durable write.
func builderV2RefusePrefixDelete(string, string) error {
	return errBuilderV2PrefixDelete
}

// TestBuilderV2SnapshotCleanupSucceeds asserts a snapshot that was written
// durably but whose superseded content could not be removed is still reported
// as a success, with the new entity tag, so the client never retries with a
// tag that is already stale.
func TestBuilderV2SnapshotCleanupSucceeds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "first")

	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID

	// Append a second snapshot, then move the cursor back so appending again
	// discards it: that discarded content is what cleanup fails to remove.
	appended := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderV2Document(t, "second")) + `}`,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	if appended.Code != http.StatusCreated {
		t.Fatalf("appending: status = %d, want %d: %s",
			appended.Code, http.StatusCreated, appended.Body)
	}

	var second builderDraftResponse

	harness.decode(appended, &second)

	moved := harness.do(builderV2Request{
		method:  http.MethodPatch,
		path:    path + "/cursor",
		body:    `{"index":0}`,
		user:    builderV2TestOwner,
		ifMatch: second.ETag,
	})

	if moved.Code != http.StatusOK {
		t.Fatalf("moving cursor: status = %d, want %d: %s", moved.Code, http.StatusOK, moved.Body)
	}

	var rewound builderDraftResponse

	harness.decode(moved, &rewound)

	harness.store.FailPrefixDelete = builderV2RefusePrefixDelete

	recorder := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderV2Document(t, "third")) + `}`,
		user:    builderV2TestOwner,
		ifMatch: rewound.ETag,
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	if warning := builderV2Warning(recorder); !strings.Contains(warning, "append snapshot") {
		t.Errorf("warning = %q, want it to name the operation", warning)
	}

	var saved builderDraftResponse

	harness.decode(recorder, &saved)

	if saved.ETag == rewound.ETag || saved.ETag == "" {
		t.Fatalf("etag = %q, want the new revision", saved.ETag)
	}

	harness.store.FailPrefixDelete = nil

	// The returned tag is the current one: the next mutation is accepted.
	next := harness.do(builderV2Request{
		method:  http.MethodPost,
		path:    path + "/snapshots",
		body:    `{"document":` + string(builderV2Document(t, "fourth")) + `}`,
		user:    builderV2TestOwner,
		ifMatch: saved.ETag,
	})

	if next.Code != http.StatusCreated {
		t.Fatalf("reusing the returned etag: status = %d, want %d: %s",
			next.Code, http.StatusCreated, next.Body)
	}
}

// TestBuilderV2DeleteCleanupSucceeds asserts a draft whose record is gone but
// whose chunks could not be removed is reported as deleted.
func TestBuilderV2DeleteCleanupSucceeds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "doomed")

	harness.store.FailPrefixDelete = builderV2RefusePrefixDelete

	recorder := harness.do(builderV2Request{
		method:  http.MethodDelete,
		path:    "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if warning := builderV2Warning(recorder); !strings.Contains(warning, "delete draft") {
		t.Errorf("warning = %q, want it to name the operation", warning)
	}

	harness.store.FailPrefixDelete = nil

	// The draft really is gone.
	after := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID,
		user:   builderV2TestOwner,
	})

	if after.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", after.Code, http.StatusNotFound, after.Body)
	}
}

// TestBuilderV2CleanupWarningOmitsCause asserts the warning names only the
// operation: nothing about the store reaches the client.
func TestBuilderV2CleanupWarningOmitsCause(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	draft := harness.createDraft(builderV2TestOwner, "doomed")

	harness.store.FailPrefixDelete = builderV2RefusePrefixDelete

	recorder := harness.do(builderV2Request{
		method:  http.MethodDelete,
		path:    "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID,
		user:    builderV2TestOwner,
		ifMatch: draft.ETag,
	})

	warning := builderV2Warning(recorder)

	if strings.Contains(warning, errBuilderV2PrefixDelete.Error()) ||
		strings.Contains(warning, draft.ID) {
		t.Errorf("warning = %q, want no cause or identifier", warning)
	}

	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want none", recorder.Body)
	}
}

// TestBuilderV2UnreadableDraft asserts that a draft whose metadata no longer
// validates (here, a field this version does not know) neither breaks the
// listing nor stays undeletable: it is listed apart, as damaged, to those who
// may see it, with whether they may delete it.
func TestBuilderV2UnreadableDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	good := harness.createDraft(builderV2TestOwner, "good")
	bad := harness.createDraft(builderV2TestOwner, "bad")

	// Written by a newer phenix, at a revision its owner never read.
	harness.damageDraft(bad.ID)

	recorder := harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/drafts", user: builderV2TestOwner})

	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	type damagedListing struct {
		Drafts  []builderDraftResponse        `json:"drafts"`
		Shared  []builderDraftResponse        `json:"shared"`
		Damaged []builderDamagedDraftResponse `json:"damaged"`
	}

	var listing damagedListing

	harness.decode(recorder, &listing)

	if len(listing.Drafts) != 1 || listing.Drafts[0].ID != good.ID {
		t.Errorf("drafts = %+v, want only the readable %q", listing.Drafts, good.ID)
	}

	// It is listed apart, with what can still be read of it and the ETag
	// that deletes it.
	if len(listing.Damaged) != 1 {
		t.Fatalf("damaged = %+v, want the unreadable %q", listing.Damaged, bad.ID)
	}

	listed := listing.Damaged[0]
	if listed.ID != bad.ID || listed.Owner != builderV2TestOwner || listed.Title != "bad" ||
		listed.Updated == nil || listed.ETag == "" || listed.ETag == bad.ETag || !listed.CanDelete {
		t.Errorf("damaged = %+v, want %q owned by %q, titled, dated, deletable at its current ETag",
			listed, bad.ID, builderV2TestOwner)
	}

	// Others see it as they see readable drafts: with cross-user list
	// permission only, and deletable only with cross-user delete permission.
	listOnly := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"*"}, []string{"list", "get", "delete"}),
		builderV2Policy([]string{"builder-drafts"}, []string{"*", "*/*"}, []string{"list", "get"}),
	)
	owner := builderV2OwnerRole()

	for _, peer := range []struct {
		name      string
		role      *rbac.Role
		damaged   int
		canDelete bool
	}{
		{name: "no cross-user permission", role: &owner, damaged: 0, canDelete: false},
		{name: "cross-user list", role: &listOnly, damaged: 1, canDelete: false},
		{name: "cross-user delete", role: nil, damaged: 1, canDelete: true},
	} {
		recorder = harness.do(builderV2Request{
			method: http.MethodGet, path: "/builder-v2/drafts", user: builderV2TestPeer, role: peer.role,
		})

		var seen damagedListing

		harness.decode(recorder, &seen)

		if len(seen.Damaged) != peer.damaged ||
			(peer.damaged > 0 && seen.Damaged[0].CanDelete != peer.canDelete) {
			t.Errorf("%s: damaged = %+v, want %d with canDelete %t",
				peer.name, seen.Damaged, peer.damaged, peer.canDelete)
		}
	}

	path := "/builder-v2/drafts/" + builderV2TestOwner + "/" + bad.ID

	// Another user without cross-user permission still cannot see it.
	recorder = harness.do(builderV2Request{
		method: http.MethodDelete, path: path, user: builderV2TestPeer, role: &owner, ifMatch: bad.ETag,
	})

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("peer delete status = %d, want %d: %s", recorder.Code, http.StatusNotFound, recorder.Body)
	}

	// The refusal of a stale ETag names the current one, as the listing does.
	recorder = harness.do(builderV2Request{method: http.MethodDelete, path: path, user: builderV2TestOwner, ifMatch: bad.ETag})

	current := recorder.Header().Get("ETag")
	if recorder.Code != http.StatusPreconditionFailed || current != listed.ETag {
		t.Fatalf("stale delete = %d with ETag %q, want %d with the listed ETag %q: %s",
			recorder.Code, current, http.StatusPreconditionFailed, listed.ETag, recorder.Body)
	}

	recorder = harness.do(builderV2Request{method: http.MethodDelete, path: path, user: builderV2TestOwner, ifMatch: listed.ETag})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 1 {
		t.Errorf("stored drafts = %d, want the readable one left", count)
	}
}
