package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
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

	c, err := store.DecodeConfigJSON([]byte(revisionTestTopology))
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

// PUT /configs/{kind}/{name} answers 400 to a body or uploaded file that does
// not decode, and leaves the stored config as it was.
func TestUpdateConfigMalformedBody(t *testing.T) {
	jsonUpload, jsonUploadType := formUpload(t, "fileupload", "topology.json", `{"kind":`)
	yamlUpload, yamlUploadType := formUpload(t, "fileupload", "topology.yml", "kind: [")

	tests := map[string]struct {
		contentType string
		body        []byte
	}{
		"JSON":        {mimeJSON, []byte(`{"kind":`)},
		"not JSON":    {mimeJSON, []byte("kind: Topology")},
		"YAML":        {mimeYAML, []byte("kind: [")},
		"JSON upload": {jsonUploadType, jsonUpload},
		"YAML upload": {yamlUploadType, yamlUpload},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rev := useRevisionTestStore(t)

			req := validationTestRequest(t, http.MethodPut, "/api/v1/configs/topology/demo", tc.contentType, tc.body,
				map[string]string{"kind": "topology", "name": "demo"})

			if status, _ := webErrorStatus(t, UpdateConfig(httptest.NewRecorder(), req)); status != http.StatusBadRequest {
				t.Errorf("UpdateConfig(PUT topology/demo, %s body) = %d, want %d", name, status, http.StatusBadRequest)
			}

			got, _ := store.NewConfig("topology/demo")
			if err := store.Get(got); err != nil {
				t.Fatalf("store.Get(topology/demo) = %v", err)
			}

			if got.Metadata.Revision != rev {
				t.Errorf("topology/demo revision after the PUT = %d, want %d", got.Metadata.Revision, rev)
			}
		})
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

// TestConfigYAMLKeepsStrings asserts a config got or downloaded as YAML loads
// back as the config, though its strings start with a line break or a tab.
func TestConfigYAMLKeepsStrings(t *testing.T) {
	stored := func(name string) store.Config {
		return store.Config{
			Version: "phenix.sandia.gov/v2",
			Kind:    "Scenario",
			Metadata: store.ConfigMetadata{
				Name: name, Created: "2026-09-29T10:00:00Z", Updated: "2026-09-29T10:00:00Z",
				Annotations: store.Annotations{"note": "\n\tnote"},
			},
			Spec: map[string]any{
				"description": "\nfirst",
				"apps": []any{map[string]any{
					"name": "app", "metadata": map[string]any{"script": "\tfirst\nsecond", "labels": []any{"\n"}},
				}},
			},
			Status: nil,
		}
	}

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = stored(c.Metadata.Name)

		return nil
	}).AnyTimes()

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"configs"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"get"}},
	}}}

	serve := func(handler func(http.ResponseWriter, *http.Request) error, req *http.Request) []byte {
		t.Helper()

		ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

		rec := httptest.NewRecorder()
		weberror.ErrorHandler(handler).ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", req.Method, req.URL, rec.Code, rec.Body.String())
		}

		return rec.Body.Bytes()
	}

	// loadProblem reports how data, loaded as YAML, differs from the stored config name.
	loadProblem := func(data []byte, name string) error {
		var loaded store.Config
		if err := yaml.Unmarshal(data, &loaded); err != nil {
			return fmt.Errorf("does not load: %w\n%s", err, data)
		}

		if diff := cmp.Diff(stored(name), loaded); diff != "" {
			return fmt.Errorf("loads mismatch (-want +got):\n%s\n%s", diff, data)
		}

		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs/scenario/exact?noupgrade=1", nil)
	req.Header.Set("Accept", "application/x-yaml")

	got := serve(GetConfig, mux.SetURLVars(req, map[string]string{"kind": "scenario", "name": "exact"}))
	if err := loadProblem(got, "exact"); err != nil {
		t.Errorf("GET /configs/scenario/exact: %v", err)
	}

	download := func(names string) []byte {
		return serve(DownloadConfigs, httptest.NewRequest(http.MethodPost, "/api/v1/configs/download", strings.NewReader(names)))
	}

	if err := loadProblem(download(`["Scenario/exact"]`), "exact"); err != nil {
		t.Errorf("POST /configs/download of Scenario/exact: %v", err)
	}

	body := download(`["Scenario/exact","Scenario/other"]`)

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("reading the zip: %v", err)
	}

	if len(archive.File) != 2 {
		t.Errorf("POST /configs/download of two configs: zip holds %d files, want 2", len(archive.File))
	}

	for _, file := range archive.File {
		f, err := file.Open()
		if err != nil {
			t.Errorf("opening %s: %v", file.Name, err)

			continue
		}

		data, err := io.ReadAll(f)
		_ = f.Close()

		if err != nil {
			t.Errorf("reading %s: %v", file.Name, err)

			continue
		}

		if err := loadProblem(data, strings.TrimSuffix(strings.TrimPrefix(file.Name, "Scenario-"), ".yml")); err != nil {
			t.Errorf("POST /configs/download of two configs: %s %v", file.Name, err)
		}
	}
}

