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
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// TestCreateConfigWhenEtcdIsOutOfSpace asserts a config etcd refused to store
// for lack of space is answered with 507 and says so plainly.
func TestCreateConfigWhenEtcdIsOutOfSpace(t *testing.T) {
	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Create(gomock.Any()).Return(fmt.Errorf(
		"writing config JSON to Etcd: %w: %w",
		store.ErrNoSpace, errors.New("etcdserver: mvcc: database space exceeded"),
	))

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	body := `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"full"},` +
		`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host"},` +
		`"hardware":{"os_type":"linux","drives":[{"image":"host.qc2"}]}}]}}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/configs", strings.NewReader(body))
	req.Header.Set("Content-Type", mimeJSON)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, configsRole("create"))
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CreateConfig).ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusInsufficientStorage, rec.Body.String())
	}

	var refused weberror.WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if refused.Message != store.ErrNoSpace.Error() {
		t.Fatalf("message = %q, want %q", refused.Message, store.ErrNoSpace.Error())
	}
}

// TestConfigYAMLKeepsStrings gets a config as YAML and downloads it, alone
// and with another in a zip, and asserts each file loads as the config
// although its strings start with a line break or a tab.
func TestConfigYAMLKeepsStrings(t *testing.T) { //nolint:paralleltest // replaces the config store
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

	loads := func(path string, data []byte, name string) {
		t.Helper()

		var loaded store.Config
		if err := yaml.Unmarshal(data, &loaded); err != nil {
			t.Fatalf("%s does not load: %v\n%s", path, err, data)
		}

		got, _ := json.Marshal(loaded)
		want, _ := json.Marshal(stored(name))

		if string(got) != string(want) {
			t.Fatalf("%s loads as %s, want %s\n%s", path, got, want, data)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/configs/scenario/exact?noupgrade=1", nil)
	req.Header.Set("Accept", "application/x-yaml")
	loads("GET /configs", serve(GetConfig, mux.SetURLVars(req, map[string]string{"kind": "scenario", "name": "exact"})), "exact")

	download := func(names string) []byte {
		return serve(DownloadConfigs, httptest.NewRequest(http.MethodPost, "/api/v1/configs/download", strings.NewReader(names)))
	}

	loads("the download", download(`["Scenario/exact"]`), "exact")

	body := download(`["Scenario/exact","Scenario/other"]`)

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("reading the zip: %v", err)
	}

	if len(archive.File) != 2 {
		t.Fatalf("zip holds %d files, want 2", len(archive.File))
	}

	for _, file := range archive.File {
		f, err := file.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", file.Name, err)
		}

		data, err := io.ReadAll(f)
		_ = f.Close()

		if err != nil {
			t.Fatalf("reading %s: %v", file.Name, err)
		}

		loads(file.Name, data, strings.TrimSuffix(strings.TrimPrefix(file.Name, "Scenario-"), ".yml"))
	}
}
