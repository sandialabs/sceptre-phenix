package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/util/file"
	"phenix/util/polltest"
)

// experimentsStoreFails makes the store fail every read of an experiment config, and returns path.
func experimentsStoreFails(path string) func(*testing.T, contractFixture) string {
	return func(t *testing.T, _ contractFixture) string {
		t.Helper()

		failTestStore(t, "get", "Experiment", errors.New("disk failed"))

		return path
	}
}

// experimentsStopped stores the stopped experiment stopped, and returns path.
func experimentsStopped(path string) func(*testing.T, contractFixture) string {
	return func(t *testing.T, _ contractFixture) string {
		t.Helper()

		useTestStore(t, namedExperiment(t, "stopped", ""))

		return path
	}
}

// experimentsStatusCases are the error statuses of the experiment routes for a missing experiment, a store failure,
// an experiment in the wrong state, a duplicate and bad query parameters.
func experimentsStatusCases() map[string]contractErrorCase {
	const (
		exp  = contractExpPath
		none = "/api/v1/experiments/none"
	)

	return map[string]contractErrorCase{
		"get of missing experiment":  {method: "GET", path: none, want: http.StatusNotFound},
		"get when the store fails":   {method: "GET", prepare: experimentsStoreFails(exp), want: http.StatusInternalServerError},
		"apps of missing experiment": {method: "GET", path: none + "/apps", want: http.StatusNotFound},
		"apps when the store fails": {
			method:  "GET",
			prepare: experimentsStoreFails(exp + "/apps"),
			want:    http.StatusInternalServerError,
		},
		"topology of missing experiment": {method: "GET", path: none + "/topology", want: http.StatusNotFound},
		"topology when the store fails": {
			method: "GET", prepare: experimentsStoreFails(exp + "/topology"), want: http.StatusInternalServerError,
		},
		"topology search of missing experiment": {
			method: "GET", path: none + "/topology/search?hostname=x", want: http.StatusNotFound,
		},
		"soh of missing experiment":      {method: "GET", path: none + "/soh", want: http.StatusNotFound},
		"soh with unknown status filter": {method: "GET", path: exp + "/soh?statusFilter=bogus", want: http.StatusBadRequest},
		"schedule of missing experiment": {method: "GET", path: none + "/schedule", want: http.StatusNotFound},
		"schedule when the store fails": {
			method:  "GET",
			prepare: experimentsStoreFails(exp + "/schedule"),
			want:    http.StatusInternalServerError,
		},
		"delete of missing experiment": {method: "DELETE", path: none, want: http.StatusNotFound},
		"delete of running experiment": {method: "DELETE", path: exp, want: http.StatusConflict},
		"start of missing experiment":  {method: "POST", path: none + "/start", want: http.StatusNotFound},
		"start of running experiment":  {method: "POST", path: exp + "/start", want: http.StatusConflict},
		"stop of missing experiment":   {method: "POST", path: none + "/stop", want: http.StatusNotFound},
		"stop of stopped experiment": {
			method:  "POST",
			prepare: experimentsStopped("/api/v1/experiments/stopped/stop"),
			want:    http.StatusConflict,
		},
		"netflow of missing experiment": {method: "POST", path: none + "/netflow", want: http.StatusNotFound},
		"trigger without apps":          {method: "POST", path: exp + "/trigger", want: http.StatusBadRequest},
		"trigger of an empty app":       {method: "POST", path: exp + "/trigger?apps=soh,", want: http.StatusBadRequest},
		"trigger of missing experiment": {method: "POST", path: none + "/trigger?apps=soh", want: http.StatusNotFound},
		"trigger of an invalid stage":   {method: "POST", path: exp + "/trigger?apps=soh&stage=bogus", want: http.StatusBadRequest},
		"trigger of an app the experiment lacks": {
			method: "POST", path: exp + "/trigger?apps=none&stage=configure", want: http.StatusBadRequest,
		},
		"trigger cancel of an empty app list": {method: "DELETE", path: exp + "/trigger?apps=", want: http.StatusBadRequest},
		"create with the name of experiment": {
			method: "POST", path: "/api/v1/experiments", want: http.StatusConflict,
			body: `{"name":"` + contractExp + `","topology":"` + contractTopo + `"}`,
		},
	}
}

