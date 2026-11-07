package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/api/job"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/web/cache"
	"phenix/web/util"
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

// A bulk action's VM fails with minimega's error when its host reports one,
// and when minimega goes away partway through the reply; the other VMs go on.
func TestRunVMActionsMinimegaFailures(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z",
		diskVM("a", "a.qc2"), diskVM("b", "a.qc2"), diskVM("c", "a.qc2")))

	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "vm stop b":
			return []*minicli.Response{mmtest.Error("compute1", "vm b is busy")}
		case "vm stop c":
			return mmtest.Truncated()
		}

		return nil
	})

	var (
		registry = useJobs(t)
		server   = serveAs(t, "alice", testRole("vms/stop", "update", "exp/*")).URL
	)

	resp := postVMActions(t, server, "pause", "a", "b", "c")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST vms/actions = %d, want %d: %s", resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding POST vms/actions: %v", err)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusPartial {
		t.Errorf("job %s status = %s, want %s", j.ID, j.Status, job.StatusPartial)
	}

	type outcome struct {
		Status job.ItemStatus
		Error  string // the part of the item's error that must appear
	}

	wantErrs := map[string]string{"b": "vm b is busy", "c": "server disconnected"}
	got := make(map[string]outcome, len(j.Items))

	for _, item := range j.Items {
		o := outcome{Status: item.Status, Error: ""}
		if want := wantErrs[item.Name]; want != "" && strings.Contains(item.Error, want) {
			o.Error = want
		} else if item.Error != "" {
			o.Error = item.Error
		}

		got[item.Name] = o
	}

	want := map[string]outcome{
		"a": {Status: job.ItemSucceeded, Error: ""},
		"b": {Status: job.ItemFailed, Error: "vm b is busy"},
		"c": {Status: job.ItemFailed, Error: "server disconnected"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("job %s items mismatch (-want +got):\n%s", j.ID, diff)
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
		"stop failing":   {allowed, http.MethodPost, "/b/stop", http.StatusInternalServerError, "minimega says no"},
		"stop forbidden": {denied, http.MethodPost, "/a/stop", http.StatusForbidden, "forbidden"},
		"kill":           {allowed, http.MethodDelete, "/a", http.StatusNoContent, ""},
		"kill forbidden": {denied, http.MethodDelete, "/a", http.StatusForbidden, "forbidden"},
	} {
		resp := do(t, tc.method, tc.server+"/api/v1/experiments/exp/vms"+tc.path, "")
		if resp.StatusCode != tc.want {
			t.Errorf("%s vms%s (%s) = %d, want %d", tc.method, tc.path, name, resp.StatusCode, tc.want)
		}

		body := readBody(t, resp)
		if resp.StatusCode >= http.StatusBadRequest {
			body = errorMessage(t, resp.StatusCode, body)
		}

		if body != tc.body {
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
	if got, want := errorMessage(t, resp.StatusCode, readBody(t, resp)), locked.Error(); got != want {
		t.Errorf("POST vms/a/stop of a locked VM = %q, want %q", got, want)
	}
}

// A VM of a stopped experiment cannot be killed.
func TestKillVMNeedsARunningExperiment(t *testing.T) {
	useVMExperiment(t, false, "a")

	acted := fakeVMAction(t, "kill", nil, nil)
	server := serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL

	resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a", "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("DELETE vms/a of a stopped experiment = %d, want %d", resp.StatusCode, http.StatusConflict)
	}

	if got, want := errorMessage(t, resp.StatusCode, readBody(t, resp)), "experiment exp is not running"; got != want {
		t.Errorf("DELETE vms/a of a stopped experiment = %q, want %q", got, want)
	}

	if len(*acted) != 0 {
		t.Errorf("DELETE vms/a of a stopped experiment killed %v, want none", *acted)
	}
}

// genVMActionsNames draws VM names from a small set, with empty ones, so most lists repeat a name.
func genVMActionsNames() *rapid.Generator[[]string] {
	return rapid.SliceOf(rapid.SampledFrom([]string{"", "a", "b", "c", "vm-1", "vm-2"}))
}

// A bulk action's VMs are the request's names, each once, in the order first
// named, without empty names.
func TestUniqueNamesProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		in := genVMActionsNames().Draw(t, "in")
		before := slices.Clone(in)
		got := uniqueNames(in)

		if diff := cmp.Diff(before, in); diff != "" {
			t.Fatalf("input after uniqueNames mismatch (-want +got):\n%s", diff)
		}

		last := -1

		for i, name := range got {
			if name == "" || slices.Contains(got[:i], name) {
				t.Fatalf("uniqueNames(%q) = %q, has %q empty or repeated", in, got, name)
			}

			first := slices.Index(in, name)
			if first <= last {
				t.Fatalf("uniqueNames(%q) = %q, %q is out of first-named order", in, got, name)
			}

			last = first
		}

		for _, name := range in {
			if name != "" && !slices.Contains(got, name) {
				t.Fatalf("uniqueNames(%q) = %q, missing %q", in, got, name)
			}
		}

		if again := uniqueNames(got); !slices.Equal(again, got) {
			t.Fatalf("uniqueNames(uniqueNames(%q)) = %q, want %q", in, again, got)
		}
	})
}

