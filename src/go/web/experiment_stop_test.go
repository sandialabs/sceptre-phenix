package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

// teardownMM is a test minimega whose ClearNamespace fails while failing is set.
type teardownMM struct {
	*testMM

	failing atomic.Bool
}

func (m *teardownMM) ClearNamespace(string) error {
	if m.failing.Load() {
		return errors.New("minimega unreachable")
	}

	return nil
}

// storedStartTime is the start time the store holds for experiment name.
func storedStartTime(t *testing.T, name string) string {
	t.Helper()

	c, _ := store.NewConfig("experiment/" + name)
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment %s) error = %v", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(%s) error = %v", name, err)
	}

	return exp.Status.StartTime()
}

// experimentStopUseMM makes fake answer for minimega for the rest of the test.
func experimentStopUseMM(t *testing.T, fake *teardownMM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// experimentStopClearCancelers cancels and removes, when the test ends, what
// a failed stop of experiment name re-armed.
func experimentStopClearCancelers(t *testing.T, name string) {
	t.Helper()

	t.Cleanup(func() {
		commonMu.Lock()
		cancels := cancelers[name]
		delete(cancelers, name)
		delete(waiters, name)
		commonMu.Unlock()

		for _, c := range cancels {
			c.cancel()
		}
	})
}

// A stop whose VMs minimega could not kill leaves the experiment running and
// says it can be forced; the forced stop marks it stopped and is audited so.
func TestStopExperimentOffersAForcedRetry(t *testing.T) {
	const started = "2024-01-01T00:00:00Z"

	useTestStore(t, namedExperiment(t, "running", started))

	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}}}
	fake.failing.Store(true)

	experimentStopUseMM(t, fake)

	var (
		events = captureAudit(t)
		stop   = serveAs(t, "alice", testRole("experiments/stop", "update", "running")).URL +
			"/api/v1/experiments/running/stop"
	)

	resp := do(t, http.MethodPost, stop, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("POST stop while minimega fails = %d, want %d: %s", resp.StatusCode, http.StatusBadGateway, body)
	}

	var werr struct {
		Code     string            `json:"code"`
		Metadata map[string]string `json:"metadata"`
	}

	if err := json.Unmarshal([]byte(body), &werr); err != nil {
		t.Fatalf("json.Unmarshal(stop error %s) error = %v", body, err)
	}

	if got := werr.Metadata["retry"]; got != "force" {
		t.Errorf("stop error metadata.retry = %q, want %q", got, "force")
	}

	if werr.Code != "unavailable" {
		t.Errorf("stop error code = %q, want %q", werr.Code, "unavailable")
	}

	if got := storedStartTime(t, "running"); got != started {
		t.Errorf("start time after a failed stop = %q, want %q (still running)", got, started)
	}

	if resp := do(t, http.MethodPost, stop+"?force=true", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST stop?force=true = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	if got := storedStartTime(t, "running"); got != "" {
		t.Errorf("start time after a forced stop = %q, want it stopped", got)
	}

	got := events()
	wantAudit(t, got, "experiment-stop", "failure", map[string]any{"phenix.experiment.name": "running"})
	stopped := wantAudit(t, got, "experiment-stop", "success", map[string]any{
		"phenix.experiment.name": "running", "phenix.experiment.force": true,
	})

	// the success gives how long the stop took
	if d, _ := stopped["phenix.duration"].(string); stopped != nil && !strings.HasSuffix(d, "s") {
		t.Errorf("experiment-stop success phenix.duration = %v, want a duration", stopped["phenix.duration"])
	}
}

