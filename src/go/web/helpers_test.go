package web

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// testRole returns a role with one policy.
func testRole(resource, verb string, names ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{resource},
					ResourceNames: names,
					Verbs:         []string{verb},
				},
			},
		},
	}
}

// adminRole may do anything, as Global Admin may.
func adminRole() rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
		Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"},
	}}}}
}

// combinedRole returns a role with the policies of every role given.
func combinedRole(roles ...rbac.Role) rbac.Role {
	combined := rbac.Role{Spec: &v1.RoleSpec{}}

	for _, role := range roles {
		combined.Spec.Policies = append(combined.Spec.Policies, role.Spec.Policies...)
	}

	return combined
}

// testStore is an in-memory store of configs. Any store.Store method it does
// not define panics through the nil embedded interface.
type testStore struct {
	store.Store

	mu      sync.Mutex
	configs []store.Config
}

func (s *testStore) find(c *store.Config) int {
	for i, stored := range s.configs {
		if strings.EqualFold(stored.Kind, c.Kind) && stored.Metadata.Name == c.Metadata.Name {
			return i
		}
	}

	return -1
}

func (s *testStore) List(kinds ...string) (store.Configs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var configs store.Configs

	for _, c := range s.configs {
		if len(kinds) == 0 || slices.ContainsFunc(kinds, func(k string) bool { return strings.EqualFold(k, c.Kind) }) {
			configs = append(configs, c)
		}
	}

	return configs, nil
}

func (s *testStore) Get(c *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.find(c)
	if i < 0 {
		return fmt.Errorf("%s/%s: %w", c.Kind, c.Metadata.Name, store.ErrNotExist)
	}

	*c = s.configs[i]

	return nil
}

func (s *testStore) Create(c *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.find(c) >= 0 {
		return fmt.Errorf("%s/%s: %w", c.Kind, c.Metadata.Name, store.ErrExist)
	}

	s.configs = append(s.configs, *c)

	return nil
}

func (s *testStore) Update(c *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.find(c)
	if i < 0 {
		return fmt.Errorf("%s/%s: %w", c.Kind, c.Metadata.Name, store.ErrNotExist)
	}

	s.configs[i] = *c

	return nil
}

func (s *testStore) Delete(c *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.find(c)
	if i < 0 {
		return fmt.Errorf("%s/%s: %w", c.Kind, c.Metadata.Name, store.ErrNotExist)
	}

	s.configs = slices.Delete(s.configs, i, i+1)

	return nil
}

// useTestStore makes the store hold the configs, as well as any the test has
// stored already, for the rest of the test.
func useTestStore(t *testing.T, configs ...store.Config) {
	t.Helper()

	s, ok := store.DefaultStore.(*testStore)
	if !ok {
		s = new(testStore)

		original := store.DefaultStore
		t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

		store.DefaultStore = s //nolint:reassign // install test double
	}

	s.mu.Lock()
	s.configs = append(s.configs, configs...)
	s.mu.Unlock()
}

// testExperiment is the stored config of test-experiment, running since
// started unless that is empty, with the given topology nodes.
func testExperiment(t *testing.T, started string, nodes ...map[string]any) store.Config {
	t.Helper()

	return namedExperiment(t, "test-experiment", started, nodes...)
}

// namedExperiment is the stored config of the experiment name, running since
// started unless that is empty, with the given topology nodes.
func namedExperiment(t *testing.T, name, started string, nodes ...map[string]any) store.Config {
	t.Helper()

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"experimentName": name,
			"baseDir":        t.TempDir(),
			"topology":       map[string]any{"nodes": nodes},
		},
	}

	if started != "" {
		c.Status = map[string]any{"startTime": started}
	}

	return c
}

// useTestExperiment stores test-experiment, running, with one VM, test-vm,
// whose two interfaces are on VLANs EXP_1 and EXP_2.
func useTestExperiment(t *testing.T) {
	t.Helper()

	useTestStore(t, testExperiment(t, "2024-01-01T00:00:00Z", map[string]any{
		"type": "VirtualMachine",
		"general": map[string]any{
			"hostname":    "test-vm",
			"do_not_boot": false,
			"snapshot":    false,
		},
		"hardware": map[string]any{
			"vcpus":   2,
			"memory":  512,
			"os_type": "linux",
			"drives":  []map[string]any{{"image": "test.qc2", "inject_partition": 1}},
		},
		"network": map[string]any{
			"interfaces": []map[string]any{
				{"name": "IF0", "vlan": "EXP_1"},
				{"name": "IF1", "vlan": "EXP_2"},
			},
		},
	}))
}