// The experiment routes answer 404 for a missing experiment, 500 when the store fails, 409 for an experiment in the
// wrong state or a duplicate name, and 400 for bad query parameters, each with the Error body openapi.yml documents.
func TestExperimentsAPIStatus(t *testing.T) {
	spec := loadOpenAPI(t)

	for name, tc := range experimentsStatusCases() {
		t.Run(name, func(t *testing.T) { checkContractError(t, spec, name, tc) })
	}
}

// experimentsCleanupFails is a cluster whose file deletes fail.
type experimentsCleanupFails struct {
	file.ClusterFiles
}

func (experimentsCleanupFails) DeleteFile(string) error { return errors.New("mesh unreachable") }

// A delete that removes the experiment but not all of its files answers 500,
// and still announces the delete to websocket clients.
func TestExperimentsDeleteCleanupFails(t *testing.T) {
	useTestStore(t, namedExperiment(t, "doomed", ""))

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = experimentsCleanupFails{} //nolint:reassign // test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double

	messages := jobsBrokerClient(t, adminRole())
	url := serveAs(t, "alice", adminRole()).URL + "/api/v1/experiments/doomed"

	resp := do(t, http.MethodDelete, url, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("DELETE %s = %d, want %d: %s", url, resp.StatusCode, http.StatusInternalServerError, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodDelete, "/api/v1/experiments/doomed", resp)
	errorMessage(t, http.StatusInternalServerError, readBody(t, resp))

	c, _ := store.NewConfig("experiment/doomed")
	if err := store.Get(c); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("store.Get(experiment/doomed) after DELETE error = %v, want store.ErrNotExist", err)
	}

	polltest.Until(t, "the delete event", func() bool { return slices.Contains(messages("experiment"), "delete doomed") })
}

// A second start of a running netflow capture answers 409.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestExperimentsNetflowAlreadyStarted(t *testing.T) {
	useNetflowExperiment(t, "br0", true)

	role := combinedRole(netflowRole(), testRole("experiments/netflow", "create", netflowTestExp))
	url := serveAs(t, "alice", role).URL + "/api/v1/experiments/" + netflowTestExp + "/netflow"

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s = %d, want %d: %s", url, resp.StatusCode, http.StatusNoContent, readBody(t, resp))
	}

	resp := do(t, http.MethodPost, url, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second POST %s = %d, want %d: %s", url, resp.StatusCode, http.StatusConflict, body)
	}

	if got, want := errorMessage(t, http.StatusConflict, body), "netflow already started for experiment"; got != want {
		t.Errorf("second POST %s error message = %q, want %q", url, got, want)
	}

	if !loadOpenAPI(t).documents(http.MethodPost, "/api/v1/experiments/"+netflowTestExp+"/netflow", http.StatusConflict) {
		t.Errorf("openapi.yml documents 409 for POST /experiments/{name}/netflow = false, want true")
	}
}

// An experiment without files, or a filter that matches none, lists files as
// an empty array, not null.
func TestExperimentsFilesEmptyList(t *testing.T) {
	useTestExperiment(t)
	useTestFiles(t)

	url := serveAs(t, "alice", testRole("experiments/files", "list", contractExp)).URL + contractExpPath + "/files"

	for _, query := range []string{"", "?filter=zzz"} {
		t.Run(query, func(t *testing.T) {
			resp := do(t, http.MethodGet, url+query, "")
			wantDocumentedBody(t, http.MethodGet, contractExpPath+"/files"+query, resp)
			body := readBody(t, resp)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s%s = %d, want %d: %s", url, query, resp.StatusCode, http.StatusOK, body)
			}

			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decoding GET %s%s body %q: %v", url, query, body, err)
			}

			want := map[string]any{"files": []any{}, "total": float64(0)}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GET %s%s body mismatch (-want +got):\n%s", url, query, diff)
			}
		})
	}
}
