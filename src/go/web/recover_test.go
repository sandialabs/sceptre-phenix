package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"phenix/util/mm"
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