// vmActionsDo makes the named action do what do does until the test ends.
func vmActionsDo(t *testing.T, action string, do func(ctx context.Context, exp, name string) error) {
	t.Helper()

	a := vmActions[action]
	original := a.do
	a.do = do

	t.Cleanup(func() { a.do = original })
}

// vmActionsNoScreenshot has a start take no screenshot until the test ends:
// the test minimega takes none.
func vmActionsNoScreenshot(t *testing.T) {
	t.Helper()

	start := vmActions["start"]
	screenshot := start.screenshot
	start.screenshot = ""

	t.Cleanup(func() { start.screenshot = screenshot })
}

// vmActionsJob starts a bulk action of exp's VMs and returns the job once it
// has finished.
func vmActionsJob(t *testing.T, registry *job.Registry, server, exp, action string, vms ...string) job.Job {
	t.Helper()

	body, err := json.Marshal(vmActionsRequest{Action: action, VMs: vms})
	if err != nil {
		t.Fatalf("json.Marshal(%s request) error = %v", action, err)
	}

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/"+exp+"/vms/actions", string(body))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s actions = %d, want %d: %s", action, resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding the job: %v", err)
	}

	return waitJob(t, registry, started.ID)
}

// A VM's screenshot is a PNG data URL, or "" when it cannot be taken.
func TestVMScreenshot(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG vm_actions_test")

	width, err := util.ScreenshotWidth(defaultScreenshotSize)
	if err != nil {
		t.Fatalf("util.ScreenshotWidth(%s) error = %v", defaultScreenshotSize, err)
	}

	// what GetScreenshotContext caches it as, so minimega is not asked
	if err := cache.SetWithExpire("vmActionsShot_a_"+width, png, time.Minute); err != nil {
		t.Fatalf("cache.SetWithExpire() error = %v", err)
	}

	tests := map[string]struct {
		size, want string
	}{
		"cached":       {defaultScreenshotSize, "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)},
		"invalid size": {"huge", ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := vmScreenshot(t.Context(), "vmActionsShot", "a", tc.size); got != tc.want {
				t.Errorf("vmScreenshot(size %s) = %q, want %q", tc.size, got, tc.want)
			}
		})
	}
}

// A kill sends minimega `vm kill` for the VM, in the experiment's namespace.
func TestKillVMKillsInMinimega(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

	received := useMinimega(t, func(mmtest.Command) []*minicli.Response { return nil })
	server := serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL

	resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE vms/a = %d, want %d: %s", resp.StatusCode, http.StatusNoContent, readBody(t, resp))
	}

	var kills []mmtest.Command

	for _, cmd := range received() {
		if strings.HasPrefix(cmd.Base, "vm kill") {
			kills = append(kills, cmd)
		}
	}

	if len(kills) != 1 || kills[0].Base != "vm kill a" || kills[0].Namespace != "exp" {
		t.Errorf("minimega kill commands = %+v, want one vm kill a in namespace exp", kills)
	}
}

