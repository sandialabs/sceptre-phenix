package scorch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"phenix/store"
)

// GetPipelines lists each run's pipeline, saying whether it can be cleaned up
// on its own, with the run executing, whether the SCORCH app is running and
// the experiment's status, so the SCORCH pages need no other request.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestGetPipelines(t *testing.T) {
	useScorch(t,
		map[string]any{"name": "setup", "start": []string{"a"}, "cleanup": []string{"c"}},
		map[string]any{"name": "attack", "start": []string{"a"}},
	)

	rec := serveScorch(http.MethodGet, "/experiments/exp/scorch/pipelines", roleFor("experiments", "get"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var body struct {
		Pipelines []struct {
			Name       string `json:"name"`
			HasCleanup bool   `json:"hasCleanup"`
		} `json:"pipelines"`
		Running    int            `json:"running"`
		AppRunning bool           `json:"app_running"`
		Experiment map[string]any `json:"experiment"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	type run struct {
		Name       string
		HasCleanup bool
	}

	runs := make([]run, len(body.Pipelines))
	for i, p := range body.Pipelines {
		runs[i] = run{p.Name, p.HasCleanup}
	}

	if want := []run{{"setup", true}, {"attack", false}}; !reflect.DeepEqual(runs, want) {
		t.Errorf("pipelines = %+v, want %+v", runs, want)
	}

	if body.Running != 3 || !body.AppRunning {
		t.Errorf("running = %d, app_running = %v; want run 3 of a running app", body.Running, body.AppRunning)
	}

	if want := map[string]any{"status": "started"}; !reflect.DeepEqual(body.Experiment, want) {
		t.Errorf("experiment = %v, want %v", body.Experiment, want)
	}

	rec = serveScorch(http.MethodGet, "/experiments/exp/scorch/pipelines", roleFor("experiments", "list"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("listing pipelines without experiment get: status %d, want 403", rec.Code)
	}
}

// GetPipeline returns one loop of a run's pipeline, loop 0 being the run
// itself, and answers 400 for a run or loop the experiment does not configure.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestGetPipeline(t *testing.T) {
	useScorch(t,
		map[string]any{"name": "flat", "start": []string{"a"}},
		map[string]any{"name": "looped", "start": []string{"a"}, "loop": map[string]any{"start": []string{"b"}}},
	)

	tests := map[string]struct {
		run, loop string
		status    int
		// the run's name and the component started, for a pipeline served
		name, component string
	}{
		"a run":                                {"0", "0", http.StatusOK, "flat", "a"},
		"a loop of a run":                      {"1", "1", http.StatusOK, "looped", "b"},
		"a run that does not exist":            {"2", "0", http.StatusBadRequest, "", ""},
		"a negative run":                       {"-1", "0", http.StatusBadRequest, "", ""},
		"a loop the run does not configure":    {"0", "1", http.StatusBadRequest, "", ""},
		"a loop beyond the run's nested loops": {"1", "2", http.StatusBadRequest, "", ""},
		"a negative loop":                      {"0", "-1", http.StatusBadRequest, "", ""},
		"a run that is not a number":           {"x", "0", http.StatusBadRequest, "", ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := "/experiments/exp/scorch/pipelines/" + tc.run + "/" + tc.loop

			rec := serveScorch(http.MethodGet, path, roleFor("experiments", "get"))
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}

			if tc.status != http.StatusOK {
				return
			}

			var body struct {
				Name     string `json:"name"`
				Pipeline []struct {
					Name  string `json:"name"`
					Stage string `json:"stage"`
					Run   int    `json:"run"`
					Loop  int    `json:"loop"`
				} `json:"pipeline"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}

			if body.Name != tc.name {
				t.Errorf("name %q, want %q", body.Name, tc.name)
			}

			started := false

			for _, n := range body.Pipeline {
				if strconv.Itoa(n.Run) != tc.run || strconv.Itoa(n.Loop) != tc.loop {
					t.Errorf("node %s is of run %d, loop %d", n.Name, n.Run, n.Loop)
				}

				started = started || (n.Name == tc.component && n.Stage == stageStart)
			}

			if !started {
				t.Errorf("pipeline %+v does not start component %s", body.Pipeline, tc.component)
			}
		})
	}
}

// A break component waiting for attention in a nested loop marks the loops
// above it unstable. If a loop above it can no longer be read, the update
// fails and the pipelines keep being served.
func TestBreakMarksTheLoopsAboveIt(t *testing.T) {
	useScorch(t, map[string]any{"name": "looped", "start": []string{"a"}, "loop": map[string]any{"start": []string{"b"}}})

	brk := ComponentUpdate{
		Exp: "exp", Run: 0, Loop: 1, Stage: stageStart, CmpName: "b", CmpType: "break", Status: statusRunning,
	}

	if err := UpdatePipeline(brk); err != nil {
		t.Fatal(err)
	}

	if got := componentStatus(t, 0, stageLoop); got != statusUnstable {
		t.Fatalf("loop status = %q, want %q", got, statusUnstable)
	}

	// only loop 1 is built when the experiment is gone
	DeletePipeline("exp", -1, -1, false)

	getLoop1 := func() *httptest.ResponseRecorder {
		return serveScorch(http.MethodGet, "/experiments/exp/scorch/pipelines/0/1", roleFor("experiments", "get"))
	}

	if rec := getLoop1(); rec.Code != http.StatusOK {
		t.Fatalf("getting loop 1: %d %s", rec.Code, rec.Body)
	}

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Delete(c); err != nil {
		t.Fatal(err)
	}

	if err := UpdatePipeline(brk); err == nil {
		t.Fatal("updating a break whose loop above is gone succeeded")
	}

	if rec := getLoop1(); rec.Code != http.StatusOK {
		t.Fatalf("getting loop 1 after the failed update: %d %s", rec.Code, rec.Body)
	}
}
