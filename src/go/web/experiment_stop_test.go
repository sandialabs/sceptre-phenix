package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

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

// A stop whose VMs minimega could not kill leaves the experiment running and
// says it can be forced; the forced stop marks it stopped and is audited so.
func TestStopExperimentOffersAForcedRetry(t *testing.T) {
	const started = "2024-01-01T00:00:00Z"

	useTestStore(t, namedExperiment(t, "running", started))

	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}}}
	fake.failing.Store(true)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = fake //nolint:reassign // install test double

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
	wantAudit(t, got, "experiment-stop", "success", map[string]any{
		"phenix.experiment.name": "running", "phenix.experiment.force": true,
	})
}

// A stop that leaves the experiment running re-arms what it canceled first:
// the periodic apps and the delayed VM starts still pending.
func TestFailedStopRearmsPendingWork(t *testing.T) {
	const due = "2999-01-01T00:00:00Z"

	exp := namedExperiment(t, "running", "2024-01-01T00:00:00Z")
	exp.Status["delayedTimers"] = map[string]any{"late": due}

	useTestStore(t, exp)

	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}, states: map[string]string{"late": "BUILDING"}}}
	fake.failing.Store(true)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() {
		commonMu.Lock()
		cancels := cancelers["running"]
		delete(cancelers, "running")
		delete(waiters, "running")
		commonMu.Unlock()

		for _, c := range cancels {
			c.cancel()
		}
	})

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

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() {
		commonMu.Lock()
		cancels := cancelers["running"]
		delete(cancelers, "running")
		delete(waiters, "running")
		commonMu.Unlock()

		for _, c := range cancels {
			c.cancel()
		}
	})

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

	forced := make(chan int, 1)

	go func() {
		resp := do(t, http.MethodPost, stop+"?force=true", "")
		_ = readBody(t, resp)
		forced <- resp.StatusCode
	}()

	select {
	case code := <-forced:
		if code != http.StatusOK {
			t.Errorf("POST stop?force=true = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("POST stop?force=true did not return; the periodic apps were not canceled")
	}
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
