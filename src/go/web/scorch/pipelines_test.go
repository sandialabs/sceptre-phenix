package scorch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
)

// GetPipelines lists each run's pipeline with cleanup, running run, app and
// experiment status.
//
//nolint:paralleltest // starts the SCORCH processors and replaces the store
func TestGetPipelines(t *testing.T) {
	useScorch(t,
		map[string]any{"name": "setup", "start": []string{"a"}, "cleanup": []string{"c"}},
		map[string]any{"name": "attack", "start": []string{"a"}},
	)

	const path = "/experiments/exp/scorch/pipelines"

	rec := serveScorch(http.MethodGet, path, roleFor("experiments", "get"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, rec.Code, rec.Body)
	}

	type run struct {
		Name       string `json:"name"`
		HasCleanup bool   `json:"hasCleanup"`
	}

	type pipelines struct {
		Pipelines  []run          `json:"pipelines"`
		Running    int            `json:"running"`
		AppRunning bool           `json:"app_running"`
		Experiment map[string]any `json:"experiment"`
	}

	var got pipelines
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding GET %s: %v", path, err)
	}

	want := pipelines{
		Pipelines:  []run{{"setup", true}, {"attack", false}},
		Running:    3,
		AppRunning: true,
		Experiment: map[string]any{"status": "started"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET %s mismatch (-want +got):\n%s", path, diff)
	}

	rec = serveScorch(http.MethodGet, path, roleFor("experiments", "list"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET %s with only experiments list = %d, want 403", path, rec.Code)
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
				t.Fatalf("GET %s = %d %s, want %d", path, rec.Code, rec.Body, tc.status)
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
				t.Fatalf("decoding GET %s: %v", path, err)
			}

			if body.Name != tc.name {
				t.Errorf("GET %s name = %q, want %q", path, body.Name, tc.name)
			}

			started := false

			for _, n := range body.Pipeline {
				if strconv.Itoa(n.Run) != tc.run || strconv.Itoa(n.Loop) != tc.loop {
					t.Errorf("GET %s lists node %s of run %d, loop %d; want run %s, loop %s",
						path, n.Name, n.Run, n.Loop, tc.run, tc.loop)
				}

				started = started || (n.Name == tc.component && n.Stage == stageStart)
			}

			if !started {
				t.Errorf("GET %s pipeline = %+v, want one starting component %s", path, body.Pipeline, tc.component)
			}
		})
	}
}

// A break waiting in a nested loop marks the loops above it unstable; an
// unreadable loop fails the update and pipelines stay served.
func TestBreakMarksTheLoopsAboveIt(t *testing.T) {
	useScorch(t, map[string]any{"name": "looped", "start": []string{"a"}, "loop": map[string]any{"start": []string{"b"}}})

	brk := ComponentUpdate{
		Exp: "exp", Run: 0, Loop: 1, Stage: stageStart, CmpName: "b", CmpType: "break", Status: statusRunning,
	}

	if err := UpdatePipeline(brk); err != nil {
		t.Fatalf("UpdatePipeline(%+v) = %v, want nil", brk, err)
	}

	if got := componentStatus(t, 0, stageLoop); got != statusUnstable {
		t.Errorf("after UpdatePipeline(%+v), run 0 loop status = %q, want %q", brk, got, statusUnstable)
	}

	// only loop 1 is built when the experiment is gone
	DeletePipeline("exp", -1, -1, false)

	const loop1 = "/experiments/exp/scorch/pipelines/0/1"

	getLoop1 := func() *httptest.ResponseRecorder {
		return serveScorch(http.MethodGet, loop1, roleFor("experiments", "get"))
	}

	if rec := getLoop1(); rec.Code != http.StatusOK {
		t.Errorf("GET %s = %d %s, want 200", loop1, rec.Code, rec.Body)
	}

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Delete(c); err != nil {
		t.Fatalf("store.Delete(experiment/exp) = %v", err)
	}

	if err := UpdatePipeline(brk); err == nil {
		t.Errorf("UpdatePipeline(%+v) with the experiment deleted = nil, want an error", brk)
	}

	if rec := getLoop1(); rec.Code != http.StatusOK {
		t.Errorf("GET %s after the failed update = %d %s, want 200", loop1, rec.Code, rec.Body)
	}
}

// runLoop finds loop n of a run nested n deep, and no loop the run does not
// nest or of a run that is not configured.
func TestRunLoopFindsNestedLoops(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		depths := rapid.SliceOfN(rapid.IntRange(0, 3), 0, 3).Draw(t, "depths")
		runs := make([]*scorchmd.Loop, len(depths))

		for i, depth := range depths {
			for loop := depth; loop >= 0; loop-- {
				runs[i] = &scorchmd.Loop{Name: fmt.Sprintf("run %d loop %d", i, loop), Loop: runs[i]}
			}
		}

		run, loop := rapid.IntRange(-1, len(depths)).Draw(t, "run"), rapid.IntRange(-1, 4).Draw(t, "loop")
		got, err := runLoop(runs, run, loop)

		if run < 0 || run >= len(depths) || loop < 0 || loop > depths[run] {
			if err == nil {
				t.Fatalf("runLoop(%v deep, %d, %d) = %+v, want an error", depths, run, loop, got)
			}

			return
		}

		if want := fmt.Sprintf("run %d loop %d", run, loop); err != nil || got.Name != want {
			t.Fatalf("runLoop(%v deep, %d, %d) = %+v, %v; want %s", depths, run, loop, got, err, want)
		}
	})
}