// A single-VM action that fails after acting answers why, and broadcasts the
// failure; an action on a VM or in an experiment that does not exist touches
// no VM and broadcasts nothing.
func TestSingleVMActionFailures(t *testing.T) {
	tests := map[string]struct {
		method, path string
		do           func(ctx context.Context, exp, name string) error
		action       string
		want         int
		broadcasts   []string
	}{
		"VM not in the topology": {
			method: http.MethodPost, path: "/experiments/exp/vms/ghost/stop", action: "pause",
			want: http.StatusNotFound,
		},
		"experiment gone after acting": {
			method: http.MethodPost, path: "/experiments/exp/vms/a/stop", action: "pause",
			do: func(context.Context, string, string) error {
				c, _ := store.NewConfig("experiment/exp")

				return store.Delete(c) //nolint:wrapcheck // test double
			},
			want: http.StatusBadRequest, broadcasts: []string{"stopping", "errorStopping"},
		},
		"kill in a missing experiment": {
			method: http.MethodDelete, path: "/experiments/none/vms/a", action: "kill", want: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useVMExperiment(t, true, "a")

			if tc.do == nil {
				tc.do = func(context.Context, string, string) error { return nil }
			}

			vmActionsDo(t, tc.action, tc.do)

			broadcasts := recordVMBroadcasts(t)
			events := captureAudit(t)
			server := serveAs(t, "alice", adminRole()).URL

			resp := do(t, tc.method, server+"/api/v1"+tc.path, "")
			if body := readBody(t, resp); resp.StatusCode != tc.want {
				t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, resp.StatusCode, tc.want, body)
			}

			if diff := cmp.Diff(tc.broadcasts, broadcasts(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s %s broadcasts mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}

			if tc.action == "kill" {
				wantAudit(t, events(), "vm-kill", "failure", map[string]any{"user.name": "alice"})
			}
		})
	}
}

// A shut down VM is answered as stopped though minimega has yet to say so;
// a paused one is answered as minimega reports it.
func TestSingleVMActionReportsStopped(t *testing.T) {
	for action, want := range map[string]bool{"shutdown": false, "stop": true} {
		t.Run(action, func(t *testing.T) {
			useVMExperiment(t, true, "a")
			useTestMM(t, &testMM{vms: mm.VMs{{Name: "a", Running: true}}})

			name := action
			if action == "stop" {
				name = "pause"
			}

			vmActionsDo(t, name, func(context.Context, string, string) error { return nil })

			server := serveAs(t, "alice", adminRole()).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/a/"+action, "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST vms/a/%s = %d, want %d: %s", action, resp.StatusCode, http.StatusOK, readBody(t, resp))
			}

			var got struct {
				Running bool `json:"running"`
			}

			if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
				t.Fatalf("decoding POST vms/a/%s: %v", action, err)
			}

			if got.Running != want {
				t.Errorf("POST vms/a/%s running = %t, want %t", action, got.Running, want)
			}
		})
	}
}

// A bulk action that ends without a VM (a kill) succeeds with no result.
func TestRunVMActionsWithoutResult(t *testing.T) {
	useVMExperiment(t, true, "a", "b")

	var (
		registry = useJobs(t)
		acted    = fakeVMAction(t, "kill", nil, nil)
		server   = serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL
	)

	j := vmActionsJob(t, registry, server, "exp", "kill", "a", "b")

	want := []job.Item{{Name: "a", Status: job.ItemSucceeded}, {Name: "b", Status: job.ItemSucceeded}}
	if diff := cmp.Diff(want, j.Items); diff != "" {
		t.Errorf("job items mismatch (-want +got):\n%s", diff)
	}

	if got := slices.Sorted(slices.Values(*acted)); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("killed VMs = %v, want [a b]", got)
	}
}

