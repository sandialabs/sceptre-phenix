package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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

// builderSPABody is what the router level NotFoundHandler serves in
// production: the SPA index. A Builder request must never reach it.
const builderSPABody = "<!doctype html><title>phenix</title>"

// builderHarness bundles a router serving the Builder routes with the
// fakes backing it.
type builderHarness struct {
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
	// broadcasts counts the config and experiment updates broadcast to open pages.
	broadcasts int
	// failConfigName fails the writes of the one config of this full name ("Scenario/sc").
	failConfigName string
	// failReconfigure fails the configure stage. lockedExperiment runs once the lock is held.
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
	// files is the Builder file directory (its "mounts" excluded). fileReads counts reads of it.
	files     string
	fileReads int
}

// newBuilderRouter returns a router wired as [Start] wires it: the SPA index
// at the root and a JSON NotFoundHandler under "/api/v1".
func newBuilderRouter() (*mux.Router, *mux.Router) {
	router := mux.NewRouter().StrictSlash(true)

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(builderSPABody))
	})

	api := router.PathPrefix("/api/v1").Subrouter()
	api.NotFoundHandler = apiNotFoundHandler()

	return router, api
}

// builderRequest describes a request to the harness.
type builderRequest struct {
	method  string
	path    string
	body    string
	user    string
	role    *rbac.Role
	ifMatch string
	// ifNoneMatch is the If-None-Match header of a read.
	ifNoneMatch string
	// contentType is the Content-Type of body, which the config routes read.
	contentType string
}

// newBuilderHarness returns a harness with the Builder routes registered. It
// reads no template files.
func newBuilderHarness(t *testing.T, configs ...store.Config) *builderHarness {
	t.Helper()

	return newBuilderHarnessWith(t, nil, configs...)
}

// newBuilderHarnessWith is [newBuilderHarness] with more options, applied
// after the harness's own.
func newBuilderHarnessWith(t *testing.T, extra []builderOption, configs ...store.Config) *builderHarness {
	t.Helper()

	var (
		fake  = memrecord.New()
		clock = new(atomic.Int64)
		ids   = new(atomic.Int64)
	)

	service, err := bapi.New(
		bapi.WithStore(fake),
		bapi.WithClock(func() time.Time { return memrecord.Time(clock.Add(1)) }),
		bapi.WithIDSource(func() (string, error) {
			return "id-" + strconv.FormatInt(ids.Add(1), 10), nil
		}),
	)
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness := &builderHarness{
		t:                 t,
		router:            nil,
		api:               nil,
		service:           service,
		store:             fake,
		configs:           configs,
		failConfigKind:    "",
		failConfigErr:     nil,
		failConfigName:    "",
		failBroadcastKind: "",
		failExperiment:    false,
		configWrites:      0,
		experimentWrites:  0,
		broadcasts:        0,
		failReconfigure:   false,
		reconfigured:      nil,
		lockedExperiment:  nil,
		configuring:       nil,
		configLists:       nil,
		configGets:        0,
		files:             filepath.Join(t.TempDir(), "phenix"),
		fileReads:         0,
	}

	harness.reroute(extra...)

	return harness
}

// failsConfigWrite reports whether a write of config fails: one of
// failConfigKind, or the one named failConfigName.
func (h *builderHarness) failsConfigWrite(config *store.Config) bool {
	return config.Kind == h.failConfigKind || (h.failConfigName != "" && config.FullName() == h.failConfigName)
}

// configWriteFailure is what a config write that failsConfigWrite reports
// fails with: failConfigErr, when set.
func (h *builderHarness) configWriteFailure() error {
	if h.failConfigErr != nil {
		return h.failConfigErr
	}

	return errors.New("injected config write failure")
}

