package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/job"
	"phenix/web/cache"
)

// fakeVMAction records the VMs the named action touches and fails those in fail;
// block, if set, holds each call until closed.
func fakeVMAction(t *testing.T, action string, fail map[string]error, block chan struct{}) *[]string {
	t.Helper()

	var (
		mu    sync.Mutex
		acted []string
		a     = vmActions[action]
		do    = a.do
	)

	a.do = func(_ context.Context, _, name string) error {
		if block != nil {
			<-block
		}

		mu.Lock()
		acted = append(acted, name)
		mu.Unlock()

		return fail[name]
	}

	t.Cleanup(func() { a.do = do })

	return &acted
}

// useVMExperiment stores the experiment exp, running unless stopped, with VMs
// of the given names, and a minimega that knows of none of them.
func useVMExperiment(t *testing.T, running bool, names ...string) {
	t.Helper()

	nodes := make([]map[string]any, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, diskVM(name, "a.qc2"))
	}

	started := ""
	if running {
		started = "2024-01-01T00:00:00Z"
	}

	useTestStore(t, namedExperiment(t, "exp", started, nodes...))
	useTestMM(t, new(testMM))
}

// postVMActions asks for a bulk action and returns the response.
func postVMActions(t *testing.T, server, action string, vms ...string) *http.Response {
	t.Helper()

	body, err := json.Marshal(vmActionsRequest{Action: action, VMs: vms})
	if err != nil {
		t.Fatalf("json.Marshal(%s request) error = %v", action, err)
	}

	return do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/actions", string(body))
}

