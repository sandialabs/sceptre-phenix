package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/util/pubsub"
	"phenix/web/cache"
)

// handlerErrorCase is a request a handler answers with an error status.
type handlerErrorCase struct {
	handler func(http.ResponseWriter, *http.Request) error
	method  string
	target  string
	vars    map[string]string
	body    string
	broken  bool     // the body cannot be read
	header  []string // pairs set on the request
	// upload, when set, makes the body a multipart form holding a file of
	// that name
	upload string
	// setup prepares the fixture after useTestExperiment
	setup func(t *testing.T)
	want  int
}

// lockExperiment holds the creation lock of the experiment name until the
// test ends, as another action on it would.
func lockExperiment(name string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		if err := cache.LockExperimentForCreation(name); err != nil {
			t.Fatalf("cache.LockExperimentForCreation(%s) error = %v", name, err)
		}

		t.Cleanup(func() { cache.UnlockExperiment(name) })
	}
}

func handlerErrorCases() map[string]handlerErrorCase {
	exp := map[string]string{"name": "test-experiment"}
	vm := map[string]string{"exp": "test-experiment", "name": "test-vm"}
	stopped := func(t *testing.T) {
		t.Helper()

		useTestStore(t, namedExperiment(t, "stopped-experiment", ""))
	}

	cases := map[string]handlerErrorCase{
		"delete locked experiment": {
			handler: DeleteExperiment, method: http.MethodDelete, vars: exp, setup: lockExperiment("test-experiment"),
			want: http.StatusConflict,
		},
		"delete unknown experiment": {
			handler: DeleteExperiment, method: http.MethodDelete, vars: map[string]string{"name": "none"},
			want: http.StatusNotFound,
		},
		"start locked experiment": {
			handler: StartExperiment, method: http.MethodPost, vars: exp, setup: lockExperiment("test-experiment"),
			want: http.StatusConflict,
		},
		"create from unreadable body": {
			handler: CreateExperiment, method: http.MethodPost, broken: true, want: http.StatusInternalServerError,
		},
		"create with the branch twice": {
			handler: CreateExperiment, method: http.MethodPost,
			body: `{"name":"new","workflow_branch":"b","annotations":{"phenix.workflow/branch":"a"}}`,
			want: http.StatusBadRequest,
		},
		"create of locked name": {
			handler: CreateExperiment, method: http.MethodPost, body: `{"name":"new","topology":"none"}`,
			setup: lockExperiment("new"), want: http.StatusConflict,
		},
		"create from unknown topology": {
			handler: CreateExperiment, method: http.MethodPost,
			body: `{"name":"new","topology":"none","deploy_mode":"sideways"}`, want: http.StatusBadRequest,
		},
		"update from unreadable body": {
			handler: UpdateExperiment, method: http.MethodPatch, vars: map[string]string{"name": "stopped-experiment"},
			broken: true, setup: stopped, want: http.StatusInternalServerError,
		},
		"update not saved": {
			handler: UpdateExperiment, method: http.MethodPatch, vars: map[string]string{"name": "stopped-experiment"},
			body: `{"EXP":101}`, want: http.StatusInternalServerError,
			setup: func(t *testing.T) {
				t.Helper()

				stopped(t)
				failTestStore(t, "update", "Experiment", errStoreDown)
			},
		},
		"apps of unknown experiment": {
			handler: GetExperimentApps, method: http.MethodGet, vars: map[string]string{"name": "none"},
			want: http.StatusNotFound,
		},
		"VMs of unknown experiment": {
			handler: GetVMs, method: http.MethodGet, vars: map[string]string{"exp": "none"}, want: http.StatusNotFound,
		},
		"VMs page that is not a number": {
			handler: GetVMs, method: http.MethodGet, target: "/?pageNum=x&perPage=5", vars: vm, want: http.StatusBadRequest,
		},
		"VM of unknown experiment": {
			handler: GetVM, method: http.MethodGet, vars: map[string]string{"exp": "none", "name": "test-vm"},
			want: http.StatusNotFound,
		},
		"VM unknown": {
			handler: GetVM, method: http.MethodGet, vars: map[string]string{"exp": "test-experiment", "name": "none"},
			want: http.StatusNotFound,
		},
		"screenshot minimega cannot take": {
			handler: GetScreenshot, method: http.MethodGet, vars: map[string]string{"exp": "shot-error", "name": "test-vm"},
			setup: func(t *testing.T) {
				t.Helper()

				useTestMM(t, &testMM{screenshotErr: errors.New("no screen")})
			},
			want: http.StatusBadRequest,
		},
		"screenshot of a size that is not one": {
			handler: GetScreenshot, method: http.MethodGet, target: "/?size=huge", vars: vm, want: http.StatusBadRequest,
		},
		"redeploy from unreadable body": {
			handler: RedeployVM, method: http.MethodPost, vars: vm, broken: true, want: http.StatusInternalServerError,
		},
		"redeploy in unknown experiment": {
			handler: RedeployVM, method: http.MethodPost, vars: map[string]string{"exp": "none", "name": "test-vm"},
			body: `{}`, want: http.StatusNotFound,
		},
		"redeploy of unknown VM": {
			handler: RedeployVM, method: http.MethodPost, vars: map[string]string{"exp": "test-experiment", "name": "none"},
			body: `{}`, want: http.StatusNotFound,
		},
		"subnet capture from unreadable body": {
			handler: StartCaptureSubnet, method: http.MethodPost, vars: vm, broken: true, want: http.StatusInternalServerError,
		},
		"subnet capture from bad JSON": {
			handler: StartCaptureSubnet, method: http.MethodPost, vars: vm, body: `{`, want: http.StatusBadRequest,
		},
		"experiment capture from unreadable body": {
			handler: StartExperimentCapture, method: http.MethodPost, vars: vm, broken: true,
			want: http.StatusInternalServerError,
		},
		"subnet capture stop from unreadable body": {
			handler: StopCaptureSubnet, method: http.MethodPost, vars: vm, broken: true, want: http.StatusInternalServerError,
		},
		"subnet capture stop from bad JSON": {
			handler: StopCaptureSubnet, method: http.MethodPost, vars: vm, body: `{`, want: http.StatusBadRequest,
		},
	}

	maps.Copy(cases, configErrorCases())

	return cases
}

