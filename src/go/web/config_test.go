package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// revisionTestTopology is a valid topology, as the UI config editor sends it.
const revisionTestTopology = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"demo"},` +
	`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host-00"},` +
	`"hardware":{"os_type":"linux","drives":[{"image":"ubuntu.qc2"}]}}]}}`

// useRevisionTestStore stores the demo topology in a BoltDB store of the
// test's own, which enforces revisions, and returns its revision.
func useRevisionTestStore(t *testing.T) uint64 {
	t.Helper()

	storetest.Use(t)

	c, err := store.NewConfigFromJSON([]byte(revisionTestTopology))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}

	return c.Metadata.Revision
}

// configRequest is a request for the demo topology with the given headers.
func configRequest(t *testing.T, method string, headers map[string]string) *http.Request {
	t.Helper()

	req := validationTestRequest(t, method, "/api/v1/configs/topology/demo", mimeJSON,
		[]byte(revisionTestTopology), map[string]string{"kind": "topology", "name": "demo"})

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req
}

// webErrorStatus returns the status of a handler's error, or 0 for none.
func webErrorStatus(t *testing.T, err error) (int, *weberror.WebError) {
	t.Helper()

	if err == nil {
		return 0, nil
	}

	var werr *weberror.WebError
	if !errors.As(err, &werr) {
		t.Fatalf("handler error = %v, want a *weberror.WebError", err)
	}

	return werr.Status, werr
}

func TestGetConfigRevision(t *testing.T) {
	rev := useRevisionTestStore(t)
	etag := `"r` + strconv.FormatUint(rev, 10) + `"`

	w := httptest.NewRecorder()
	if err := GetConfig(w, configRequest(t, http.MethodGet, nil)); err != nil {
		t.Fatalf("GetConfig(GET topology/demo) = %v", err)
	}

	if got := w.Header().Get("ETag"); got != etag {
		t.Errorf("GetConfig(GET topology/demo) ETag = %q, want %q", got, etag)
	}

	if got := w.Header().Get("X-Phenix-Revision"); got != strconv.FormatUint(rev, 10) {
		t.Errorf("GetConfig(GET topology/demo) X-Phenix-Revision = %q, want %d", got, rev)
	}

	var body store.Config
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Errorf("GetConfig(GET topology/demo) = %s, not a config: %v", w.Body.String(), err)
	} else if body.Metadata.Revision != rev {
		t.Errorf("GetConfig(GET topology/demo) metadata.revision = %d, want %d", body.Metadata.Revision, rev)
	}

	w = httptest.NewRecorder()
	if err := GetConfig(w, configRequest(t, http.MethodGet, map[string]string{"If-None-Match": etag})); err != nil {
		t.Fatalf("GetConfig(GET topology/demo If-None-Match %s) = %v", etag, err)
	}

	if w.Code != http.StatusNotModified {
		t.Errorf("GetConfig(GET topology/demo If-None-Match %s) = %d, want %d", etag, w.Code, http.StatusNotModified)
	}

	if w.Body.Len() != 0 {
		t.Errorf("GetConfig(GET topology/demo If-None-Match %s) body = %q, want none", etag, w.Body.String())
	}
}

