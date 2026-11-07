package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/polltest"
)

// A start a restarted phenix ui cannot resume is recorded as interrupted,
// which the experiment's API answer reports, and leaves nothing to cancel.
func TestRecoveredStartThatCannotResumeIsInterrupted(t *testing.T) {
	exp := namedExperiment(t, "waiting", "2024-01-01T00:00:00Z")
	exp.Status["delayedTimers"] = map[string]any{"gone": "2024-01-01T00:05:00Z"}
	exp.Status["postStartPending"] = true

	useTestStore(t, exp)
	useTestMM(t, &testMM{vms: mm.VMs{}, states: map[string]string{}})

	// recoverExperiments runs this in the background for each experiment with a pending start
	resumeStart("waiting")

	commonMu.Lock()
	_, cancelable := cancelers["waiting"]
	commonMu.Unlock()

	if cancelable {
		t.Error("a start that could not resume left a canceler, want none")
	}

	url := serveAs(t, "alice", testRole("experiments", "get", "waiting")).URL + "/api/v1/experiments/waiting"

	resp := do(t, http.MethodGet, url, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET experiment = %d, want %d: %s", resp.StatusCode, http.StatusOK, body)
	}

	var got struct {
		Interrupted string `json:"interrupted"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
	}

	if !strings.Contains(got.Interrupted, "no longer in minimega: gone") {
		t.Errorf("GET experiment interrupted = %q, want it to name gone", got.Interrupted)
	}
}

// recoverExperiments clears the app running flags of every running experiment, and resumes in the background each
// one's pending start, which here cannot resume and so is recorded as interrupted.
//
//nolint:paralleltest // replaces the store and minimega, and resumes starts in the background
func TestRecoverExperiments(t *testing.T) {
	waiting := namedExperiment(t, "waiting", "2024-01-01T00:00:00Z")
	waiting.Status["delayedTimers"] = map[string]any{"gone": "2024-01-01T00:05:00Z"}
	waiting.Status["appRunningStageStatus"] = map[string]any{"scorch": true, "soh": false}

	idle := namedExperiment(t, "idle", "2024-01-01T00:00:00Z")
	idle.Status["appRunningStageStatus"] = map[string]any{"soh": true}

	stopped := namedExperiment(t, "stopped", "")
	stopped.Status = map[string]any{"appRunningStageStatus": map[string]any{"soh": true}}

	useTestStore(t, waiting, idle, stopped)
	useTestMM(t, &testMM{vms: mm.VMs{}, states: map[string]string{}})

	recoverExperiments()

	polltest.Until(t, "the pending start is recorded as interrupted", func() bool {
		exp, err := experiment.Get("waiting")

		return err == nil && strings.Contains(exp.Status.Interrupted(), "no longer in minimega: gone")
	})

	polltest.Until(t, "the resumed start lets go of its canceler", func() bool {
		commonMu.Lock()
		defer commonMu.Unlock()

		_, ok := cancelers["waiting"]

		return !ok
	})

	got := map[string]map[string]bool{}

	for _, name := range []string{"waiting", "idle", "stopped"} {
		exp, err := experiment.Get(name)
		if err != nil {
			t.Fatalf("experiment.Get(%s) error = %v", name, err)
		}

		got[name] = exp.Status.AppRunning()
	}

	want := map[string]map[string]bool{
		"waiting": {"scorch": false, "soh": false},
		"idle":    {"soh": false},
		"stopped": {"soh": true}, // not running, so left as it is
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("app running flags after recoverExperiments() mismatch (-want +got):\n%s", diff)
	}
}

// recoverExperiments logs, and resumes nothing, when it cannot list the experiments.
//
//nolint:paralleltest // replaces the store and the logs
func TestRecoverExperimentsStoreFailure(t *testing.T) {
	useTestStore(t)

	s, _ := store.DefaultStore.(*testStore)
	store.DefaultStore = failingListStore{testStore: s} //nolint:reassign // useTestStore restores it

	records := captureRecords(t)

	commonMu.Lock()
	before := len(cancelers)
	commonMu.Unlock()

	recoverExperiments()

	var logged []string

	for _, line := range records() {
		for _, msg := range []string{
			"clearing stale app running flags", "clearing stale experiment start markers", "listing experiments with pending starts",
		} {
			if strings.Contains(line, `"msg":"`+msg+`"`) && strings.Contains(line, "store unreadable") {
				logged = append(logged, msg)
			}
		}
	}

	want := []string{
		"clearing stale app running flags", "clearing stale experiment start markers", "listing experiments with pending starts",
	}
	if diff := cmp.Diff(want, logged); diff != "" {
		t.Errorf("recoverExperiments() with an unreadable store logged errors mismatch (-want +got):\n%s", diff)
	}

	commonMu.Lock()
	defer commonMu.Unlock()

	if n := len(cancelers); n != before {
		t.Errorf("recoverExperiments() with an unreadable store left %d cancelers, want the %d before", n, before)
	}
}