func (h *builderHarness) publishOps() builderPublishOps {
	return builderPublishOps{
		createConfig: func(config *store.Config) (*store.Config, error) {
			if h.failsConfigWrite(config) {
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
			if h.failsConfigWrite(config) {
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

			// The topology the experiment holds, as phenix copies it in.
			for _, stored := range h.configs {
				if stored.FullName() == builderKindTopology+"/"+topology {
					config.Spec["topology"] = maps.Clone(stored.Spec)
				}
			}

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
			h.broadcasts++

			if config.Kind == h.failBroadcastKind {
				return errors.New("injected broadcast failure")
			}

			return nil
		},
		broadcastExperiment: func(string, string) error {
			h.broadcasts++

			return nil
		},
		decodeTopology: h.decodeTopology,
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
//
//nolint:ireturn // matches the decodeTopology field's signature
func (h *builderHarness) decodeTopology(config store.Config) (ifaces.TopologySpec, error) {
	var spec v1.TopologySpec

	if err := mapstructure.WeakDecode(config.Spec, &spec); err != nil {
		return nil, err
	}

	for _, name := range spec.IncludeTopologiesF {
		included, err := h.getConfig(builderKindTopology + "/" + name)
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

// listConfigs is the read-only config lister the API is given.
func (h *builderHarness) listConfigs(kind string) (store.Configs, error) {
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
func (h *builderHarness) getConfig(name string) (*store.Config, error) {
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
func (h *builderHarness) do(request builderRequest) *httptest.ResponseRecorder {
	h.t.Helper()

	var body io.Reader

	if request.body != "" {
		body = strings.NewReader(request.body)
	}

	req := httptest.NewRequest(request.method, "/api/v1"+request.path, body)

	if request.user != "" {
		role := builderFullRole()
		if request.role != nil {
			role = request.role
		}

		ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, request.user)
		ctx = context.WithValue(ctx, middleware.ContextKeyRole, *role)
		req = req.WithContext(ctx)
	}

	if request.ifMatch != "" {
		req.Header.Set("If-Match", request.ifMatch)
	}

	if request.ifNoneMatch != "" {
		req.Header.Set("If-None-Match", request.ifNoneMatch)
	}

	if request.contentType != "" {
		req.Header.Set("Content-Type", request.contentType)
	}

	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, req)

	return recorder
}

// decode unmarshals a recorded response body.
func (h *builderHarness) decode(recorder *httptest.ResponseRecorder, target any) {
	h.t.Helper()

	decodeBuilderAnswer(h.t, recorder, target)
}

// TestBuilderUnknownAPIRoutesAnswerJSON asserts an unmatched request under
// /api/v1 is answered by [apiNotFoundHandler], never by the SPA index.
func TestBuilderUnknownAPIRoutesAnswerJSON(t *testing.T) {
	paths := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/no-such-route"},
		{method: http.MethodGet, path: "/builder/no-such-route"},
		{method: http.MethodPost, path: "/builder/no-such-route"},
	}

	harness := newBuilderHarness(t)

	// Registered after the Builder routes, exactly as [Start] does.
	harness.api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	for _, tt := range paths {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			recorder := harness.do(builderRequest{method: tt.method, path: tt.path, user: builderAlice})

			if recorder.Code != http.StatusNotFound {
				t.Errorf("%s %s: status = %d, want %d", tt.method, tt.path, recorder.Code, http.StatusNotFound)
			}

			if contentType := recorder.Header().Get("Content-Type"); contentType != mimeJSON {
				t.Errorf("%s %s: Content-Type = %q, want %q", tt.method, tt.path, contentType, mimeJSON)
			}

			if strings.Contains(recorder.Body.String(), builderSPABody) {
				t.Errorf("%s %s: the SPA index answered an API request", tt.method, tt.path)
			}

			var body struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: decoding the body: %v", tt.method, tt.path, err)
			}

			if want := "no API route matches this request"; body.Message != want {
				t.Errorf("%s %s: message = %q, want %q", tt.method, tt.path, body.Message, want)
			}
		})
	}

	// The SPA fallback still answers everything outside the API.
	recorder := httptest.NewRecorder()
	harness.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/builder", nil))

	if recorder.Code != http.StatusOK {
		t.Errorf("GET /builder: status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if !strings.Contains(recorder.Body.String(), builderSPABody) {
		t.Errorf("GET /builder = %s, want the SPA index", recorder.Body)
	}
}

// TestBuilderRegistersOnUnusedStore asserts registering the routes writes
// nothing to an empty store, even when the topologies cannot be listed.
func TestBuilderRegistersOnUnusedStore(t *testing.T) {
	tests := []struct {
		name string
		list func(kind string) (store.Configs, error)
	}{
		{
			name: "no topologies",
			list: func(string) (store.Configs, error) { return nil, nil },
		},
		{
			name: "listing fails",
			list: func(string) (store.Configs, error) { return nil, errors.New("injected listing failure") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				records = memrecord.New()
				writes  int
			)

			count := func(string, string) error {
				writes++

				return nil
			}

			records.BeforeCreate = count
			records.BeforeUpdate = count
			records.FailDelete = count
			records.FailPrefixDelete = count

			service, err := bapi.New(bapi.WithStore(records))
			if err != nil {
				t.Fatalf("bapi.New returned error: %v", err)
			}

			harness := newBuilderHarnessWith(t, []builderOption{
				withBuilderService(service),
				withBuilderConfigs(tt.list, func(string) (*store.Config, error) { return nil, store.ErrNotExist }),
			})

			if writes != 0 {
				t.Errorf("registering wrote to or deleted from the store %d times, want 0", writes)
			}

			for _, namespace := range []string{
				bapi.NamespaceDrafts, bapi.NamespaceContent, bapi.NamespacePublished,
			} {
				if got := records.Count(namespace); got != 0 {
					t.Errorf("%s holds %d records, want 0", namespace, got)
				}
			}

			if err := builderStatusError(harness.as(builderAlice, nil).do(
				builderRequest{method: http.MethodGet, path: "/builder/drafts"},
			), http.StatusOK); err != nil {
				t.Errorf("GET /builder/drafts: %v", err)
			}
		})
	}
}

func TestBuilderPublishTopology(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "topo")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)

	if response.Status != "succeeded" {
		t.Errorf("publishing topo: status = %q, want succeeded: %#v", response.Status, response)
	}

	if len(response.Stages) != 3 {
		t.Errorf("publishing topo: %d stages, want 3: %#v", len(response.Stages), response.Stages)
	}

	topology, err := harness.getConfig("Topology/topo")
	if err != nil {
		t.Fatalf("getConfig(Topology/topo) returned error: %v", err)
	}

	reference, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference(builder-doc annotation) returned error: %v", err)
	}

	// The annotation holds the document's digest and ID and nothing else:
	// which draft and snapshot it was published from is on its record.
	published, err := harness.service.GetPublishedDocument(context.Background(), reference.StoredID("topo"))
	if err != nil {
		t.Fatalf("GetPublishedDocument(%s) returned error: %v", reference.StoredID("topo"), err)
	}

	if diff := cmp.Diff(published.Reference(), reference); diff != "" {
		t.Errorf("builder-doc annotation mismatch with the published document (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(bapi.DocumentReference{Digest: draft.Digest, ID: reference.ID, Path: ""}, reference); diff != "" {
		t.Errorf("builder-doc annotation mismatch with the draft (-want +got):\n%s", diff)
	}

	type origin struct{ SnapshotID, DraftID, Schema string }

	want := origin{SnapshotID: draft.SnapshotID, DraftID: draft.ID, Schema: bdoc.SchemaURI}
	if diff := cmp.Diff(want, origin{published.SnapshotID, published.DraftID, published.Schema}); diff != "" {
		t.Errorf("published document %s mismatch (-want +got):\n%s", published.ID, diff)
	}

	meta := mustDraftMeta(t, harness, draft.ID)
	if meta.Dirty() {
		t.Errorf("draft %s is dirty after publishing, want clean", draft.ID)
	}

	if meta.Publication == nil {
		t.Errorf("draft %s publication = nil, want document %s", draft.ID, reference.ID)
	} else if got := meta.Publication.DocumentID; got != reference.ID {
		t.Errorf("draft %s publication document = %q, want %q", draft.ID, got, reference.ID)
	}
}

// TestBuilderConfigRoutesNeedConfigPermission asserts the routes that read or
// write phenix configs refuse the Builder permission alone with 403.
func TestBuilderConfigRoutesNeedConfigPermission(t *testing.T) {
	harness := newBuilderHarness(t)
	requests := []builderRequest{
		{method: http.MethodGet, path: "/builder/sources"},
		{method: http.MethodPost, path: "/builder/generate", body: `{}`},
		{method: http.MethodPost, path: "/builder/legacy", body: `{}`},
		{method: http.MethodPost, path: "/builder/export/topology", body: `{}`},
		{method: http.MethodPost, path: "/builder/package", body: `{}`},
		{method: http.MethodPost, path: "/builder/package/resolve", body: `{}`},
		{method: http.MethodGet, path: "/builder/documents"},
	}
	cases := make([]builderAccessCase, 0, len(requests))

	for _, request := range requests {
		cases = append(cases, builderAccessCase{
			name: request.method + " " + request.path, user: builderAlice, role: builderOnlyRole(),
			request: request, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		})
	}

	runBuilderAccessCases(t, harness, cases)
}