func TestUpdateConfigIfMatch(t *testing.T) {
	rev := useRevisionTestStore(t)
	etag := `"r` + strconv.FormatUint(rev, 10) + `"`
	stale := `"r` + strconv.FormatUint(rev-1, 10) + `"`

	status, werr := webErrorStatus(t, UpdateConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodPut, map[string]string{"If-Match": stale})))
	if status != http.StatusPreconditionFailed {
		t.Errorf("UpdateConfig(PUT If-Match %s, stale) = %d, want %d", stale, status, http.StatusPreconditionFailed)
	}

	if werr != nil && werr.UserMetadata["revision"] != strconv.FormatUint(rev, 10) {
		t.Errorf("UpdateConfig(PUT If-Match %s, stale) metadata.revision = %v, want %d", stale, werr.UserMetadata["revision"], rev)
	}

	if status, _ := webErrorStatus(t, UpdateConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodPut, map[string]string{"If-Match": "r1"}))); status != http.StatusBadRequest {
		t.Errorf("UpdateConfig(PUT If-Match r1, unquoted) = %d, want %d", status, http.StatusBadRequest)
	}

	w := httptest.NewRecorder()
	if err := UpdateConfig(w, configRequest(t, http.MethodPut, map[string]string{"If-Match": etag})); err != nil {
		t.Fatalf("UpdateConfig(PUT If-Match %s, current) = %v", etag, err)
	}

	if w.Code != http.StatusNoContent {
		t.Errorf("UpdateConfig(PUT If-Match %s, current) = %d, want %d", etag, w.Code, http.StatusNoContent)
	}

	newETag := w.Header().Get("ETag")
	if newETag == "" || newETag == etag {
		t.Fatalf("UpdateConfig(PUT If-Match %s, current) ETag = %q, want a new one", etag, newETag)
	}

	got, _ := store.NewConfig("topology/demo")
	if err := store.Get(got); err != nil {
		t.Errorf("store.Get(topology/demo) after the PUT = %v", err)
	} else if stored := `"r` + strconv.FormatUint(got.Metadata.Revision, 10) + `"`; stored != newETag {
		t.Errorf("stored revision ETag = %s, want the PUT's %s", stored, newETag)
	}

	// without If-Match, a PUT writes whatever the revision
	if err := UpdateConfig(httptest.NewRecorder(), configRequest(t, http.MethodPut, nil)); err != nil {
		t.Fatalf("UpdateConfig(PUT without If-Match) = %v", err)
	}

	status, _ = webErrorStatus(t, DeleteConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodDelete, map[string]string{"If-Match": newETag})))
	if status != http.StatusPreconditionFailed {
		t.Errorf("DeleteConfig(DELETE If-Match %s, stale) = %d, want %d", newETag, status, http.StatusPreconditionFailed)
	}

	if err := store.Get(got); err != nil {
		t.Errorf("store.Get(topology/demo) after a refused DELETE = %v, want the config kept", err)
	}

	if err := DeleteConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodDelete, map[string]string{"If-Match": "*"})); err != nil {
		t.Errorf("DeleteConfig(DELETE If-Match *) = %v", err)
	}
}

// An updated config's location is under the base path the UI is served
// behind.
func TestUpdateConfigLocationHasBasePath(t *testing.T) {
	useRevisionTestStore(t)

	original := o
	t.Cleanup(func() { o = original })

	o.basePath = "/phenix/"

	w := httptest.NewRecorder()
	if err := UpdateConfig(w, configRequest(t, http.MethodPut, nil)); err != nil {
		t.Fatalf("UpdateConfig(PUT topology/demo) = %v", err)
	}

	if got, want := w.Header().Get("Location"), "/phenix/api/v1/configs/topology/demo"; got != want {
		t.Errorf("UpdateConfig(PUT topology/demo) behind /phenix/: Location = %q, want %q", got, want)
	}
}

func TestGetConfigsEmptyList(t *testing.T) {
	useRevisionTestStore(t)

	req := validationTestRequest(t, http.MethodGet, "/api/v1/configs", "", nil, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole,
		testRole("configs", "list", "Scenario/*")))

	w := httptest.NewRecorder()
	if err := GetConfigs(w, req); err != nil {
		t.Fatalf("GetConfigs(GET /api/v1/configs) = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GetConfigs(GET /api/v1/configs) = %s, not JSON: %v", w.Body.String(), err)
	}

	// an empty list, not null
	if diff := cmp.Diff(map[string]any{"configs": []any{}}, got); diff != "" {
		t.Errorf("GetConfigs(GET /api/v1/configs) that the role sees none of mismatch (-want +got):\n%s", diff)
	}
}

