package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"phenix/api/job"
	"phenix/web/cache"
)

// fakeVMAction makes the named action's minimega call record the VMs it is
// taken on and fail on those in fail, for the rest of the test; block, if not
// nil, holds each call until it is closed.
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
		t.Fatal(err)
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
		t.Fatal(err)
	}

	t.Cleanup(func() { cache.UnlockVM("exp", "b") })

	resp := postVMActions(t, server, "pause", "a", "b", "c", "a", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	if started.Kind != "vms.action" || started.Title != "Pause 3 VMs" || started.Params["action"] != "pause" ||
		started.Experiment != "exp" || len(started.Items) != 3 {
		t.Errorf("started %+v", started)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusPartial {
		t.Errorf("job %s, want partial", j.Status)
	}

	a, b, c := j.Items[0], j.Items[1], j.Items[2]

	var result struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(a.Result, &result); a.Status != job.ItemSucceeded || err != nil || result.Name != "a" {
		t.Errorf("a: %+v (%v)", a, err)
	}

	if b.Status != job.ItemFailed || !b.Retryable || b.Error != "vm b is locked with status starting" {
		t.Errorf("b: %+v", b)
	}

	if c.Status != job.ItemSkipped || c.Error != "you don't have permission" {
		t.Errorf("c: %+v", c)
	}

	if fmt.Sprint(*acted) != "[a]" {
		t.Errorf("paused %v, want only a", *acted)
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
			t.Errorf("%s: status %d, want %d: %s", name, resp.StatusCode, tc.want, readBody(t, resp))
		}
	}

	if resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/actions", "{"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed body: %d", resp.StatusCode)
	}

	if jobs := registry.List(job.Filter{}); len(jobs) != 0 {
		t.Errorf("refused requests started %d jobs", len(jobs))
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
		t.Fatal(err)
	}

	if resp := do(t, http.MethodPost, server+"/api/v1/jobs/"+started.ID+"/cancel", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}

	close(block)

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled {
		t.Errorf("job %s, want canceled", j.Status)
	}

	succeeded, _, _, canceled := j.Counts()
	if succeeded != len(*acted) || succeeded > bulkVMWorkers || succeeded+canceled != len(names) {
		t.Errorf("%d succeeded and %d canceled after pausing %v", succeeded, canceled, *acted)
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
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"a"`) {
		t.Errorf("stop: %d %s", resp.StatusCode, body)
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
		if body := readBody(t, resp); resp.StatusCode != tc.want || body != tc.body {
			t.Errorf("%s: %d %q, want %d %q", name, resp.StatusCode, body, tc.want, tc.body)
		}
	}

	if err := cache.LockVMForStarting("exp", "a"); err != nil {
		t.Fatal(err)
	}

	resp = do(t, http.MethodPost, allowed+"/api/v1/experiments/exp/vms/a/stop", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusConflict || body != "vm a is locked with status starting\n" {
		t.Errorf("stop of a locked VM: %d %q", resp.StatusCode, body)
	}

	cache.UnlockVM("exp", "a")
}

// A VM of a stopped experiment cannot be killed.
func TestKillVMNeedsARunningExperiment(t *testing.T) {
	useVMExperiment(t, false, "a")

	acted := fakeVMAction(t, "kill", nil, nil)
	server := serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL

	resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest || body != "experiment not running\n" {
		t.Errorf("kill: %d %q", resp.StatusCode, body)
	}

	if len(*acted) != 0 {
		t.Errorf("killed %v", *acted)
	}
}