func TestBuilderCreateDraft(t *testing.T) {
	harness := newBuilderHarness(t)

	var draft builderDraftResponse

	recorder := harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: `{"title":"fallback","sourceToken":"Topology/foo","document":` + string(builderDocument(t, "topo")) + `}`,
	}, http.StatusCreated, &draft)

	type creation struct{ CreatedBy, Title string }

	// The document names itself, so the request title is only a fallback.
	if diff := cmp.Diff(creation{CreatedBy: builderAlice, Title: "topo"}, creation{draft.CreatedBy, draft.Title}); diff != "" {
		t.Errorf("POST /builder/drafts mismatch (-want +got):\n%s", diff)
	}

	if draft.ETag == "" || !strings.HasPrefix(draft.ETag, `"`) {
		t.Errorf("POST /builder/drafts: etag = %q, want a quoted entity tag", draft.ETag)
	}

	if header := recorder.Header().Get("ETag"); header != draft.ETag {
		t.Errorf("POST /builder/drafts: ETag header = %q, want %q", header, draft.ETag)
	}

	want := "/api/v1/builder/drafts/" + draft.ID
	if location := recorder.Header().Get("Location"); location != want {
		t.Errorf("POST /builder/drafts: Location = %q, want %q", location, want)
	}
}

// TestBuilderCreateDraftFromPublishedDocument asserts a draft may name a
// published document only when the caller may get its config. Others are 404.
func TestBuilderCreateDraftFromPublishedDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	secret := builderPublish(t, harness, "secret")
	public := builderPublish(t, harness, "public")

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get", "create"},
	))

	document := string(builderDocument(t, "topo"))

	create := func(token string) *httptest.ResponseRecorder {
		return harness.do(builderRequest{
			method: http.MethodPost,
			path:   "/builder/drafts",
			body:   `{"sourceToken":"` + token + `","document":` + document + `}`,
			user:   builderAlice,
			role:   role,
		})
	}

	for _, tt := range []struct{ name, id string }{
		{name: "a document the role may not get", id: secret.ID},
		{name: "a missing document", id: "missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := create(builderDocTokenPrefix + tt.id)

			if recorder.Code != http.StatusNotFound {
				t.Errorf("POST /builder/drafts from document %s: status = %d, want %d: %s",
					tt.id, recorder.Code, http.StatusNotFound, recorder.Body)
			}

			if want := "phenix cannot find document " + tt.id; !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("POST /builder/drafts from document %s = %s, want %q", tt.id, recorder.Body, want)
			}
		})
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}

	recorder := create(builderDocTokenPrefix + public.ID)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /builder/drafts from document %s: status = %d, want %d: %s",
			public.ID, recorder.Code, http.StatusCreated, recorder.Body)
	}

	var draft builderDraftResponse

	harness.decode(recorder, &draft)

	if draft.SourceToken != builderDocTokenPrefix+public.ID {
		t.Errorf("POST /builder/drafts from document %s: source token = %q, want %q",
			public.ID, draft.SourceToken, builderDocTokenPrefix+public.ID)
	}
}

func TestBuilderCreateDraftRequests(t *testing.T) {
	harness := newBuilderHarness(t)

	document := string(builderDocument(t, "topo"))

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
			body:   `{"title":"` + strings.Repeat("x", builderMaxRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/drafts",
				body:   tt.body,
				user:   builderAlice,
			})

			if recorder.Code != tt.status {
				t.Errorf("POST /builder/drafts: status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 0 {
		t.Errorf("stored drafts = %d, want 0", count)
	}
}

func TestBuilderGetDraft(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "topo")

	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/drafts/" + draft.ID,
		user:   builderAlice,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /builder/drafts/%s: status = %d, want %d", draft.ID, recorder.Code, http.StatusOK)
	}

	if etag := recorder.Header().Get("ETag"); etag != draft.ETag {
		t.Errorf("GET /builder/drafts/%s: ETag = %q, want %q", draft.ID, etag, draft.ETag)
	}

	var response builderDraftResponse
	harness.decode(recorder, &response)

	type envelope struct {
		HasDocument bool
		History     int
		CreatedBy   string
	}

	want := envelope{HasDocument: true, History: 1, CreatedBy: builderAlice}
	got := envelope{HasDocument: len(response.Document) != 0, History: len(response.History), CreatedBy: response.CreatedBy}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /builder/drafts/%s envelope mismatch (-want +got):\n%s", draft.ID, diff)
	}
}

func TestBuilderGetDraftNotFound(t *testing.T) {
	harness := newBuilderHarness(t)

	tests := []struct{ name, path string }{
		{name: "unknown ID", path: "/builder/drafts/id-missing"},
		// Identifiers that cannot name a draft never reach the store.
		{name: "ID with a space", path: "/builder/drafts/bad%20id"},
		{name: "ID too long", path: "/builder/drafts/" + strings.Repeat("x", 200)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodGet,
				path:   tt.path,
				user:   builderAlice,
			})

			if recorder.Code != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want %d", tt.path, recorder.Code, http.StatusNotFound)
			}
		})
	}
}

// TestBuilderListDrafts asserts every Builder user lists every draft, the
// most recently changed first, each with its creator.
func TestBuilderListDrafts(t *testing.T) {
	harness := newBuilderHarness(t)
	older := harness.createDraft(builderAlice, "older")
	newer := harness.createDraft(builderBob, "newer")

	type listed struct{ ID, CreatedBy string }

	want := []listed{{ID: newer.ID, CreatedBy: builderBob}, {ID: older.ID, CreatedBy: builderAlice}}

	for _, user := range []string{builderAlice, builderCarol} {
		t.Run(user, func(t *testing.T) {
			var listing struct {
				Drafts  []builderDraftResponse        `json:"drafts"`
				Damaged []builderDamagedDraftResponse `json:"damaged"`
			}

			recorder := harness.as(user, builderOnlyRole()).do(builderRequest{method: http.MethodGet, path: "/builder/drafts"})
			if err := builderStatusError(recorder, http.StatusOK); err != nil {
				t.Fatalf("GET /builder/drafts as %s: %v", user, err)
			}

			decodeBuilderAnswer(t, recorder, &listing)

			got := make([]listed, 0, len(listing.Drafts))
			for _, draft := range listing.Drafts {
				got = append(got, listed{ID: draft.ID, CreatedBy: draft.CreatedBy})
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GET /builder/drafts as %s mismatch (-want +got):\n%s", user, diff)
			}

			if len(listing.Damaged) != 0 {
				t.Errorf("GET /builder/drafts as %s: damaged = %+v, want none", user, listing.Damaged)
			}
		})
	}
}

