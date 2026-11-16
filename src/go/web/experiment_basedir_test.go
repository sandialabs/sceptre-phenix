package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// baseDirTestFiles is a cluster that holds no experiment files, for the
// delete hook of an experiment.
type baseDirTestFiles struct {
	file.ClusterFiles
}

func (baseDirTestFiles) DeleteFile(string) error { return nil }

func (baseDirTestFiles) DeleteExistingFiles([]string) error { return nil }

// useBaseDirTest makes a temporary folder the phenix base, with an empty
// store and cluster, and returns the phenix base and a folder outside it.
func useBaseDirTest(t *testing.T) (string, string) {
	t.Helper()

	useTestStore(t)

	base, outside := filepath.Join(t.TempDir(), "phenix"), t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // point at a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	originalFiles := file.DefaultClusterFiles
	file.DefaultClusterFiles = baseDirTestFiles{} //nolint:reassign // test double

	t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

	return base, outside
}

// baseDirExperimentJSON is the config of the stopped experiment lab with the
// base directory dir, "" for the default.
func baseDirExperimentJSON(dir string) string {
	const name = "lab"

	spec := map[string]any{"experimentName": name, "deployMode": "all", "topology": map[string]any{"nodes": []any{}}}
	if dir != "" {
		spec["baseDir"] = dir
	}

	body, _ := json.Marshal(map[string]any{
		"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment",
		"metadata": map[string]any{"name": name}, "spec": spec,
	})

	return string(body)
}

// serveBaseDirTest calls handler with every permission, and returns the
// response.
func serveBaseDirTest(
	handler func(http.ResponseWriter, *http.Request) error,
	method, target string, vars map[string]string, body string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", mimeJSON)

	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, testRole("*", "*", "*", "*/*"))
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(handler).ServeHTTP(rec, req.WithContext(ctx))

	return rec
}

// responseCode is the code of the JSON error body of rec, "" for none.
func responseCode(rec *httptest.ResponseRecorder) weberror.Code {
	var body struct {
		Code weberror.Code `json:"code"`
	}

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	return body.Code
}

// Each API route that creates or changes an experiment config refuses a base
// directory outside <base-dir.phenix>/experiments with a 400 invalid_request.
func TestAPIRefusesABaseDirOutsideTheExperimentsFolder(t *testing.T) { //nolint:paralleltest // replaces package globals
	type route struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		target  string
		vars    map[string]string
		stored  bool // the route updates a stored experiment lab
	}

	routes := []route{
		{name: "POST /configs", handler: CreateConfig, method: http.MethodPost, target: "/api/v1/configs"},
		{
			name: "PUT /configs/experiment/lab", handler: UpdateConfig, method: http.MethodPut,
			target: "/api/v1/configs/experiment/lab", vars: map[string]string{"kind": "experiment", "name": "lab"},
			stored: true,
		},
		{
			name: "POST /workflow/configs/main, create", handler: WorkflowUpsertConfig, method: http.MethodPost,
			target: "/api/v1/workflow/configs/main", vars: map[string]string{"branch": "main"},
		},
		{
			name: "POST /workflow/configs/main, update", handler: WorkflowUpsertConfig, method: http.MethodPost,
			target: "/api/v1/workflow/configs/main", vars: map[string]string{"branch": "main"}, stored: true,
		},
	}

	dirs := []struct {
		name     string
		dir      func(base, outside string) string
		wantCode weberror.Code // "" for success
	}{
		{name: "the default", dir: func(string, string) string { return "" }},
		{name: "inside", dir: func(base, _ string) string { return base + "/experiments/lab" }},
		{name: "a new folder inside", dir: func(base, _ string) string { return base + "/experiments/x/lab" }},
		{name: "outside", dir: func(_, outside string) string { return outside }, wantCode: weberror.CodeInvalidRequest},
		{name: "relative", dir: func(string, string) string { return "lab" }, wantCode: weberror.CodeInvalidRequest},
		{
			name: "a dot-dot out", dir: func(base, _ string) string { return base + "/experiments/../lab" },
			wantCode: weberror.CodeInvalidRequest,
		},
		{
			name: "the experiments folder", dir: func(base, _ string) string { return base + "/experiments" },
			wantCode: weberror.CodeInvalidRequest,
		},
		{name: "the file system root", dir: func(string, string) string { return "/" }, wantCode: weberror.CodeInvalidRequest},
	}

	for _, rt := range routes {
		for _, d := range dirs {
			t.Run(rt.name+", "+d.name, func(t *testing.T) {
				base, outside := useBaseDirTest(t)
				dir := d.dir(base, outside)

				if rt.stored {
					c, err := store.DecodeConfigJSON([]byte(baseDirExperimentJSON(base + "/experiments/lab")))
					if err != nil {
						t.Fatal(err)
					}

					if err := store.Create(c); err != nil {
						t.Fatal(err)
					}
				}

				rec := serveBaseDirTest(rt.handler, rt.method, rt.target, rt.vars, baseDirExperimentJSON(dir))

				if d.wantCode == "" {
					if rec.Code >= http.StatusBadRequest {
						t.Errorf("%s with baseDir %q status = %d, want success: %s", rt.name, dir, rec.Code, rec.Body)
					}

					return
				}

				// the message names the field, so the refusal is for the base directory
				if rec.Code != http.StatusBadRequest || responseCode(rec) != d.wantCode ||
					!strings.Contains(rec.Body.String(), "spec.baseDir") {
					t.Errorf("%s with baseDir %q = %d %q, want 400 %q: %s",
						rt.name, dir, rec.Code, responseCode(rec), d.wantCode, rec.Body)
				}

				c, _ := store.NewConfig("experiment/lab")
				if err := store.Get(c); err == nil && c.Spec["baseDir"] == dir {
					t.Errorf("%s with baseDir %q stored the refused base directory", rt.name, dir)
				}
			})
		}
	}
}