// A config that does not exist is not found, whether read, updated or
// deleted.
func TestConfigNotFound(t *testing.T) {
	useRevisionTestStore(t)

	vars := map[string]string{"kind": "topology", "name": "none"}

	for method, handler := range map[string]func(http.ResponseWriter, *http.Request) error{
		http.MethodGet:    GetConfig,
		http.MethodPut:    UpdateConfig,
		http.MethodDelete: DeleteConfig,
	} {
		t.Run(method, func(t *testing.T) {
			req := validationTestRequest(t, method, "/api/v1/configs/topology/none", mimeJSON,
				[]byte(strings.Replace(revisionTestTopology, `"demo"`, `"none"`, 1)), vars)

			if status, _ := webErrorStatus(t, handler(httptest.NewRecorder(), req)); status != http.StatusNotFound {
				t.Errorf("%s /api/v1/configs/topology/none = %d, want %d", method, status, http.StatusNotFound)
			}
		})
	}
}

// topologyConfigJSON returns a valid Topology config with the given name,
// as JSON.
func topologyConfigJSON(name string) string {
	return `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"` + name + `"},` +
		`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host"},` +
		`"hardware":{"os_type":"linux","drives":[{"image":"host.qc2"}]}}]}}`
}

// TestConfigWritesCheckTheConfigTheyWrite asserts a role limited to some
// configs writes no other through any config write route, whatever the body names.
func TestConfigWritesCheckTheConfigTheyWrite(t *testing.T) {
	const (
		user = `{"apiVersion":"phenix.sandia.gov/v1","kind":"User","metadata":{"name":"mallory"},` +
			`"spec":{"username":"mallory","first_name":"Mallory","last_name":"M","rbac":{"roleName":"Global Admin",` +
			`"policies":[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]}}}`
		role = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Role","metadata":{"name":"root"},` +
			`"spec":{"roleName":"Root","policies":[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]}}`
	)

	var written []string

	write := func(c *store.Config) error {
		written = append(written, c.FullName())

		return nil
	}

	// The store holds Topology/lab, and only the topologies created are
	// written: a refused request writes nothing.
	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Create(gomock.Any()).DoAndReturn(write).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(write).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.FullName() != "Topology/lab" {
			return store.ErrNotExist
		}

		return nil
	}).AnyTimes()

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	var (
		topologies = configsRoleOn([]string{"Topology/*"}, "create", "update")
		everything = configsRole("create", "update")
	)

	for _, tt := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		vars    map[string]string
		body    string
		role    rbac.Role
		status  int
	}{
		{"POST /configs of a User", CreateConfig, http.MethodPost, nil, user, topologies, http.StatusForbidden},
		{"POST /configs of a Role", CreateConfig, http.MethodPost, nil, role, topologies, http.StatusForbidden},
		{
			"PUT /configs/topology/lab of a User", UpdateConfig, http.MethodPut,
			map[string]string{"kind": "topology", "name": "lab"}, user, topologies, http.StatusForbidden,
		},
		{
			"POST /workflow/configs/main of a User", WorkflowUpsertConfig, http.MethodPost,
			map[string]string{"branch": "main"}, user, topologies, http.StatusForbidden,
		},
		{"POST /configs of a Topology", CreateConfig, http.MethodPost, nil, topologyConfigJSON("plant"), topologies, http.StatusCreated},
		{
			"POST /configs of a Topology, with every config", CreateConfig, http.MethodPost, nil,
			topologyConfigJSON("site"), everything, http.StatusCreated,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/configs", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", mimeJSON)

			if tt.vars != nil {
				req = mux.SetURLVars(req, tt.vars)
			}

			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, tt.role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(tt.handler).ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != tt.status {
				t.Errorf("%s status = %d, want %d: %s", tt.name, rec.Code, tt.status, rec.Body)
			}
		})
	}

	if want := []string{"Topology/plant", "Topology/site"}; !slices.Equal(written, want) {
		t.Errorf("configs written = %q, want %q", written, want)
	}
}