func TestBuilderSnapshotRoundTrip(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "first")
	path := "/builder/drafts/" + draft.ID

	var saved, moved builderDraftResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", user: builderAlice, ifMatch: draft.ETag,
		body: `{"summary":"second","document":` + string(builderDocument(t, "second")) + `,"opId":"op-2"}`,
	}, http.StatusCreated, &saved)

	if saved.ETag == draft.ETag {
		t.Errorf("POST %s/snapshots: ETag = %q, want a new one", path, saved.ETag)
	}

	if saved.Snapshots != 2 {
		t.Errorf("POST %s/snapshots: %d snapshots, want 2", path, saved.Snapshots)
	}

	// The client's id for the save comes back, so a client that lost the answer can tell it was stored.
	var snapshot builderSnapshotDocumentResponse

	harness.doDecode(builderRequest{method: http.MethodGet, path: path + "/snapshots/current", user: builderAlice},
		http.StatusOK, &snapshot)

	type current struct {
		Current bool
		OpID    string
	}

	if diff := cmp.Diff(current{Current: true, OpID: "op-2"}, current{snapshot.Snapshot.Current, snapshot.Snapshot.OpID}); diff != "" {
		t.Errorf("GET %s/snapshots/current mismatch (-want +got):\n%s", path, diff)
	}

	if !strings.Contains(string(snapshot.Document), "second") {
		t.Errorf("GET %s/snapshots/current = %s, want the saved document", path, snapshot.Document)
	}

	harness.doDecode(builderRequest{
		method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderAlice, ifMatch: saved.ETag,
	}, http.StatusOK, &moved)

	type position struct{ Cursor, Snapshots int }

	if diff := cmp.Diff(position{Cursor: 0, Snapshots: 2}, position{moved.Cursor, moved.Snapshots}); diff != "" {
		t.Errorf("PATCH %s/cursor to index 0 mismatch (-want +got):\n%s", path, diff)
	}
}

// TestBuilderDeleteSnapshot deletes a version from a draft's history. The
// current version cannot be deleted.
func TestBuilderDeleteSnapshot(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "first")
	path := "/builder/drafts/" + draft.ID

	var (
		saved   builderDraftResponse
		history struct {
			Snapshots []builderSnapshotResponse `json:"snapshots"`
		}
	)

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", user: builderAlice, ifMatch: draft.ETag,
		body: `{"document":` + string(builderDocument(t, "second")) + `}`,
	}, http.StatusCreated, &saved)
	harness.doDecode(builderRequest{method: http.MethodGet, path: path + "/snapshots", user: builderAlice},
		http.StatusOK, &history)

	if len(history.Snapshots) != 2 {
		t.Fatalf("GET %s/snapshots = %+v, want 2 snapshots", path, history)
	}

	if got := history.Snapshots[0].CreatedBy; got != builderAlice {
		t.Errorf("GET %s/snapshots: first saved by %q, want %q", path, got, builderAlice)
	}

	remove := func(snapshot, ifMatch string) builderRequest {
		return builderRequest{
			method: http.MethodDelete, path: path + "/snapshots/" + snapshot, user: builderAlice, ifMatch: ifMatch,
		}
	}

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "the current version", user: builderAlice, request: remove(builderCurrentSnapshot, saved.ETag),
			status: http.StatusConflict, code: bdoc.CodeDraftSnapshotCurrent,
		},
		{
			name: "the current version by ID", user: builderAlice, request: remove(history.Snapshots[1].ID, saved.ETag),
			status: http.StatusConflict, code: bdoc.CodeDraftSnapshotCurrent,
		},
		{name: "an unknown version", user: builderAlice, request: remove("id-missing", saved.ETag), status: http.StatusNotFound},
		{
			name: "a stale ETag", user: builderAlice, request: remove(history.Snapshots[0].ID, draft.ETag),
			status: http.StatusPreconditionFailed, code: bdoc.CodeDraftStale,
		},
	})

	var deleted builderDraftResponse

	recorder := harness.doDecode(remove(history.Snapshots[0].ID, saved.ETag), http.StatusOK, &deleted)

	if deleted.Snapshots != 1 {
		t.Errorf("DELETE %s/snapshots/%s: %d snapshots, want 1", path, history.Snapshots[0].ID, deleted.Snapshots)
	}

	if deleted.ETag == saved.ETag {
		t.Errorf("DELETE %s/snapshots/%s: ETag = %q, want a new one", path, history.Snapshots[0].ID, deleted.ETag)
	}

	if header := recorder.Header().Get("ETag"); header != deleted.ETag {
		t.Errorf("DELETE %s/snapshots/%s: ETag header = %q, want %q", path, history.Snapshots[0].ID, header, deleted.ETag)
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: path + "/snapshots/" + history.Snapshots[0].ID, user: builderAlice},
		http.StatusNotFound, nil)
}

func TestBuilderCursorRequests(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "topo")
	path := "/builder/drafts/" + draft.ID + "/cursor"

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
			recorder := harness.do(builderRequest{
				method:  http.MethodPatch,
				path:    path,
				body:    tt.body,
				user:    builderAlice,
				ifMatch: draft.ETag,
			})

			if recorder.Code != tt.status {
				t.Errorf("PATCH %s %s: status = %d, want %d: %s", path, tt.body, recorder.Code, tt.status, recorder.Body)
			}
		})
	}

	// Only PATCH moves the cursor.
	recorder := harness.do(builderRequest{
		method: http.MethodPut, path: path, body: `{"index":0}`, user: builderAlice, ifMatch: draft.ETag,
	})

	got := builderAnswer{Status: recorder.Code, Allow: recorder.Header().Get("Allow")}
	want := builderAnswer{Status: http.StatusMethodNotAllowed, Allow: "PATCH, OPTIONS"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PUT %s mismatch (-want +got):\n%s", path, diff)
	}
}