// A bulk start checks no disks when it cannot read the experiment, or when
// every VM it may start is running; it then acts on every VM of an
// experiment that exists.
func TestRunVMActionsSkipsTheDiskCheck(t *testing.T) {
	image := loopImage(t)

	tests := map[string]struct {
		exp    string
		states map[string]string
		want   []job.Item
		acted  []string
	}{
		"experiment missing": {
			exp: "none",
			// the action itself then fails reading the experiment
			want: []job.Item{{Name: "a", Status: job.ItemFailed}},
		},
		"every VM running": {
			exp: "exp", states: map[string]string{"a": "RUNNING"},
			want:  []job.Item{{Name: "a", Status: job.ItemSucceeded}},
			acted: []string{"a"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", snapshotVM("a", image)))
			useTestMM(t, &testMM{states: tc.states})
			useDiskCheck(t, common.DiskCheckError)
			vmActionsNoScreenshot(t)

			var (
				registry = useJobs(t)
				acted    = fakeVMAction(t, "start", nil, nil)
				server   = serveAs(t, "alice", testRole("vms/start", "update", tc.exp+"/a")).URL
			)

			j := vmActionsJob(t, registry, server, tc.exp, "start", "a")

			if diff := cmp.Diff(tc.want, j.Items, cmpopts.IgnoreFields(job.Item{}, "Result", "Error")); diff != "" {
				t.Errorf("job items mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.acted, *acted, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("started VMs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A vmActionError reads as its cause, which [errors.Is] finds.
func TestVMActionError(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("locking: %w", job.ErrForbidden)
	err := error(&vmActionError{status: http.StatusConflict, err: cause})

	if err.Error() != cause.Error() || !errors.Is(err, job.ErrForbidden) {
		t.Errorf("vmActionError = %q (is ErrForbidden: %t), want %q and true", err, errors.Is(err, job.ErrForbidden), cause)
	}
}

// A request's log fields name its user, experiment and VM, and its job when it
// has one.
func TestVMRequestLogArgs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		req  vmRequest
		want []any
	}{
		"single VM": {vmRequest{user: "alice", exp: "exp", name: "a"}, []any{"user", "alice", "exp", "exp", "vm", "a"}},
		"job": {
			vmRequest{user: "alice", exp: "exp", name: "a", jobID: "j1"},
			[]any{"user", "alice", "exp", "exp", "vm", "a", "job", "j1"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, tc.req.logArgs()); diff != "" {
				t.Errorf("logArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A start of a VM that minimega does not know answers 409 with how to launch
// it when the topology sets the VM to not boot, and 404 otherwise. Both fail
// the start: they audit it and broadcast the failure.
func TestStartVMNotLaunched(t *testing.T) {
	const notLaunched = "VM off is set to not boot, so minimega did not launch it. Redeploy the VM to launch it"

	tests := map[string]struct {
		vm   string
		want int
		body string // the error message, or the part of it that must appear
	}{
		"set to not boot": {vm: "off", want: http.StatusConflict, body: notLaunched},
		"set to boot":     {vm: "on", want: http.StatusNotFound, body: "VM on not found in experiment exp"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			off := diskVM("off", "a.qc2")
			off["general"] = map[string]any{"hostname": "off", "do_not_boot": true}

			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", off, diskVM("on", "a.qc2")))

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if vm, ok := strings.CutPrefix(cmd.Base, "vm start "); ok {
					return []*minicli.Response{mmtest.Error("compute1", "vm not found: "+vm)}
				}

				return nil
			})

			broadcasts := recordVMBroadcasts(t)
			events := captureAudit(t)
			server := serveAs(t, "alice", testRole("vms/start", "update", "exp/*")).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/"+tc.vm+"/start", "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("POST vms/%s/start = %d, want %d: %s", tc.vm, resp.StatusCode, tc.want, body)
			}

			if got := errorMessage(t, resp.StatusCode, body); !strings.Contains(got, tc.body) {
				t.Errorf("POST vms/%s/start message = %q, want it to contain %q", tc.vm, got, tc.body)
			}

			if got := mmtest.Count(received(), "vm start "+tc.vm); got != 1 {
				t.Errorf("minimega got vm start %s %d times, want 1", tc.vm, got)
			}

			if diff := cmp.Diff([]string{"starting", "errorStarting"}, broadcasts()); diff != "" {
				t.Errorf("POST vms/%s/start broadcasts mismatch (-want +got):\n%s", tc.vm, diff)
			}

			wantAudit(t, events(), "vm-start", "failure", map[string]any{"user.name": "alice"})
		})
	}
}

// startVM gives minimega's error as it is for a VM it does not know when the
// experiment is not in the store, and for a VM the topology does not have.
func TestStartVMKeepsOtherErrors(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Error("compute1", "vm not found: "+strings.TrimPrefix(cmd.Base, "vm start "))}
	})

	for _, tc := range []struct{ exp, vm string }{{exp: "none", vm: "a"}, {exp: "exp", vm: "ghost"}} {
		err := startVM(t.Context(), tc.exp, tc.vm)

		var actionErr *vmActionError
		if err == nil || errors.As(err, &actionErr) || !strings.Contains(err.Error(), "vm not found: "+tc.vm) {
			t.Errorf("startVM(%s, %s) error = %v, want minimega's vm not found error", tc.exp, tc.vm, err)
		}
	}
}