// A stop that leaves the experiment running re-arms what it canceled first:
// the job of the periodic apps, for the same owner, and the delayed VM starts
// still pending.
func TestFailedStopRearmsPendingWork(t *testing.T) {
	const due = "2999-01-01T00:00:00Z"

	exp := namedExperiment(t, "running", "2024-01-01T00:00:00Z")
	exp.Status["delayedTimers"] = map[string]any{"late": due}
	exp.Spec["scenario"] = map[string]any{
		"apps": []any{map[string]any{"name": "periodic", "runPeriodically": "1h"}},
	}

	useTestStore(t, exp)

	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}, states: map[string]string{"late": "BUILDING"}}}
	fake.failing.Store(true)

	experimentStopUseMM(t, fake)

	experimentStopClearCancelers(t, "running")

	before := startTestPeriodicJob(t, "running")

	stop := serveAs(t, "alice", testRole("experiments/stop", "update", "running")).URL +
		"/api/v1/experiments/running/stop"

	if resp := do(t, http.MethodPost, stop, ""); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("POST stop while minimega fails = %d, want %d: %s", resp.StatusCode, http.StatusBadGateway, readBody(t, resp))
	}

	commonMu.Lock()
	cancels, wg := len(cancelers["running"]), waiters["running"]
	commonMu.Unlock()

	if cancels != 2 || wg == nil {
		t.Errorf("after a failed stop: %d cancelers, waiter %t; want 2 (periodic apps, delayed starts) and a waiter", cancels, wg != nil)
	}

	if j := wantPeriodicJob(t, "running"); j.ID == before.ID || j.Owner != before.Owner {
		t.Errorf("periodic job after a failed stop = %s of %s, want a new job of %s", j.ID, j.Owner, before.Owner)
	}

	c, _ := store.NewConfig("experiment/running")
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment running) error = %v", err)
	}

	stored, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(running) error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"late": due}, stored.Status.DelayedTimers()); diff != "" {
		t.Errorf("delayed timers after a failed stop mismatch (-want +got):\n%s", diff)
	}

	if got := stored.Status.Interrupted(); got != "" {
		t.Errorf("interrupted after a failed stop = %q, want none", got)
	}
}

// A failed stop with no pending start keeps the re-armed periodic apps
// cancelable, so that the forced retry stops them and does not wait forever.
func TestFailedStopWithNothingToResumeKeepsPeriodicApps(t *testing.T) {
	exp := namedExperiment(t, "running", "2024-01-01T00:00:00Z")
	exp.Spec["scenario"] = map[string]any{
		"apps": []any{map[string]any{"name": "periodic", "runPeriodically": "1h"}},
	}

	useTestStore(t, exp)

	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}}}
	fake.failing.Store(true)

	experimentStopUseMM(t, fake)

	experimentStopClearCancelers(t, "running")

	startTestPeriodicJob(t, "running")

	stop := serveAs(t, "alice", testRole("experiments/stop", "update", "running")).URL +
		"/api/v1/experiments/running/stop"

	if resp := do(t, http.MethodPost, stop, ""); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("POST stop while minimega fails = %d, want %d: %s", resp.StatusCode, http.StatusBadGateway, readBody(t, resp))
	}

	commonMu.Lock()
	cancels, wg := len(cancelers["running"]), waiters["running"]
	commonMu.Unlock()

	if cancels != 1 || wg == nil {
		t.Fatalf("after a failed stop: %d cancelers, waiter %t; want 1 (periodic apps) and a waiter", cancels, wg != nil)
	}

	forced := make(chan error, 1)

	go func() { forced <- postStatus(t.Context(), stop+"?force=true", http.StatusOK) }()

	select {
	case err := <-forced:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("POST stop?force=true did not return; the periodic apps were not canceled")
	}
}

// postStatus sends an empty POST to url and reports an error unless the
// status is want. Unlike do it may run outside the test's goroutine.
func postStatus(ctx context.Context, url string, want int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("making POST %s: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading POST %s: %w", url, err)
	}

	if resp.StatusCode != want {
		return fmt.Errorf("POST %s = %d, want %d: %s", url, resp.StatusCode, want, body)
	}

	return nil
}

// dropCanceler cancels and removes only the handle it is given; every other
// canceler of the key stays registered and uncanceled.
func TestDropCancelerKeepsTheOthers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		const key = "drop-canceler-test"

		var (
			count    = rapid.IntRange(1, 8).Draw(t, "count")
			drop     = rapid.SliceOfNDistinct(rapid.IntRange(0, count-1), 0, count, rapid.ID[int]).Draw(t, "drop")
			canceled = make([]bool, count)
			handles  = make([]*canceler, count)
		)

		for i := range count {
			handles[i] = addCanceler(key, func() { canceled[i] = true })
		}

		for _, i := range drop {
			dropCanceler(key, handles[i])
		}

		dropped := make([]bool, count)
		for _, i := range drop {
			dropped[i] = true
		}

		var want []*canceler

		for i, h := range handles {
			if !dropped[i] {
				want = append(want, h)
			}
		}

		commonMu.Lock()
		got, present := cancelers[key]
		delete(cancelers, key)
		commonMu.Unlock()

		if diff := cmp.Diff(dropped, canceled); diff != "" {
			t.Errorf("canceled handles mismatch (-want +got):\n%s", diff)
		}

		if !slices.Equal(want, got) {
			t.Errorf("cancelers after dropping %v of %d = %d entries, want the %d others", drop, count, len(got), len(want))
		}

		if present != (len(want) > 0) {
			t.Errorf("key registered after dropping %v of %d = %t, want %t", drop, count, present, len(want) > 0)
		}
	})
}