// POST /experiments refuses the names phenix or minimega reserve, also . and
// .., whose default base directory is the phenix base or the experiments
// folder itself.
func TestCreateExperimentRefusesReservedNames(t *testing.T) { //nolint:paralleltest // replaces package globals
	for _, name := range []string{"all", "minimega", "__PHENIX__", ".", ".."} {
		t.Run(name, func(t *testing.T) {
			useBaseDirTest(t)

			body := `{"name":"` + name + `","topology":"topo"}`

			rec := serveBaseDirTest(CreateExperiment, http.MethodPost, "/api/v1/experiments", nil, body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot use") {
				t.Errorf("POST /experiments name %q = %d, want 400 that names the reserved name: %s", name, rec.Code, rec.Body)
			}
		})
	}
}

// A delete through the API deletes the experiment, but leaves its base
// directory when it is outside the experiments folder, and removes it when it
// is inside.
func TestAPIDeletesLeaveABaseDirOutsideTheExperimentsFolder(t *testing.T) { //nolint:paralleltest // replaces package globals
	routes := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		target  string
		vars    map[string]string
		body    string
	}{
		{
			name: "DELETE /experiments/lab", handler: DeleteExperiment, method: http.MethodDelete,
			target: "/api/v1/experiments/lab", vars: map[string]string{"name": "lab"},
		},
		{
			name: "DELETE /configs/experiment/lab", handler: DeleteConfig, method: http.MethodDelete,
			target: "/api/v1/configs/experiment/lab", vars: map[string]string{"kind": "experiment", "name": "lab"},
		},
		{
			name: "POST /configs/delete", handler: DeleteConfigs, method: http.MethodPost,
			target: "/api/v1/configs/delete", body: `{"kind":"experiment"}`,
		},
	}

	for _, rt := range routes {
		for _, inside := range []bool{false, true} {
			t.Run(rt.name+map[bool]string{false: ", outside", true: ", inside"}[inside], func(t *testing.T) {
				base, outside := useBaseDirTest(t)

				dir := outside + "/lab"
				if inside {
					dir = base + "/experiments/lab"
				}

				if err := os.MkdirAll(dir, 0o750); err != nil {
					t.Fatal(err)
				}

				c, err := store.DecodeConfigJSON([]byte(baseDirExperimentJSON(dir)))
				if err != nil {
					t.Fatal(err)
				}

				if err := store.Create(c); err != nil {
					t.Fatal(err)
				}

				rec := serveBaseDirTest(rt.handler, rt.method, rt.target, rt.vars, rt.body)
				if rec.Code >= http.StatusBadRequest {
					t.Fatalf("%s status = %d, want success: %s", rt.name, rec.Code, rec.Body)
				}

				stored, _ := store.NewConfig("experiment/lab")
				if err := store.Get(stored); !errors.Is(err, store.ErrNotExist) {
					t.Errorf("store.Get(experiment/lab) after %s error = %v, want store.ErrNotExist", rt.name, err)
				}

				_, err = os.Stat(dir)
				if gone := errors.Is(err, fs.ErrNotExist); gone != inside {
					t.Errorf("%s removed base directory %s = %t, want %t", rt.name, dir, gone, inside)
				}
			})
		}
	}
}