// TestBuilderIfMatchRequired asserts every mutation after creation refuses
// to run without a usable entity tag, and that a 412 carries the current one.
func TestBuilderIfMatchRequired(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "topo")

	var (
		path     = "/builder/drafts/" + draft.ID
		document = string(builderDocument(t, "second"))
	)

	mutations := []struct {
		name    string
		request builderRequest
	}{
		{"save", builderRequest{method: http.MethodPost, path: path + "/snapshots", body: `{"document":` + document + `}`}},
		{"move the cursor", builderRequest{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`}},
		{"delete the current snapshot", builderRequest{method: http.MethodDelete, path: path + "/snapshots/current"}},
		{"delete the draft", builderRequest{method: http.MethodDelete, path: path}},
		{"publish", builderRequest{
			method: http.MethodPost, path: path + "/publish",
			body: `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
		}},
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
			t.Run(mutation.name+" with a "+tag.name+" tag", func(t *testing.T) {
				request := mutation.request
				request.user = builderAlice
				request.ifMatch = tag.ifMatch

				recorder := harness.do(request)

				if recorder.Code != tag.status {
					t.Errorf("%s %s with a %s tag: status = %d, want %d",
						request.method, request.path, tag.name, recorder.Code, tag.status)
				}

				// A 412 names the draft's current tag.
				if etag := recorder.Header().Get("ETag"); tag.status == http.StatusPreconditionFailed && etag != draft.ETag {
					t.Errorf("%s %s with a %s tag: ETag = %q, want %q",
						request.method, request.path, tag.name, etag, draft.ETag)
				}
			})
		}
	}

	// Nothing above changed the draft.
	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   path,
		user:   builderAlice,
	})

	if etag := recorder.Header().Get("ETag"); etag != draft.ETag {
		t.Errorf("GET %s: ETag = %q, want the unchanged %q", path, etag, draft.ETag)
	}
}

// TestBuilderPublishedDocumentsRequireCurrentConfigReference ensures stale
// immutable records never remain visible after their config reference changes.
func TestBuilderPublishedDocumentsRequireCurrentConfigReference(t *testing.T) {
	harness := newBuilderHarness(t)

	// listDocuments returns the IDs of the documents GET /builder/documents lists.
	listDocuments := func() []string {
		t.Helper()

		recorder := harness.do(builderRequest{
			method: http.MethodGet,
			path:   "/builder/documents",
			user:   builderAlice,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET /builder/documents: status = %d, want %d", recorder.Code, http.StatusOK)
		}

		var response struct {
			Documents []builderDocumentResponse `json:"documents"`
		}

		harness.decode(recorder, &response)

		ids := make([]string, 0, len(response.Documents))
		for _, document := range response.Documents {
			ids = append(ids, document.ID)
		}

		return ids
	}

	getStatus := func(id string) int {
		t.Helper()

		return harness.do(builderRequest{
			method: http.MethodGet,
			path:   "/builder/documents/" + id,
			user:   builderAlice,
		}).Code
	}

	published, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:   "current-topology",
		Kind:     builderKindTopology,
		Actor:    builderAlice,
		Document: builderDocument(t, "current"),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument(current) returned error: %v", err)
	}

	reference, err := published.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference(current) returned error: %v", err)
	}

	topology, err := store.NewConfig("Topology/current-topology")
	if err != nil {
		t.Fatalf("NewConfig(Topology/current-topology) returned error: %v", err)
	}

	topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, *topology)

	if diff := cmp.Diff([]string{published.ID}, listDocuments()); diff != "" {
		t.Errorf("GET /builder/documents mismatch (-want +got):\n%s", diff)
	}

	if status := getStatus(published.ID); status != http.StatusOK {
		t.Errorf("GET /builder/documents/%s: status = %d, want %d", published.ID, status, http.StatusOK)
	}

	// Missing annotations hide an otherwise valid immutable record.
	delete(harness.configs[0].Metadata.Annotations, bapi.DocumentAnnotation)
	if listed := listDocuments(); len(listed) != 0 {
		t.Errorf("GET /builder/documents without the annotation = %q, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Errorf("GET /builder/documents/%s without the annotation: status = %d, want %d",
			published.ID, status, http.StatusNotFound)
	}

	// Malformed references fail closed without making the listing unavailable.
	harness.configs[0].Metadata.Annotations[bapi.DocumentAnnotation] = "{"
	if listed := listDocuments(); len(listed) != 0 {
		t.Errorf("GET /builder/documents with a malformed reference = %q, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Errorf("GET /builder/documents/%s with a malformed reference: status = %d, want %d",
			published.ID, status, http.StatusNotFound)
	}

	// Deleting the target config makes the document an inaccessible orphan.
	harness.configs = nil
	if listed := listDocuments(); len(listed) != 0 {
		t.Errorf("GET /builder/documents after deleting the config = %q, want none", listed)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Errorf("GET /builder/documents/%s after deleting the config: status = %d, want %d",
			published.ID, status, http.StatusNotFound)
	}

	// Replacing the annotation exposes only the new immutable document.
	replacement, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:   "current-topology",
		Kind:     builderKindTopology,
		Actor:    builderAlice,
		Document: builderDocument(t, "replacement"),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument(replacement) returned error: %v", err)
	}

	replacementReference, err := replacement.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference(replacement) returned error: %v", err)
	}

	topology.Metadata.Annotations[bapi.DocumentAnnotation] = replacementReference
	harness.configs = append(harness.configs, *topology)

	if diff := cmp.Diff([]string{replacement.ID}, listDocuments()); diff != "" {
		t.Errorf("GET /builder/documents after replacing the annotation mismatch (-want +got):\n%s", diff)
	}
	if status := getStatus(published.ID); status != http.StatusNotFound {
		t.Errorf("GET /builder/documents/%s after replacing it: status = %d, want %d",
			published.ID, status, http.StatusNotFound)
	}
	if status := getStatus(replacement.ID); status != http.StatusOK {
		t.Errorf("GET /builder/documents/%s, the replacement: status = %d, want %d",
			replacement.ID, status, http.StatusOK)
	}
}

// TestBuilderErrorStatuses asserts every sentinel [phenix/api/builder]
// documents is mapped to a status.
func TestBuilderErrorStatuses(t *testing.T) {
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

			webErr := builderWebError(tt.err, "unable to do the thing")

			if webErr.Status != tt.status {
				t.Errorf("builderWebError(%v) status = %d, want %d", tt.err, webErr.Status, tt.status)
			}
		})
	}
}

