package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/job"
	"phenix/api/workflow"
	"phenix/store"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/mm"
	"phenix/web/weberror"
)

// loopImage writes a qcow2 image backed by itself, which qemu cannot open, and returns its path.
func loopImage(t *testing.T) string {
	t.Helper()

	image := t.TempDir() + "/loop.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{Backing: "loop.qc2"})

	return image
}

// snapshotVM is a topology node of VM name booting from a snapshot of image.
func snapshotVM(name, image string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name, "snapshot": true},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": image}}},
	}
}

// brokenDisksBody is a broken_disks Error body as a client reads it.
type brokenDisksBody struct {
	Message string          `json:"message"`
	Code    weberror.Code   `json:"code"`
	Details json.RawMessage `json:"details"`
}

// wantBrokenDisk checks that body is a broken_disks error with message, listing only VM vm booting from image.
func wantBrokenDisk(t *testing.T, raw, message, vm, image string) {
	t.Helper()

	var body brokenDisksBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decoding the error body %s: %v", raw, err)
	}

	if body.Message != message || body.Code != weberror.CodeBrokenDisks {
		t.Errorf("error message, code = %q, %q, want %q, %q", body.Message, body.Code, message, weberror.CodeBrokenDisks)
	}

	want := `{"disks":[{"vm":"` + vm + `","drive":"disk","image":"` + image + `","path":"` + image +
		`","missing":"","problem":"backing chain loops back to ` + image + `"}]}`
	if string(body.Details) != want {
		t.Errorf("error details =\n%s\nwant\n%s", body.Details, want)
	}
}

// A start refused for a broken disk answers 400 with the code broken_disks and the list of disks, as openapi.yml
// documents.
func TestStartAnswersBrokenDisks(t *testing.T) {
	image := loopImage(t)

	useTestStore(t, namedExperiment(t, "stopped", "", snapshotVM("vm1", image)))

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = launchMM{testMM: &testMM{vms: mm.VMs{}}} //nolint:reassign // install test double

	start := serveAs(t, "alice", testRole("experiments/start", "update", "stopped")).URL +
		"/api/v1/experiments/stopped/start"

	resp := do(t, http.MethodPost, start, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST start = %d, want %d: %s", resp.StatusCode, http.StatusBadRequest, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodPost, "/api/v1/experiments/stopped/start", resp)
	wantBrokenDisk(t, readBody(t, resp), "unable to start experiment stopped", "vm1", image)
}

// workflowLoopTopology is the stored topology topo with VM vm1 booting from image.
func workflowLoopTopology(image string) store.Config {
	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "topo"},
		Spec:     map[string]any{"nodes": []any{snapshotVM("vm1", image)}},
	}
}

// A dry run of a restart lists what the disk check of its start would find.
func TestApplyWorkflowDryRunWarnsOfBrokenDisks(t *testing.T) {
	image := loopImage(t)

	m := installValidationTestStore(t)
	expectWorkflowList(m, workflowTestExperiment("foo", "main", true))
	expectWorkflowRead(m, "topology/topo", workflowLoopTopology(image))

	req := applyWorkflowRequest(workflowTestRole(true), "dryRun=true", mimeYAML,
		strings.NewReader(workflowTestYAML("  topology: topo\n")))

	rec := serveApplyWorkflow(t, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var result workflow.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding the result %s: %v", rec.Body, err)
	}

	want := workflow.Result{
		Plan:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""},
		DryRun: true,
		Warnings: []string{
			"the start would fail: VM vm1: disk " + image + ": backing chain loops back to " + image,
		},
	}

	if diff := cmp.Diff(want, result); diff != "" {
		t.Errorf("dry run result mismatch (-want +got):\n%s", diff)
	}
}