// A bulk action acts on each VM the role may, skips the others, and reports
// each VM's outcome; a VM another action holds fails, worth retrying.
func TestRunVMActions(t *testing.T) {
	useVMExperiment(t, true, "a", "b", "c")

	var (
		registry = useJobs(t)
		acted    = fakeVMAction(t, "pause", nil, nil)
		server   = serveAs(t, "alice", testRole("vms/stop", "update", "exp/a", "exp/b")).URL
	)

	if err := cache.LockVMForStarting("exp", "b"); err != nil {
		t.Fatalf("cache.LockVMForStarting(exp, b) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockVM("exp", "b") })

	const path = "/api/v1/experiments/exp/vms/actions"

	resp := postVMActions(t, server, "pause", "a", "b", "c", "a", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want %d: %s", path, resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding POST %s: %v", path, err)
	}

	wantStarted := job.Job{
		Kind: "vms.action", Owner: "alice", Experiment: "exp", Title: "Pause 3 VMs",
		Params: map[string]string{"action": "pause"}, Cancelable: true,
		Progress: job.Progress{Total: 3},
		Items:    []job.Item{{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}
	startedOpts := cmp.Options{
		cmpopts.IgnoreFields(job.Job{}, "ID", "Status", "Created", "Updated", "Started", "Finished", "Revision"),
		cmpopts.IgnoreFields(job.Progress{}, "Done", "Bytes"),
		cmpopts.IgnoreFields(job.Item{}, "Status"),
	}

	if diff := cmp.Diff(wantStarted, started, startedOpts); diff != "" {
		t.Errorf("POST %s mismatch (-want +got):\n%s", path, diff)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusPartial {
		t.Errorf("job %s status = %s, want %s", j.ID, j.Status, job.StatusPartial)
	}

	locked := &cache.LockedError{What: "vm", Name: "b", Status: cache.StatusStarting}
	wantItems := []job.Item{
		{Name: "a", Status: job.ItemSucceeded},
		{Name: "b", Status: job.ItemFailed, Error: locked.Error(), Retryable: true},
		{Name: "c", Status: job.ItemSkipped, Error: job.ErrForbidden.Error()},
	}

	if diff := cmp.Diff(wantItems, j.Items, cmpopts.IgnoreFields(job.Item{}, "Result")); diff != "" {
		t.Fatalf("job %s items mismatch (-want +got):\n%s", j.ID, diff)
	}

	var result struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(j.Items[0].Result, &result); err != nil {
		t.Errorf("job %s item a result = %s, not a VM: %v", j.ID, j.Items[0].Result, err)
	} else if result.Name != "a" {
		t.Errorf("job %s item a result name = %q, want a", j.ID, result.Name)
	}

	if diff := cmp.Diff([]string{"a"}, *acted); diff != "" {
		t.Errorf("paused VMs mismatch (-want +got):\n%s", diff)
	}
}

// Refusals come before any job starts.
func TestRunVMActionsRefuses(t *testing.T) {
	useVMExperiment(t, true, "a")

	var (
		registry = useJobs(t)
		server   = serveAs(t, "alice", testRole("vms/stop", "update", "exp/a")).URL
		tooMany  = make([]string, maxBulkVMs+1)
	)

	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("vm%d", i)
	}

	for name, tc := range map[string]struct {
		action string
		vms    []string
		want   int
	}{
		"unknown action":        {"explode", []string{"a"}, http.StatusBadRequest},
		"no VMs":                {"pause", nil, http.StatusBadRequest},
		"only empty names":      {"pause", []string{""}, http.StatusBadRequest},
		"too many":              {"pause", tooMany, http.StatusBadRequest},
		"allowed on none":       {"start", []string{"a"}, http.StatusForbidden},
		"allowed on none named": {"pause", []string{"b"}, http.StatusForbidden},
	} {
		if resp := postVMActions(t, server, tc.action, tc.vms...); resp.StatusCode != tc.want {
			t.Errorf("POST vms/actions with %s = %d, want %d: %s", name, resp.StatusCode, tc.want, readBody(t, resp))
		}
	}

	if resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/actions", "{"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST vms/actions with a malformed body = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	if jobs := registry.List(job.Filter{}); len(jobs) != 0 {
		t.Errorf("Registry.List() after refused requests = %d jobs, want 0", len(jobs))
	}
}

// Canceling a bulk action starts no more VMs; those in progress finish.
func TestRunVMActionsCanceled(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f"}

	useVMExperiment(t, true, names...)

	var (
		registry = useJobs(t)
		block    = make(chan struct{})
		acted    = fakeVMAction(t, "pause", nil, block)
		server   = serveAs(t, "alice", testRole("vms/stop", "update", "exp/*")).URL
	)

	resp := postVMActions(t, server, "pause", names...)

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding POST vms/actions: %v", err)
	}

	cancelURL := "/api/v1/jobs/" + started.ID + "/cancel"
	if resp := do(t, http.MethodPost, server+cancelURL, ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want %d", cancelURL, resp.StatusCode, http.StatusAccepted)
	}

	close(block)

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled {
		t.Errorf("job %s status = %s, want %s", j.ID, j.Status, job.StatusCanceled)
	}

	succeeded, _, _, canceled := j.Counts()
	if succeeded != len(*acted) {
		t.Errorf("job %s succeeded items = %d, want one per paused VM %v", j.ID, succeeded, *acted)
	}

	if succeeded > bulkVMWorkers {
		t.Errorf("job %s succeeded items = %d, want at most the %d in progress", j.ID, succeeded, bulkVMWorkers)
	}

	if succeeded+canceled != len(names) {
		t.Errorf("job %s succeeded + canceled items = %d + %d, want %d", j.ID, succeeded, canceled, len(names))
	}
}

// The single-VM routes answer as they always have.
func TestSingleVMActions(t *testing.T) {
	useVMExperiment(t, true, "a", "b")
	useJobs(t)

	fakeVMAction(t, "pause", map[string]error{"b": errors.New("minimega says no")}, nil)
	fakeVMAction(t, "kill", nil, nil)

	var (
		allowed = serveAs(t, "alice", combinedRole(
			testRole("vms/stop", "update", "exp/*"), testRole("vms", "delete", "exp/*"),
		)).URL
		denied = serveAs(t, "bob", testRole("vms", "get", "exp/*")).URL
	)

	resp := do(t, http.MethodPost, allowed+"/api/v1/experiments/exp/vms/a/stop", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST vms/a/stop = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var stopped struct {
		Name string `json:"name"`
	}

	if body := readBody(t, resp); json.Unmarshal([]byte(body), &stopped) != nil {
		t.Errorf("POST vms/a/stop = %q, want the VM", body)
	} else if stopped.Name != "a" {
		t.Errorf("POST vms/a/stop name = %q, want a", stopped.Name)
	}

	for name, tc := range map[string]struct {
		server, method, path string
		want                 int
		body                 string
	}{
		"stop failing":   {allowed, http.MethodPost, "/b/stop", http.StatusInternalServerError, "minimega says no\n"},
		"stop forbidden": {denied, http.MethodPost, "/a/stop", http.StatusForbidden, "forbidden\n"},
		"kill":           {allowed, http.MethodDelete, "/a", http.StatusNoContent, ""},
		"kill forbidden": {denied, http.MethodDelete, "/a", http.StatusForbidden, "forbidden\n"},
	} {
		resp := do(t, tc.method, tc.server+"/api/v1/experiments/exp/vms"+tc.path, "")
		if resp.StatusCode != tc.want {
			t.Errorf("%s vms%s (%s) = %d, want %d", tc.method, tc.path, name, resp.StatusCode, tc.want)
		}

		if body := readBody(t, resp); body != tc.body {
			t.Errorf("%s vms%s (%s) = %q, want %q", tc.method, tc.path, name, body, tc.body)
		}
	}

	if err := cache.LockVMForStarting("exp", "a"); err != nil {
		t.Fatalf("cache.LockVMForStarting(exp, a) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockVM("exp", "a") })

	resp = do(t, http.MethodPost, allowed+"/api/v1/experiments/exp/vms/a/stop", "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("POST vms/a/stop of a locked VM = %d, want %d", resp.StatusCode, http.StatusConflict)
	}

	locked := &cache.LockedError{What: "vm", Name: "a", Status: cache.StatusStarting}
	if got, want := readBody(t, resp), locked.Error()+"\n"; got != want {
		t.Errorf("POST vms/a/stop of a locked VM = %q, want %q", got, want)
	}
}

// A VM of a stopped experiment cannot be killed.
func TestKillVMNeedsARunningExperiment(t *testing.T) {
	useVMExperiment(t, false, "a")

	acted := fakeVMAction(t, "kill", nil, nil)
	server := serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL

	resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("DELETE vms/a of a stopped experiment = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	if got, want := readBody(t, resp), "experiment not running\n"; got != want {
		t.Errorf("DELETE vms/a of a stopped experiment = %q, want %q", got, want)
	}

	if len(*acted) != 0 {
		t.Errorf("DELETE vms/a of a stopped experiment killed %v, want none", *acted)
	}
}