// TestBuilderSchemaRoutes asserts each Builder schema route serves its schema
// as JSON, ahead of the generic "/schemas/{kind}/{version}" route.
func TestBuilderSchemaRoutes(t *testing.T) {
	harness := newBuilderHarness(t)

	// Registered after the Builder routes, exactly as [Start] does.
	harness.api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	want, err := bdoc.SchemaJSON()
	if err != nil {
		t.Fatalf("SchemaJSON returned error: %v", err)
	}

	for path, schema := range map[string]func() ([]byte, error){
		builderSchemaPath:         bdoc.SchemaJSON,
		builderTemplateSchemaPath: bdoc.TemplateFileSchemaJSON,
		builderPackageSchemaPath:  bdoc.PackageSchemaJSON,
		builderLayerSetSchemaPath: bdoc.LayerSetFileSchemaJSON,
	} {
		t.Run(path, func(t *testing.T) {
			served, err := schema()
			if err != nil {
				t.Fatalf("the schema of %s: %v", path, err)
			}

			recorder := harness.as(builderAlice, builderOnlyRole()).do(builderRequest{method: http.MethodGet, path: path})
			if err := builderStatusError(recorder, http.StatusOK); err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}

			if got := recorder.Header().Get("Content-Type"); got != mimeJSON {
				t.Errorf("GET %s: Content-Type = %q, want %q", path, got, mimeJSON)
			}

			// The exact bytes served to clients.
			if diff := cmp.Diff(string(served), recorder.Body.String()); diff != "" {
				t.Errorf("GET %s schema mismatch (-want +got):\n%s", path, diff)
			}
		})
	}

	other := harness.do(builderRequest{method: http.MethodGet, path: "/schemas/topology/v1", user: builderAlice})
	if other.Code != http.StatusOK {
		t.Errorf("GET /schemas/topology/v1: status = %d, want %d", other.Code, http.StatusOK)
	}

	if bytes.Equal(other.Body.Bytes(), want) {
		t.Error("GET /schemas/topology/v1 = the Builder schema, want the generic Topology schema")
	}

	// The generic handler answers a kind or version it has no schema for with 404.
	for _, path := range []string{"/schemas/nope/v1", "/schemas/topology/v9"} {
		t.Run(path, func(t *testing.T) {
			recorder := harness.do(builderRequest{method: http.MethodGet, path: path, user: builderAlice})

			if recorder.Code != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want %d: %s", path, recorder.Code, http.StatusNotFound, recorder.Body)
			}
		})
	}
}

// TestBuilderRegisteredBeforeGenericSchema guards the registration order in
// server.go, which TestBuilderSchemaRoutes cannot see.
func TestBuilderRegisteredBeforeGenericSchema(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	var (
		builderRoutes = bytes.Index(source, []byte("registerBuilderRoutes(api,"))
		generic       = bytes.Index(source, []byte(`"/schemas/{kind}/{version}"`))
	)

	if builderRoutes < 0 {
		t.Error("server.go does not register the Builder routes")
	}

	if generic < 0 {
		t.Error("server.go does not register the generic schema route")
	}

	if builderRoutes >= 0 && generic >= 0 && builderRoutes > generic {
		t.Error("server.go registers the Builder routes after /schemas/{kind}/{version}, want them before")
	}
}

// TestBuilderRoutesNeedTheBuilder asserts every Builder route and method
// refuses no identity, Global Viewer and a configs-only role with 403.
func TestBuilderRoutesNeedTheBuilder(t *testing.T) {
	viewer := builtinRole(t, "global-viewer")
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "kept")
	variable := regexp.MustCompile(`\{[^}]+\}`)
	operations := 0

	err := harness.api.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, templateErr := route.GetPathTemplate()
		methods, methodsErr := route.GetMethods()

		if templateErr != nil || methodsErr != nil {
			return nil //nolint:nilerr // routes without a path or methods are not operations
		}

		path := variable.ReplaceAllString(strings.TrimPrefix(template, "/api/v1"), draft.ID)

		for _, method := range methods {
			if method == http.MethodOptions {
				continue
			}

			operations++

			t.Run(method+" "+template, func(t *testing.T) {
				for name, caller := range map[string]builderCaller{
					"no identity":   harness.as("", nil),
					"Global Viewer": harness.as(builderBob, viewer),
					"configs only":  harness.as(builderBob, builderConfigsOnlyRole()),
				} {
					recorder := caller.do(builderRequest{method: method, path: path, body: `{}`, ifMatch: draft.ETag})
					if recorder.Code != http.StatusForbidden {
						t.Errorf("%s %s as %s: status = %d, want 403: %s", method, template, name, recorder.Code, recorder.Body)
					} else if code := builderRefusal(t, recorder).Code; code != string(bdoc.CodeRequestForbidden) {
						t.Errorf("%s %s as %s: code = %q, want %q", method, template, name, code, bdoc.CodeRequestForbidden)
					}
				}
			})
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the routes: %v", err)
	}

	if operations < 40 {
		t.Errorf("checked %d operations, want every Builder route (at least 40)", operations)
	}

	if meta := mustDraftMeta(t, harness, draft.ID); meta.ETag() != draft.ETag {
		t.Errorf("draft %s ETag = %s after the refused requests, want the unchanged %s", draft.ID, meta.ETag(), draft.ETag)
	}

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 1 {
		t.Errorf("stored drafts = %d, want only the kept one", count)
	}
}