func configErrorCases() map[string]handlerErrorCase {
	topology := map[string]string{"kind": "topology", "name": "demo"}
	experiment := map[string]string{"kind": "experiment", "name": "test-experiment"}
	yaml := []string{"Content-Type", mimeYAML}
	plain := []string{"Content-Type", "text/plain"}
	multipart := []string{"Content-Type", "multipart/form-data; boundary=x"}

	return map[string]handlerErrorCase{
		"configs list fails": {
			handler: GetConfigs, method: http.MethodGet, want: http.StatusInternalServerError,
			setup: func(t *testing.T) { t.Helper(); failTestStore(t, "list", "Topology", errStoreDown) },
		},
		"config in a type it cannot send": {
			handler: GetConfig, method: http.MethodGet, vars: experiment, header: []string{"Accept", "text/html"},
			want: http.StatusNotAcceptable,
		},
		"download from unreadable body": {
			handler: DownloadConfigs, method: http.MethodPost, broken: true, want: http.StatusInternalServerError,
		},
		"download from bad JSON": {
			handler: DownloadConfigs, method: http.MethodPost, body: `{`, want: http.StatusBadRequest,
		},
		"download of unknown config": {
			handler: DownloadConfigs, method: http.MethodPost, body: `["topology/none"]`, want: http.StatusBadRequest,
		},
		"download of configs, one unknown": {
			handler: DownloadConfigs, method: http.MethodPost, body: `["experiment/test-experiment","topology/none"]`,
			want: http.StatusBadRequest,
		},
		"create from unreadable JSON": {
			handler: CreateConfig, method: http.MethodPost, broken: true, want: http.StatusInternalServerError,
		},
		"create from unreadable YAML": {
			handler: CreateConfig, method: http.MethodPost, broken: true, header: yaml, want: http.StatusInternalServerError,
		},
		"create from upload without file": {
			handler: CreateConfig, method: http.MethodPost, header: multipart, want: http.StatusBadRequest,
		},
		"create from upload of unknown type": {
			handler: CreateConfig, method: http.MethodPost, upload: "topology.txt", want: http.StatusBadRequest,
		},
		"create from unknown content type": {
			handler: CreateConfig, method: http.MethodPost, header: plain, body: "{}", want: http.StatusUnsupportedMediaType,
		},
		"update from unreadable body": {
			handler: UpdateConfig, method: http.MethodPut, vars: topology, broken: true, want: http.StatusInternalServerError,
		},
		"update from upload without file": {
			handler: UpdateConfig, method: http.MethodPut, vars: topology, header: multipart, want: http.StatusBadRequest,
		},
		"update from upload of unknown type": {
			handler: UpdateConfig, method: http.MethodPut, vars: topology, upload: "topology.txt", want: http.StatusBadRequest,
		},
		"update from unknown content type": {
			handler: UpdateConfig, method: http.MethodPut, vars: topology, header: plain, body: "{}",
			want: http.StatusUnsupportedMediaType,
		},
	}
}