// A restart whose start would fail on a broken disk is refused before the stop, so the experiment stays running:
// the mock store allows no read the stop would make.
func TestApplyWorkflowRestartChecksDisksBeforeStopping(t *testing.T) {
	image := loopImage(t)

	m := installValidationTestStore(t)
	expectWorkflowList(m, workflowTestExperiment("foo", "main", true))
	expectWorkflowRead(m, "topology/topo", workflowLoopTopology(image))

	req := applyWorkflowRequest(workflowTestRole(true), "", mimeYAML, strings.NewReader(workflowTestYAML("  topology: topo\n")))

	rec := serveApplyWorkflow(t, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("restart = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body)
	}

	wantBrokenDisk(t, rec.Body.String(), "unable to restart experiment foo, which is still running", "vm1", image)
}

// With a user app, which can change the disks before the start checks them again, the check before the stop only
// warns, and the restart goes on to stop the experiment.
func TestApplyWorkflowRestartWithUserAppStops(t *testing.T) {
	image := loopImage(t)

	foo := workflowTestExperiment("foo", "main", true)
	foo.Spec = map[string]any{"scenario": map[string]any{"apps": []any{map[string]any{"name": "soh"}}}}

	m := installValidationTestStore(t)
	expectWorkflowList(m, foo)
	expectWorkflowRead(m, "topology/topo", workflowLoopTopology(image))
	m.EXPECT().Get(gomock.Eq(workflowTestKey("experiment/foo"))).Return(errors.New("store is down"))

	req := applyWorkflowRequest(workflowTestRole(true), "", mimeYAML, strings.NewReader(workflowTestYAML("  topology: topo\n")))

	rec := serveApplyWorkflow(t, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unable to stop experiment foo") {
		t.Fatalf("restart = %d %s, want %d from the stop", rec.Code, rec.Body, http.StatusBadRequest)
	}
}

// A bulk start fails each VM with a broken disk at once, starts the others, and skips those the role may not start
// without checking them.
func TestRunVMActionsChecksDisks(t *testing.T) {
	image := loopImage(t)

	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z",
		snapshotVM("a", image), diskVM("b", "b.img"), snapshotVM("c", image)))
	useTestMM(t, new(testMM))

	var (
		registry = useJobs(t)
		acted    = fakeVMAction(t, "start", nil, nil)
		server   = serveAs(t, "alice", testRole("vms/start", "update", "exp/a", "exp/b")).URL
	)

	// the test minimega takes no screenshots
	start := vmActions["start"]
	screenshot := start.screenshot
	start.screenshot = ""

	t.Cleanup(func() { start.screenshot = screenshot })

	resp := postVMActions(t, server, "start", "a", "b", "c")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST start actions = %d, want %d: %s", resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding the job: %v", err)
	}

	j := waitJob(t, registry, started.ID)

	wantItems := []job.Item{
		{
			Name: "a", Status: job.ItemFailed,
			Error: "broken VM disk images: VM a: disk " + image + ": backing chain loops back to " + image,
		},
		{Name: "b", Status: job.ItemSucceeded},
		{Name: "c", Status: job.ItemSkipped, Error: job.ErrForbidden.Error()},
	}

	if diff := cmp.Diff(wantItems, j.Items, cmpopts.IgnoreFields(job.Item{}, "Result")); diff != "" {
		t.Errorf("job items mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"b"}, *acted); diff != "" {
		t.Errorf("started VMs mismatch (-want +got):\n%s", diff)
	}
}

// A bulk start checks only the VMs minimega launches again: one that is paused only resumes, with its images open.
func TestRunVMActionsChecksOnlyRelaunchedVMs(t *testing.T) {
	image := loopImage(t)

	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", snapshotVM("a", image), snapshotVM("b", image)))
	useTestMM(t, &testMM{states: map[string]string{"a": "PAUSED", "b": "QUIT"}})

	var (
		registry = useJobs(t)
		acted    = fakeVMAction(t, "start", nil, nil)
		server   = serveAs(t, "alice", testRole("vms/start", "update", "exp/a", "exp/b")).URL
	)

	start := vmActions["start"]
	screenshot := start.screenshot
	start.screenshot = ""

	t.Cleanup(func() { start.screenshot = screenshot })

	resp := postVMActions(t, server, "start", "a", "b")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST start actions = %d, want %d: %s", resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding the job: %v", err)
	}

	j := waitJob(t, registry, started.ID)

	wantItems := []job.Item{
		{Name: "a", Status: job.ItemSucceeded},
		{
			Name: "b", Status: job.ItemFailed,
			Error: "broken VM disk images: VM b: disk " + image + ": backing chain loops back to " + image,
		},
	}

	if diff := cmp.Diff(wantItems, j.Items, cmpopts.IgnoreFields(job.Item{}, "Result")); diff != "" {
		t.Errorf("job items mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"a"}, *acted); diff != "" {
		t.Errorf("started VMs mismatch (-want +got):\n%s", diff)
	}
}

// Only a VM that quit, failed or has no known state is launched again by a start or restart.
func TestRelaunched(t *testing.T) {
	t.Parallel()

	for state, want := range map[string]bool{
		"BUILDING": false, "RUNNING": false, "PAUSED": false, "QUIT": true, "ERROR": true, "": true,
	} {
		if got := relaunched(state); got != want {
			t.Errorf("relaunched(%q) = %t, want %t", state, got, want)
		}
	}
}