// TestBuilderLibrariesNeedOnlyTheBuilder asserts the Builder permission alone
// may use the template, icon and layer set libraries.
func TestBuilderLibrariesNeedOnlyTheBuilder(t *testing.T) {
	harness, requests := builderTemplatePermissionRequests(t)
	statuses := []int{
		http.StatusOK, http.StatusCreated, http.StatusOK, http.StatusCreated, http.StatusOK, http.StatusOK,
		http.StatusOK, http.StatusOK, http.StatusCreated, http.StatusOK, http.StatusNoContent,
		http.StatusOK, http.StatusOK, http.StatusCreated, http.StatusOK, http.StatusNoContent, http.StatusOK, http.StatusOK,
	}

	if _, _, err := harness.service.AddIcon(t.Context(), builderBob, "plc", builderIconPNG(t, 1, 1, 6)); err != nil {
		t.Fatalf("AddIcon returned error: %v", err)
	}

	gone, _, err := harness.service.AddIcon(t.Context(), builderBob, "gone", builderIconPNG(t, 1, 1, 8))
	if err != nil {
		t.Fatalf("AddIcon returned error: %v", err)
	}

	set, err := harness.service.CreateLayerSet(t.Context(), builderBob, bdoc.BuiltinLayerSets()[0].LayerSet)
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
	}

	setPath := builderLayerSetsRoute + "/" + set.ID
	requests = append(requests,
		builderRequest{method: http.MethodGet, path: builderIconsRoute},
		builderRequest{method: http.MethodGet, path: builderIconsRoute + "/plc"},
		builderRequest{
			method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "other", builderIconPNG(t, 1, 1, 7)),
		},
		builderRequest{method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`},
		builderRequest{method: http.MethodDelete, path: builderIconsRoute + "/gone", ifMatch: gone.ETag()},
		builderRequest{method: http.MethodGet, path: builderLayerSetsRoute},
		builderRequest{method: http.MethodGet, path: setPath},
		builderRequest{method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "x")},
		builderRequest{method: http.MethodPut, path: setPath, body: builderLayerSetBody(t, "y"), ifMatch: `"1"`},
		builderRequest{method: http.MethodDelete, path: setPath, ifMatch: `"2"`},
		builderRequest{method: http.MethodGet, path: builderLayerSetsRoute + "/default"},
		builderRequest{method: http.MethodPut, path: builderLayerSetsRoute + "/default", body: `{"id":null}`, ifMatch: `"0"`},
	)

	builder := harness.as(builderAlice, builderOnlyRole())

	for i, request := range requests {
		what := request.method + " " + request.path

		t.Run(what, func(t *testing.T) {
			recorder := builder.do(request)
			if err := builderStatusError(recorder, statuses[i]); err != nil {
				t.Fatalf("%s: %v", what, err)
			}

			if err := builderJSONError(recorder); err != nil {
				t.Errorf("%s: %v", what, err)
			}
		})
	}
}

// TestBuilderRoutesDocumented asserts every Builder route and method
// is described in the OpenAPI document the server publishes under /docs/.
func TestBuilderRoutesDocumented(t *testing.T) {
	harness := newBuilderHarness(t)

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
		t.Error("checked no operations, want every registered Builder route")
	}
}

// openAPIProperty is what TestBuilderDocumentDocumented reads of a property
// in openapi.yml.
type openAPIProperty struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Example     any    `yaml:"example"`
}

// openAPIObject is what TestBuilderDocumentDocumented reads of a component.
type openAPIObject struct {
	Properties map[string]openAPIProperty `yaml:"properties"`
}

// TestBuilderDocumentDocumented asserts the OpenAPI document components list
// exactly the JSON fields of their models, each with a title, description and example.
func TestBuilderDocumentDocumented(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("reading openapi.yml: %v", err)
	}

	var spec struct {
		Components struct {
			Schemas struct {
				Document openAPIObject `yaml:"BuilderDocument"`
				Metadata openAPIObject `yaml:"BuilderDocumentMetadata"`
				Issue    openAPIObject `yaml:"BuilderIssue"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}

	if err := yaml.Unmarshal(source, &spec); err != nil {
		t.Fatalf("parsing openapi.yml: %v", err)
	}

	components := []struct {
		name   string
		object openAPIObject
		model  reflect.Type
	}{
		{"BuilderDocument", spec.Components.Schemas.Document, reflect.TypeFor[bdoc.Document]()},
		{"BuilderDocumentMetadata", spec.Components.Schemas.Metadata, reflect.TypeFor[bdoc.Metadata]()},
		{"BuilderIssue", spec.Components.Schemas.Issue, reflect.TypeFor[bdoc.Issue]()},
	}

	for _, component := range components {
		t.Run(component.name, func(t *testing.T) {
			t.Parallel()

			documented := slices.Sorted(maps.Keys(component.object.Properties))

			if diff := cmp.Diff(jsonFieldNames(component.model), documented); diff != "" {
				t.Errorf("%s properties mismatch with the JSON fields of %s (-want +got):\n%s",
					component.name, component.model, diff)
			}

			for _, name := range documented {
				property := component.object.Properties[name]

				if strings.TrimSpace(property.Title) == "" {
					t.Errorf("%s.%s has no title", component.name, name)
				}

				if strings.TrimSpace(property.Description) == "" {
					t.Errorf("%s.%s has no description", component.name, name)
				}

				if property.Example == nil {
					t.Errorf("%s.%s has no example", component.name, name)
				}
			}
		})
	}
}