// The handlers answer a request they cannot read, one that names what the
// store lacks, or one minimega or the store fails, with an error status and
// the JSON Error body.
//
//nolint:paralleltest // replaces the store and minimega
func TestHandlerErrors(t *testing.T) {
	for name, tc := range handlerErrorCases() {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useTestMM(t, &testMM{})

			if tc.setup != nil {
				tc.setup(t)
			}

			target := tc.target
			if target == "" {
				target = "/"
			}

			body := tc.body

			var formType string
			if tc.upload != "" {
				form, contentType := formUpload(t, "fileupload", tc.upload, "{}")
				body, formType = string(form), contentType
			}

			req := roleRequest(tc.method, target, body, tc.vars, adminRole())
			if tc.broken {
				req.Body = io.NopCloser(failingReader{})
			}

			if formType != "" {
				req.Header.Set("Content-Type", formType)
			}

			for i := 0; i+1 < len(tc.header); i += 2 {
				req.Header.Set(tc.header[i], tc.header[i+1])
			}

			got, rec := serveHandler(tc.handler, req)
			if got.Status != tc.want {
				t.Fatalf("%s = %d, want %d: %s", name, got.Status, tc.want, rec.Body)
			}

			errorMessage(t, tc.want, rec.Body.String())
		})
	}
}

// GET /experiments/{exp}/vms/{name}/screenshot.png sends minimega's PNG, or
// with base64 true, the PNG as a data URL.
//
//nolint:paralleltest // replaces minimega
func TestGetScreenshot(t *testing.T) {
	png := []byte("\x89PNG not really")

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	tests := map[string]struct {
		target, contentType, body string
	}{
		"PNG":          {"/", "image/png", string(png)},
		"base64":       {"/?base64=1&size=300", "text/plain; charset=utf-8", dataURL},
		"base64 true":  {"/?base64=true", "text/plain; charset=utf-8", dataURL},
		"base64 false": {"/?base64=false", "image/png", string(png)},
		"base64 empty": {"/?base64=", "image/png", string(png)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestMM(t, &testMM{screenshot: png})

			// a name of its own, as screenshots are cached by experiment and VM
			vars := map[string]string{"exp": "shot-" + name, "name": "vm"}

			got, rec := serveHandler(GetScreenshot, roleRequest(http.MethodGet, tc.target, "", vars, adminRole()))
			if got.Status != http.StatusOK {
				t.Fatalf("GET screenshot = %+v, want 200: %s", got, rec.Body)
			}

			if ct := rec.Header().Get("Content-Type"); ct != tc.contentType {
				t.Errorf("GET screenshot Content-Type = %q, want %q", ct, tc.contentType)
			}

			if rec.Body.String() != tc.body {
				t.Errorf("GET screenshot body = %q, want %q", rec.Body.String(), tc.body)
			}
		})
	}

	t.Run("base64 not a boolean", func(t *testing.T) {
		useTestMM(t, &testMM{screenshot: png})

		vars := map[string]string{"exp": "shot-not-boolean", "name": "vm"}
		want := handlerAnswer{Status: http.StatusBadRequest, Message: `base64 must be true or false, not "yes"`}

		got, _ := serveHandler(GetScreenshot, roleRequest(http.MethodGet, "/?base64=yes", "", vars, adminRole()))
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET screenshot?base64=yes mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		useTestMM(t, &testMM{screenshot: png})

		vars := map[string]string{"exp": "shot-forbidden", "name": "vm"}
		role := testRole("vms/screenshot", "get", "shot-forbidden/other")

		if got, _ := serveHandler(GetScreenshot, roleRequest(http.MethodGet, "/", "", vars, role)); got.Status != http.StatusForbidden {
			t.Errorf("GET screenshot of another VM's grant = %+v, want 403", got)
		}
	})
}

