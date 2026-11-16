package scorch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/scorch/scorchexe"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// statusAnswer is the status of an answer and the code and message of its
// JSON Error body.
type statusAnswer struct {
	Status  int
	Code    weberror.Code
	Message string
}

// statusAnswerOf serves the request through the SCORCH routes as serveScorch
// does, and returns its answer.
func statusAnswerOf(t *testing.T, method, path string, role *rbac.Role) statusAnswer {
	t.Helper()

	rec := serveScorch(method, path, role)

	var body struct {
		Code    weberror.Code `json:"code"`
		Message string        `json:"message"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s = %d %q, which is not a JSON Error: %v", method, path, rec.Code, rec.Body, err)
	}

	return statusAnswer{Status: rec.Code, Code: body.Code, Message: body.Message}
}

// failingGetStore is a store whose Get always fails, as a store that is down.
type failingGetStore struct {
	store.Store
}

var errStoreDown = errors.New("store down")

func (failingGetStore) Get(*store.Config) error { return errStoreDown }

// The pipeline routes answer 404 for an experiment, SCORCH app, run or loop
// that does not exist, 409 for a run that cannot start now, and 500 when the
// store fails.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestPipelineStatuses(t *testing.T) {
	useScorch(t, map[string]any{"start": []string{"a"}, "cleanup": []string{"c"}})

	runs := map[string]any{"runs": []map[string]any{{"start": []string{"a"}}}}
	handlersStore(t, "stopped", handlersScorchSpec(runs), nil)
	handlersStore(t, "plain", map[string]any{}, map[string]any{"startTime": "2026-01-01T00:00:00Z"})
	t.Cleanup(func() { DeletePipeline("stopped", -1, -1, false) })

	// run 0 of exp is executing
	scorchexe.AddCanceler(context.Background(), "exp", 0)
	t.Cleanup(func() { scorchexe.GetCanceler("exp", 0)() })

	all := handlersRole("*", "experiments", "get", "experiments/trigger", "create")

	notFound := func(message string) statusAnswer {
		return statusAnswer{http.StatusNotFound, weberror.CodeNotFound, message}
	}

	conflict := func(message string) statusAnswer {
		return statusAnswer{http.StatusConflict, weberror.CodeConflict, message}
	}

	tests := map[string]struct {
		method, path string
		want         statusAnswer
	}{
		"pipelines of a missing experiment": {
			http.MethodGet, "/experiments/missing/scorch/pipelines", notFound("experiment missing not found"),
		},
		"pipeline of a missing experiment": {
			http.MethodGet, "/experiments/missing/scorch/pipelines/0/0", notFound("experiment missing not found"),
		},
		"pipeline of a run that does not exist": {
			http.MethodGet, "/experiments/exp/scorch/pipelines/9/0", notFound("Scorch pipeline 9 not found for experiment exp"),
		},
		"pipeline of a loop that does not exist": {
			http.MethodGet, "/experiments/exp/scorch/pipelines/0/3", notFound("Scorch pipeline 0 not found for experiment exp"),
		},
		"pipeline of an experiment without SCORCH": {
			http.MethodGet, "/experiments/plain/scorch/pipelines/0/0", notFound("Scorch pipeline 0 not found for experiment plain"),
		},
		"clear in a missing experiment": {
			http.MethodPost, "/experiments/missing/scorch/pipelines/0/clear", notFound("experiment missing not found"),
		},
		"start in a missing experiment": {
			http.MethodPost, "/experiments/missing/scorch/pipelines/0", notFound("experiment missing not found"),
		},
		"clean up in a missing experiment": {
			http.MethodPost, "/experiments/missing/scorch/pipelines/0/cleanup", notFound("experiment missing not found"),
		},
		"start a run that does not exist": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/9", notFound("Scorch run 9 not found for experiment exp"),
		},
		"start a run of an experiment without SCORCH": {
			http.MethodPost, "/experiments/plain/scorch/pipelines/0", notFound("Scorch run 0 not found for experiment plain"),
		},
		"start a run that is executing": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/0", conflict("Scorch run already executing for experiment exp"),
		},
		"clean up a run that is executing": {
			http.MethodPost, "/experiments/exp/scorch/pipelines/0/cleanup",
			conflict("Scorch run already executing for experiment exp"),
		},
		"start a run of a stopped experiment": {
			http.MethodPost, "/experiments/stopped/scorch/pipelines/0", conflict("experiment stopped is not running"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, statusAnswerOf(t, tc.method, tc.path, all)); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
		})
	}
}

// A run of an experiment whose SCORCH app already runs another run does not
// start, and nothing is published.
//
//nolint:paralleltest // starts the SCORCH processors, replaces the store and subscribes to publications
func TestStartPipelineWhileTheAppRuns(t *testing.T) {
	// useScorch's experiment runs, and so does its SCORCH app
	useScorch(t, map[string]any{"start": []string{"a"}, "cleanup": []string{"c"}})

	pubs := handlersPublications(t)
	operator := roleFor("experiments", "get", "experiments/trigger", "create")

	want := statusAnswer{http.StatusConflict, weberror.CodeConflict, "the Scorch app is already running for experiment exp"}

	for _, path := range []string{"/experiments/exp/scorch/pipelines/0", "/experiments/exp/scorch/pipelines/0/cleanup"} {
		if diff := cmp.Diff(want, statusAnswerOf(t, http.MethodPost, path, operator)); diff != "" {
			t.Errorf("POST %s mismatch (-want +got):\n%s", path, diff)
		}
	}

	select {
	case msg := <-pubs:
		t.Errorf("after a refused start, publication %+v, want none", msg)
	default:
	}

	if scorchexe.HasCanceler("exp", 0) {
		t.Error("after a refused start, run 0 has a canceler, want none")
	}
}

// The pipeline routes answer 500 when the store fails.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestPipelineStatusesWithTheStoreDown(t *testing.T) {
	useScorch(t)
	storetest.Use(t, failingGetStore{Store: store.DefaultStore})

	all := handlersRole("*", "experiments", "get", "experiments/trigger", "create")

	for path, want := range map[string]statusAnswer{
		"GET /experiments/down/scorch/pipelines": {
			http.StatusInternalServerError, weberror.CodeInternal, "unable to get experiment down from store",
		},
		"GET /experiments/down/scorch/pipelines/0/0": {
			http.StatusInternalServerError, weberror.CodeInternal, "unable to get pipeline 0 for experiment down",
		},
		"POST /experiments/down/scorch/pipelines/0": {
			http.StatusInternalServerError, weberror.CodeInternal, "unable to get experiment down from store",
		},
		"POST /experiments/down/scorch/pipelines/0/clear": {
			http.StatusInternalServerError, weberror.CodeInternal, "unable to get experiment down from store",
		},
		"GET /experiments/down/scorch/components/0/0/start/a": {
			http.StatusInternalServerError, weberror.CodeInternal, "unable to get experiment down from store",
		},
	} {
		t.Run(path, func(t *testing.T) {
			method, target, _ := strings.Cut(path, " ")

			if diff := cmp.Diff(want, statusAnswerOf(t, method, target, all)); diff != "" {
				t.Errorf("%s mismatch (-want +got):\n%s", path, diff)
			}
		})
	}
}

// The terminal and component routes answer 404 for a terminal, component or
// experiment that does not exist, and 400 for a PID that is not a number.
//
//nolint:paralleltest // starts the SCORCH processors, replaces the store and uses the component state
func TestTerminalAndOutputStatuses(t *testing.T) {
	useScorch(t)

	const breakName = "status-break-no-term"

	brk := update(breakName, statusStart, "")
	brk.CmpType = componentTypeBreak
	UpdateComponent(brk)
	UpdateComponent(update(breakName, statusRunning, ""))
	handlersFinish(t, breakName)

	operator := handlersRole("*", "experiments", "get", "experiments/trigger", "create")

	tests := map[string]struct {
		method, path string
		want         statusAnswer
	}{
		"component without a terminal": {
			http.MethodGet, "/experiments/exp/scorch/terminals/0/0/start/none",
			statusAnswer{http.StatusNotFound, weberror.CodeNotFound, "no web terminal found"},
		},
		"output of a break without a terminal": {
			http.MethodGet, handlersComponentPath(breakName),
			statusAnswer{http.StatusNotFound, weberror.CodeNotFound, "unable to initialize terminal"},
		},
		"output of a missing experiment": {
			http.MethodGet, "/experiments/missing/scorch/components/0/0/start/ping",
			statusAnswer{http.StatusNotFound, weberror.CodeNotFound, "experiment missing not found"},
		},
		"exit of a PID that is not a number": {
			http.MethodPost, "/experiments/exp/scorch/terminals/abc/exit/id",
			statusAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "invalid terminal PID provided"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, statusAnswerOf(t, tc.method, tc.path, operator)); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
		})
	}
}