// jsonFieldNames returns the names encoding/json gives the fields of a
// struct type, sorted.
func jsonFieldNames(model reflect.Type) []string {
	names := make([]string, 0, model.NumField())

	for index := range model.NumField() {
		field := model.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		switch {
		case name == "-" || !field.IsExported():
			continue
		case name == "":
			name = field.Name
		}

		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// builderWarning returns the cleanup warning a response carries, if any.
func builderWarning(recorder *httptest.ResponseRecorder) string {
	return recorder.Header().Get("Warning")
}

// errBuilderContentDelete is what the harness store answers a delete of
// content that [builderRefuseContentDelete] refuses.
var errBuilderContentDelete = errors.New("content deletion is unavailable")

// builderRefuseContentDelete, as the harness store's FailDelete, refuses
// every delete of content.
func builderRefuseContentDelete(namespace, _ string) error {
	if namespace == bapi.NamespaceContent {
		return errBuilderContentDelete
	}

	return nil
}

// TestBuilderSnapshotSaveIsAtomic asserts a save whose discarded redo content
// cannot be removed stores nothing: the draft keeps its ETag, and the same
// save succeeds once the store removes content again.
func TestBuilderSnapshotSaveIsAtomic(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "first")
	path := "/builder/drafts/" + draft.ID

	var second, rewound, saved builderDraftResponse

	save := func(name, ifMatch string) builderRequest {
		return builderRequest{
			method: http.MethodPost, path: path + "/snapshots", user: builderAlice, ifMatch: ifMatch,
			body: `{"document":` + string(builderDocument(t, name)) + `}`,
		}
	}

	harness.doDecode(save("second", draft.ETag), http.StatusCreated, &second)
	harness.doDecode(builderRequest{
		method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderAlice, ifMatch: second.ETag,
	}, http.StatusOK, &rewound)

	harness.store.FailDelete = builderRefuseContentDelete

	harness.doDecode(save("third", rewound.ETag), http.StatusInternalServerError, nil)

	var current builderDraftResponse

	harness.doDecode(builderRequest{method: http.MethodGet, path: path, user: builderAlice}, http.StatusOK, &current)

	if current.ETag != rewound.ETag || current.Snapshots != 2 {
		t.Errorf("GET %s after a failed save = ETag %q with %d snapshots, want %q with 2",
			path, current.ETag, current.Snapshots, rewound.ETag)
	}

	harness.store.FailDelete = nil

	harness.doDecode(save("third", rewound.ETag), http.StatusCreated, &saved)

	if saved.Snapshots != 2 || saved.ETag == rewound.ETag {
		t.Errorf("POST %s/snapshots = ETag %q with %d snapshots, want a new ETag with 2", path, saved.ETag, saved.Snapshots)
	}
}

// TestBuilderDeleteDraft asserts a deleted draft is gone with its content,
// and a delete whose content cannot be removed deletes nothing.
func TestBuilderDeleteDraft(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "doomed")
	remove := builderRequest{
		method: http.MethodDelete, path: "/builder/drafts/" + draft.ID, user: builderAlice, ifMatch: draft.ETag,
	}

	harness.store.FailPrefixDelete = func(string, string) error { return errBuilderContentDelete }

	harness.doDecode(remove, http.StatusInternalServerError, nil)

	if count := harness.store.Count(bapi.NamespaceDrafts); count != 1 {
		t.Errorf("after a failed DELETE /builder/drafts/%s: %d drafts stored, want the draft kept", draft.ID, count)
	}

	harness.store.FailPrefixDelete = nil

	recorder := harness.doDecode(remove, http.StatusNoContent, nil)

	if recorder.Body.Len() != 0 {
		t.Errorf("DELETE /builder/drafts/%s = %q, want no body", draft.ID, recorder.Body)
	}

	for _, namespace := range []string{bapi.NamespaceDrafts, bapi.NamespaceContent} {
		if count := harness.store.Count(namespace); count != 0 {
			t.Errorf("after DELETE /builder/drafts/%s: %d records in %s, want none", draft.ID, count, namespace)
		}
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts/" + draft.ID, user: builderAlice},
		http.StatusNotFound, nil)
}

// TestBuilderUnreadableDraft asserts a draft whose metadata no longer decodes
// is listed apart as damaged, cannot be read, and can be deleted by anyone.
func TestBuilderUnreadableDraft(t *testing.T) {
	harness := newBuilderHarness(t)
	good := harness.createDraft(builderAlice, "good")
	bad := harness.createDraft(builderAlice, "bad")
	harness.damageDraft(bad.ID)

	var listing struct {
		Drafts  []builderDraftResponse        `json:"drafts"`
		Damaged []builderDamagedDraftResponse `json:"damaged"`
	}

	peer := harness.as(builderBob, builderOnlyRole())
	peer.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts"}, http.StatusOK, &listing)

	readable := make([]string, 0, len(listing.Drafts))
	for _, draft := range listing.Drafts {
		readable = append(readable, draft.ID)
	}

	if diff := cmp.Diff([]string{good.ID}, readable); diff != "" {
		t.Errorf("GET /builder/drafts readable drafts mismatch (-want +got):\n%s", diff)
	}

	if len(listing.Damaged) != 1 {
		t.Fatalf("GET /builder/drafts damaged = %+v, want %q", listing.Damaged, bad.ID)
	}

	type damaged struct{ ID, Title string }

	if diff := cmp.Diff(damaged{ID: bad.ID, Title: "bad"}, damaged{listing.Damaged[0].ID, listing.Damaged[0].Title}); diff != "" {
		t.Errorf("GET /builder/drafts damaged draft mismatch (-want +got):\n%s", diff)
	}

	if listing.Damaged[0].ETag == bad.ETag {
		t.Errorf("GET /builder/drafts: damaged draft ETag = %q, want a new one", listing.Damaged[0].ETag)
	}

	path := "/builder/drafts/" + bad.ID

	if err := builderStatusError(peer.do(builderRequest{method: http.MethodGet, path: path}), http.StatusInternalServerError); err != nil {
		t.Errorf("GET %s (a damaged draft): %v", path, err)
	}

	recorder := peer.mustDo("deleting it at a stale ETag", builderRequest{
		method: http.MethodDelete, path: path, ifMatch: bad.ETag,
	}, http.StatusPreconditionFailed)
	if current := recorder.Header().Get("ETag"); current != listing.Damaged[0].ETag {
		t.Errorf("DELETE %s at a stale ETag: ETag header = %q, want the listed %q", path, current, listing.Damaged[0].ETag)
	}

	if err := builderStatusError(peer.do(
		builderRequest{method: http.MethodDelete, path: path, ifMatch: listing.Damaged[0].ETag},
	), http.StatusNoContent); err != nil {
		t.Errorf("DELETE %s at the listed ETag: %v", path, err)
	}
}

// TestBuilderDraftLimit refuses a new draft when the server holds the most
// drafts it may, with the code and a message that says what to do, and lists
// the limit.
func TestBuilderDraftLimit(t *testing.T) {
	harness := newBuilderHarness(t)

	for i := range bapi.MaxDrafts {
		key := "full-" + strconv.Itoa(i)
		if _, err := harness.store.CreateRecord(bapi.NamespaceDrafts, key, []byte("{}")); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", key, err)
		}
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: `{"document":` + string(builderDocument(t, "one-more")) + `}`,
	})
	if err := builderStatusError(recorder, http.StatusRequestEntityTooLarge); err != nil {
		t.Fatal(err)
	}

	refusal := builderRefusal(t, recorder)

	if refusal.Code != string(bdoc.CodeDraftLimit) {
		t.Errorf("POST /builder/drafts past the limit: code = %q, want %q", refusal.Code, bdoc.CodeDraftLimit)
	}

	if want := "Clean up in the Builder settings"; !strings.Contains(refusal.Message, want) {
		t.Errorf("POST /builder/drafts past the limit: message = %q, want it to name %q", refusal.Message, want)
	}

	var listing struct {
		Limits builderDraftLimits `json:"limits"`
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts", user: builderAlice},
		http.StatusOK, &listing)

	if listing.Limits.Drafts != bapi.MaxDrafts {
		t.Errorf("GET /builder/drafts limits.drafts = %d, want %d", listing.Limits.Drafts, bapi.MaxDrafts)
	}
}