// testFiles is the cluster file listing of test-experiment. Other
// file.ClusterFiles methods panic.
type testFiles struct {
	file.ClusterFiles

	files file.Files
}

func (f *testFiles) GetExperimentFiles(string, string) (file.Files, error) {
	return f.files, nil
}

// useTestFiles makes the cluster list the experiment files at paths for the
// rest of the test.
func useTestFiles(t *testing.T, paths ...string) *testFiles {
	t.Helper()

	fake := new(testFiles)
	for _, p := range paths {
		fake.files = append(fake.files, file.File{Name: filepath.Base(p), Path: p, Categories: []string{}})
	}

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = fake //nolint:reassign // test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double

	return fake
}

// useExperimentFilesDir makes a temp dir the phenix base, with contents in test-experiment's files directory.
func useExperimentFilesDir(t *testing.T, files map[string]string) {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // point at a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	for p, contents := range files {
		local := filepath.Join(base, "images", "test-experiment", "files", p)

		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			t.Fatalf("making the folder of experiment file %s: %v", p, err)
		}

		if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing experiment file %s: %v", p, err)
		}
	}
}

// testPublic is the UI's files for serving tests.
func testPublic() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<!DOCTYPE html><title>phenix</title>")},
		"assets/index-abc123.js": {Data: []byte(strings.Repeat("console.log('phenix');\n", 500))},
		"assets/logo-abc123.png": {Data: []byte("\x89PNG not really")},
		"docs/index.html":        {Data: []byte(strings.Repeat("<p>phenix docs</p>\n", 200))},
		"novnc/app/readme.txt":   {Data: []byte("no index here")},
	}
}

// roleNames numbers the roles serveAs stores: dev authentication keeps a role
// it has looked up, by name, for every later request of the process.
var roleNames atomic.Int64 //nolint:gochecknoglobals // unique across tests

// serveAs serves the routes over testPublic, taking every API request as user with role (dev auth);
// it adds the role to the test's store, installing one if needed.
//
//nolint:unparam // tests can serve as any user
func serveAs(t *testing.T, user string, role rbac.Role) *httptest.Server {
	t.Helper()

	return serveRouter(t, testPublic(), devKey(t, user, role))
}

// devKey is the JWT signing key of dev authentication as user with role,
// which it adds to the test's store as serveAs does.
func devKey(t *testing.T, user string, role rbac.Role) string {
	t.Helper()

	name := fmt.Sprintf("test-role-%d", roleNames.Add(1))

	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"roleName": name, "policies": rolePolicies(role)},
	})

	return "dev|" + user + "|" + name
}

// rolePolicies are the role's policies as a stored config holds them.
func rolePolicies(role rbac.Role) []map[string]any {
	policies := make([]map[string]any, 0, len(role.Spec.Policies))
	for _, p := range role.Spec.Policies {
		policies = append(policies, map[string]any{
			"resources": p.Resources, "resourceNames": p.ResourceNames, "verbs": p.Verbs,
		})
	}

	return policies
}

// serveRouter serves newRouter's routes over the UI's public files, the API
// authenticating requests as the JWT signing key has phenix ui do.
func serveRouter(t *testing.T, public fs.FS, jwtKey string) *httptest.Server {
	t.Helper()

	opts := o
	o.jwtKey = jwtKey

	router := newRouter(http.FS(public))

	o = opts

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server
}

// do sends a request to the server and returns the response, which is closed
// when the test ends.
func do(t *testing.T, method, url, body string, header ...string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequestWithContext(%s, %s) error = %v", method, url, err)
	}

	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, url, err)
	}

	t.Cleanup(func() { resp.Body.Close() })

	return resp
}

func get(t *testing.T, h http.Handler, target, acceptEncoding string) *http.Response {
	t.Helper()

	return getIfNoneMatch(t, h, target, acceptEncoding, "")
}

func getIfNoneMatch(t *testing.T, h http.Handler, target, acceptEncoding, etag string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}

	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Result()
}

// errorMessage returns the message of body, a JSON Error body, failing the
// test unless it is one with the code of status.
func errorMessage(t *testing.T, status int, body string) string {
	t.Helper()

	var e struct {
		Message string        `json:"message"`
		Code    weberror.Code `json:"code"`
	}

	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("error body %q is not JSON: %v", body, err)
	}

	if want := weberror.CodeFor(status); e.Code != want {
		t.Errorf("error body %q code = %q, want %q", body, e.Code, want)
	}

	return e.Message
}

// readBody reads the response's body, decompressed if it is gzipped.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	r := resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader(response body) error = %v", err)
		}

		r = zr
	}

	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the response body: %v", err)
	}

	return string(body)
}