// GET /experiments/{name}/apps lists each app of the scenario with whether its
// running stage runs, for a role that may get them.
//
//nolint:paralleltest // replaces the store
func TestGetExperimentApps(t *testing.T) {
	c := testExperiment(t, "")
	c.Spec["scenario"] = map[string]any{"apps": []any{map[string]any{"name": "soh"}, map[string]any{"name": "ntp"}}}
	c.Status = map[string]any{"appRunningStageStatus": map[string]any{"soh": true}}
	useTestStore(t, c)

	vars := map[string]string{"name": "test-experiment"}

	got, rec := serveHandler(
		GetExperimentApps,
		roleRequest(http.MethodGet, "/", "", vars, testRole("experiments/apps", "get", "test-experiment")),
	)
	if got.Status != http.StatusOK {
		t.Fatalf("GET apps = %+v, want 200: %s", got, rec.Body)
	}

	var apps map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(map[string]bool{"soh": true, "ntp": false}, apps); diff != "" {
		t.Errorf("GET apps mismatch (-want +got):\n%s", diff)
	}

	if got, _ := serveHandler(
		GetExperimentApps,
		roleRequest(http.MethodGet, "/", "", vars, testRole("experiments/apps", "get", "other")),
	); got.Status != http.StatusForbidden {
		t.Errorf("GET apps of another experiment's grant = %+v, want 403", got)
	}
}

// Triggering apps of an experiment that cannot run them announces each start
// and then its error.
//
//nolint:paralleltest // replaces the store and subscribes to the trigger topic
func TestTriggerExperimentAppsReportsErrors(t *testing.T) {
	useTestStore(t, namedExperiment(t, "none", ""))

	ch := pubsub.Subscribe("trigger-app")
	t.Cleanup(func() { pubsub.Unsubscribe("trigger-app", ch) })

	vars := map[string]string{"name": "none"}
	req := roleRequest(http.MethodPost, "/?apps=soh", "", vars, testRole("experiments/trigger", "create", "none"))

	if got, rec := serveHandler(TriggerExperimentApps, req); got.Status != http.StatusNoContent {
		t.Fatalf("POST trigger = %+v, want 204: %s", got, rec.Body)
	}

	states := make([]string, 0, 2)

	for range 2 {
		pub, _ := receiveTrigger(t, ch).(app.TriggerPublication)
		if pub.Experiment != "none" || pub.App != "soh" {
			t.Errorf("trigger publication = %+v, want one for none/soh", pub)
		}

		if pub.State == "error" && pub.Error == nil {
			t.Errorf("error publication = %+v, want an error", pub)
		}

		states = append(states, pub.State)
	}

	if !slices.Equal(states, []string{"start", "error"}) {
		t.Errorf("trigger states = %v, want [start error]", states)
	}

	if got, _ := serveHandler(
		TriggerExperimentApps,
		roleRequest(http.MethodPost, "/?apps=soh", "", vars, testRole("experiments/trigger", "create", "other")),
	); got.Status != http.StatusForbidden {
		t.Errorf("POST trigger with another experiment's grant = %+v, want 403", got)
	}
}