// snapshotMM is a test minimega that counts the Headnode calls, which only
// the delete of a stopped experiment's injection snapshots makes.
type snapshotMM struct {
	*teardownMM

	headnodes atomic.Int32
}

func (m *snapshotMM) Headnode() string {
	m.headnodes.Add(1)

	return "headnode"
}

// The keepInjectionSnapshots query parameter of a stop takes a boolean, as
// force does. A stop that keeps the injection snapshots does not delete them
// and is audited so.
func TestStopExperimentKeepInjectionSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		status  int
		deletes bool
		kept    bool
	}{
		{name: "kept", query: "?keepInjectionSnapshots=true", status: http.StatusOK, deletes: false, kept: true},
		{name: "deleted", query: "?keepInjectionSnapshots=false", status: http.StatusOK, deletes: true, kept: false},
		{name: "default", query: "", status: http.StatusOK, deletes: true, kept: false},
		{name: "bare", query: "?keepInjectionSnapshots", status: http.StatusBadRequest, deletes: false, kept: false},
		{name: "not a boolean", query: "?keepInjectionSnapshots=maybe", status: http.StatusBadRequest, deletes: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "running", "2024-01-01T00:00:00Z"))

			fake := &snapshotMM{teardownMM: &teardownMM{testMM: &testMM{vms: mm.VMs{}}}}
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

			mm.DefaultMM = fake //nolint:reassign // install test double

			var (
				events = captureAudit(t)
				stop   = serveAs(t, "alice", testRole("experiments/stop", "update", "running")).URL +
					"/api/v1/experiments/running/stop" + tt.query
			)

			resp := do(t, http.MethodPost, stop, "")
			if resp.StatusCode != tt.status {
				t.Fatalf("POST stop%s = %d, want %d: %s", tt.query, resp.StatusCode, tt.status, readBody(t, resp))
			}

			if got := fake.headnodes.Load() > 0; got != tt.deletes {
				t.Errorf("POST stop%s deleted the injection snapshots = %t, want %t", tt.query, got, tt.deletes)
			}

			if tt.status != http.StatusOK {
				return
			}

			event := wantAudit(t, events(), "experiment-stop", "success", map[string]any{"phenix.experiment.name": "running"})
			if _, got := event["phenix.experiment.keep_injection_snapshots"]; got != tt.kept {
				t.Errorf("stop%s audit has phenix.experiment.keep_injection_snapshots = %t, want %t", tt.query, got, tt.kept)
			}
		})
	}
}

// startTestPeriodicJob starts, as alice, the job of the periodic apps of the stored experiment name, in a job
// registry of the test, and returns it.
func startTestPeriodicJob(t *testing.T, name string) job.Job {
	t.Helper()

	useJobs(t)

	exp, err := experiment.Get(name)
	if err != nil {
		t.Fatalf("experiment.Get(%s) error = %v", name, err)
	}

	j := startPeriodicJob("alice", exp, false)
	if j == nil {
		t.Fatalf("startPeriodicJob(alice, %s) = nil, want a job", name)
	}

	t.Cleanup(func() { cancelPeriodicJob(name) })

	return *j
}

// wantPeriodicJob returns the active job of the periodic apps of experiment name, or fails the test.
func wantPeriodicJob(t *testing.T, name string) job.Job {
	t.Helper()

	commonMu.Lock()
	id, ok := periodicJobs[name]
	commonMu.Unlock()

	j, found := jobs.Get(id)
	if !ok || !found || j.Status.Terminal() {
		t.Fatalf("periodic job of %s = %q (%s), want an active job", name, id, j.Status)
	}

	return j
}
