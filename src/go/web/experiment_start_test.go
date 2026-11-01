package web

import (
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"phenix/util/mm"
)

// launchMM is the test minimega, reporting no launch progress.
type launchMM struct {
	*testMM
}

func (launchMM) GetLaunchProgress(string, int) (float64, error) { return 0, nil }

// startGoroutines counts the goroutines that run an experiment start.
func startGoroutines() int {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "phenix/web.startExperiment.func")
		}

		buf = make([]byte, 2*len(buf))
	}
}

// A start that experiment.Start refuses answers 400, and the goroutine that
// ran the start exits instead of blocking on a second status send.
func TestRefusedStartLeaksNoGoroutine(t *testing.T) {
	useTestStore(t, namedExperiment(t, "running", "2024-01-01T00:00:00Z"))

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = launchMM{testMM: &testMM{vms: mm.VMs{}}} //nolint:reassign // install test double

	before := startGoroutines()

	start := serveAs(t, "alice", testRole("experiments/start", "update", "running")).URL +
		"/api/v1/experiments/running/start"

	resp := do(t, http.MethodPost, start, "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST start of a running experiment = %d, want %d: %s", resp.StatusCode, http.StatusBadRequest, body)
	}

	deadline := time.Now().Add(5 * time.Second)

	for startGoroutines() > before {
		if time.Now().After(deadline) {
			t.Fatalf("start goroutines after a refused start = %d, want %d", startGoroutines(), before)
		}

		time.Sleep(10 * time.Millisecond)
	}
}