// TestConfigWritesExpandOnlyBranchName asserts that each REST config write
// fills in ${BRANCH_NAME} and stores every other placeholder as written.
func TestConfigWritesExpandOnlyBranchName(t *testing.T) {
	const secret = "rest-config-secret-value"

	t.Setenv("PHENIX_WEB_TEST_SECRET", secret)

	annotations := store.Annotations{
		"branch":   "${BRANCH_NAME}",
		"braces":   "${PHENIX_WEB_TEST_SECRET}",
		"default":  "${PHENIX_WEB_TEST_SECRET:fallback}",
		"noBraces": "$PHENIX_WEB_TEST_SECRET",
	}

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "lab", Annotations: annotations},
		Spec: map[string]any{"nodes": []any{map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": "host"},
			"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "host.qc2"}}},
		}}},
	}

	asJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(config) returned error: %v", err)
	}

	asYAML, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("yaml.Marshal(config) returned error: %v", err)
	}

	jsonUpload, jsonUploadType := formUpload(t, "fileupload", "topology.json", string(asJSON))
	yamlUpload, yamlUploadType := formUpload(t, "fileupload", "topology.yml", string(asYAML))

	var (
		create = map[string]string(nil)
		update = map[string]string{"kind": "topology", "name": "lab"}
		branch = map[string]string{"branch": "main"}
	)

	tests := []struct {
		name        string
		handler     func(http.ResponseWriter, *http.Request) error
		method      string
		vars        map[string]string
		contentType string
		body        []byte
		exists      bool
		branch      string
	}{
		{"CreateConfig JSON", CreateConfig, http.MethodPost, create, mimeJSON, asJSON, false, "env-branch"},
		{"CreateConfig YAML", CreateConfig, http.MethodPost, create, mimeYAML, asYAML, false, "env-branch"},
		{"CreateConfig JSON upload", CreateConfig, http.MethodPost, create, jsonUploadType, jsonUpload, false, "env-branch"},
		{"CreateConfig YAML upload", CreateConfig, http.MethodPost, create, yamlUploadType, yamlUpload, false, "env-branch"},
		{"UpdateConfig JSON", UpdateConfig, http.MethodPut, update, mimeJSON, asJSON, true, "env-branch"},
		{"UpdateConfig YAML", UpdateConfig, http.MethodPut, update, mimeYAML, asYAML, true, "env-branch"},
		{"UpdateConfig JSON upload", UpdateConfig, http.MethodPut, update, jsonUploadType, jsonUpload, true, "env-branch"},
		{"UpdateConfig YAML upload", UpdateConfig, http.MethodPut, update, yamlUploadType, yamlUpload, true, "env-branch"},
		{"WorkflowUpsertConfig JSON", WorkflowUpsertConfig, http.MethodPost, branch, mimeJSON, asJSON, false, "main"},
		{"WorkflowUpsertConfig YAML", WorkflowUpsertConfig, http.MethodPost, branch, mimeYAML, asYAML, true, "main"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The config handlers read BRANCH_NAME from the environment. The
			// workflow handler uses its branch.
			t.Setenv("BRANCH_NAME", "env-branch")

			var stored []store.Annotations

			write := func(c *store.Config) error {
				stored = append(stored, c.Metadata.Annotations)

				return nil
			}

			m := installValidationTestStore(t)
			m.EXPECT().Create(gomock.Any()).DoAndReturn(write).AnyTimes()
			m.EXPECT().Update(gomock.Any()).DoAndReturn(write).AnyTimes()
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(*store.Config) error {
				if tt.exists {
					return nil
				}

				return store.ErrNotExist
			}).AnyTimes()
			m.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

			req := validationTestRequest(t, tt.method, "/api/v1/configs", tt.contentType, tt.body, tt.vars)

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(tt.handler).ServeHTTP(rec, req)

			if rec.Code >= http.StatusBadRequest {
				t.Fatalf("%s status = %d: %s", tt.name, rec.Code, rec.Body)
			}

			want := store.Annotations{
				"branch":   tt.branch,
				"braces":   "${PHENIX_WEB_TEST_SECRET}",
				"default":  "${PHENIX_WEB_TEST_SECRET:fallback}",
				"noBraces": "$PHENIX_WEB_TEST_SECRET",
			}

			if len(stored) != 1 {
				t.Fatalf("%s stored %d configs, want 1", tt.name, len(stored))
			}

			if diff := cmp.Diff(want, stored[0]); diff != "" {
				t.Errorf("%s stored annotations mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}