// The Builder publish writes configs as the other API routes do: an
// experiment config whose base directory is outside the experiments folder is
// refused.
func TestBuilderPublishOpsRefuseABaseDirOutsideTheExperimentsFolder(t *testing.T) { //nolint:paralleltest // replaces package globals
	base, outside := useBaseDirTest(t)
	ops := newBuilderPublishOps()

	stored, err := store.DecodeConfigJSON([]byte(baseDirExperimentJSON(base + "/experiments/lab")))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Create(stored); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		write func(*store.Config) error
	}{
		{name: "createConfig", write: func(c *store.Config) error {
			c.Metadata.Name = "other"
			_, err := ops.createConfig(c)

			return err
		}},
		{name: "updateConfig", write: func(c *store.Config) error { return ops.updateConfig("Experiment/lab", c) }},
	} {
		c, err := store.DecodeConfigJSON([]byte(baseDirExperimentJSON(outside)))
		if err != nil {
			t.Fatal(err)
		}

		if err := tc.write(c); !errors.Is(err, experiment.ErrBaseDirOutside) {
			t.Errorf("builder %s(baseDir outside) error = %v, want %v", tc.name, err, experiment.ErrBaseDirOutside)
		}
	}
}

// An API update of an experiment whose stored base directory is outside the
// experiments folder works when it keeps that base directory, and is refused
// when it changes it to another folder outside.
func TestAPIUpdateKeepsAStoredBaseDirOutside(t *testing.T) { //nolint:paralleltest // replaces package globals
	routes := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		target  string
		vars    map[string]string
	}{
		{
			name: "PUT /configs/experiment/lab", handler: UpdateConfig, method: http.MethodPut,
			target: "/api/v1/configs/experiment/lab", vars: map[string]string{"kind": "experiment", "name": "lab"},
		},
		{
			name: "POST /workflow/configs/main", handler: WorkflowUpsertConfig, method: http.MethodPost,
			target: "/api/v1/workflow/configs/main", vars: map[string]string{"branch": "main"},
		},
	}

	for _, rt := range routes {
		for _, changed := range []bool{false, true} {
			t.Run(rt.name+map[bool]string{false: ", unchanged", true: ", changed"}[changed], func(t *testing.T) {
				_, outside := useBaseDirTest(t)

				c, err := store.DecodeConfigJSON([]byte(baseDirExperimentJSON(outside + "/lab")))
				if err != nil {
					t.Fatal(err)
				}

				if err := store.Create(c); err != nil {
					t.Fatal(err)
				}

				dir := outside + "/lab"
				if changed {
					dir = outside + "/other"
				}

				rec := serveBaseDirTest(rt.handler, rt.method, rt.target, rt.vars, baseDirExperimentJSON(dir))

				if changed && (rec.Code != http.StatusBadRequest || responseCode(rec) != weberror.CodeInvalidRequest) {
					t.Errorf("%s changing an outside base directory = %d %q, want 400: %s",
						rt.name, rec.Code, responseCode(rec), rec.Body)
				}

				if !changed && rec.Code >= http.StatusBadRequest {
					t.Errorf("%s keeping an outside base directory = %d, want success: %s", rt.name, rec.Code, rec.Body)
				}
			})
		}
	}
}
