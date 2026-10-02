package web

import (
	"io"
	"net/http"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	gproto "google.golang.org/protobuf/proto"

	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// experimentViewerRole may list and get the test experiment and list its VMs.
func experimentViewerRole() rbac.Role {
	return combinedRole(
		testRole("experiments", "list", "test-experiment"),
		testRole("experiments", "get", "test-experiment"),
		testRole("vms", "list", "test-experiment/*"),
	)
}

// getAPI gets url and decodes a 200 response into msg, returning the status.
func getAPI(t *testing.T, url string, msg gproto.Message) int {
	t.Helper()

	resp := do(t, http.MethodGet, url, "")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode == http.StatusOK {
		if err := protojson.Unmarshal(body, msg); err != nil {
			t.Fatalf("unmarshaling %s: %v: %s", url, err, body)
		}
	}

	return resp.StatusCode
}

// The experiment endpoints list the experiment's VMs as minimega reports them
// unless asked not to (vms=false), when the count comes from the topology and
// minimega is not asked. A screenshot needs the VMs, so it overrides
// vms=false; and while an experiment is starting, the list takes the VMs from
// the topology rather than wait on minimega.
func TestExperimentEndpointsListVMs(t *testing.T) {
	running := mm.VMs{{Name: "test-vm", Running: true, State: "RUNNING", Networks: []string{"EXP_1 (101)"}}}

	for _, tt := range []struct {
		name     string
		list     bool
		query    string
		vms      mm.VMs // as minimega reports them
		starting bool

		wantListings int32
		wantVMs      int
		wantState    string // of the VM listed, if one is
		wantCount    uint32
		wantPercent  float64
	}{
		{name: "experiment", vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "experiment without VMs", query: "?vms=false", vms: running, wantCount: 1},
		// minimega reports no VMs, so there are none to take screenshots of
		{name: "experiment screenshots", query: "?vms=false&screenshot=200", wantListings: 1},
		{name: "list", list: true, vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "list without VMs", list: true, query: "?vms=false", vms: running, wantCount: 1},
		{name: "list screenshots", list: true, query: "?vms=false&screenshot=200", wantListings: 1},
		// the VM as the topology configures it, with no state from minimega
		{
			name: "list while starting", list: true, vms: running, starting: true,
			wantVMs: 1, wantCount: 1, wantPercent: 0.25,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTestExperiment(t)

			fake := useTestMM(t, &testMM{vms: tt.vms})

			if tt.starting {
				if err := cache.LockExperimentForStarting("test-experiment"); err != nil {
					t.Fatal(err)
				}

				startProgress.Store("test-experiment", tt.wantPercent)

				t.Cleanup(func() {
					startProgress.Delete("test-experiment")
					cache.UnlockExperiment("test-experiment")
				})
			}

			var (
				server = serveAs(t, "alice", experimentViewerRole()).URL
				exp    = new(proto.Experiment)
			)

			if tt.list {
				var list proto.ExperimentList

				code := getAPI(t, server+"/api/v1/experiments"+tt.query, &list)
				if code != http.StatusOK || len(list.GetExperiments()) != 1 {
					t.Fatalf("got %d: %v", code, list.GetExperiments())
				}

				exp = list.GetExperiments()[0]
			} else if code := getAPI(t, server+"/api/v1/experiments/test-experiment"+tt.query, exp); code != http.StatusOK {
				t.Fatalf("got %d", code)
			}

			if n := fake.vmListings.Load(); n != tt.wantListings {
				t.Errorf("minimega listed VMs %d times, want %d", n, tt.wantListings)
			}

			if !exp.GetRunning() {
				t.Error("experiment not running")
			}

			if len(exp.GetVms()) != tt.wantVMs || exp.GetVmCount() != tt.wantCount {
				t.Fatalf("got %d VMs of %d, want %d of %d", len(exp.GetVms()), exp.GetVmCount(), tt.wantVMs, tt.wantCount)
			}

			if tt.wantVMs == 1 && exp.GetVms()[0].GetState() != tt.wantState {
				t.Errorf("VM state %q, want %q", exp.GetVms()[0].GetState(), tt.wantState)
			}

			if exp.GetPercent() != tt.wantPercent {
				t.Errorf("percent %v, want %v", exp.GetPercent(), tt.wantPercent)
			}
		})
	}
}

// Both REST lists of an experiment's VMs page them alike: the count is of
// every VM, not just the page's, and an invalid page is refused (web/util's
// ParsePage tests cover what is invalid).
func TestVMListPaging(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{Name: "test-vm", Running: true, State: "RUNNING"}}})

	var (
		server = serveAs(t, "alice", experimentViewerRole()).URL + "/api/v1/experiments/test-experiment"
		exp    proto.Experiment
		list   proto.VMList
	)

	for _, tt := range []struct {
		name string
		path string
		msg  gproto.Message
		page func() (vms, total int)
	}{
		{"experiment", "", &exp, func() (int, int) { return len(exp.GetVms()), int(exp.GetVmCount()) }},
		{"VM list", "/vms", &list, func() (int, int) { return len(list.GetVms()), int(list.GetTotal()) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if code := getAPI(t, server+tt.path+"?pageNum=2&perPage=1", tt.msg); code != http.StatusOK {
				t.Fatalf("second page: expected 200, got %d", code)
			}

			if vms, total := tt.page(); vms != 0 || total != 1 {
				t.Fatalf("expected an empty second page of 1 VM, got %d VMs of %d", vms, total)
			}

			if code := getAPI(t, server+tt.path+"?pageNum=0&perPage=10", tt.msg); code != http.StatusBadRequest {
				t.Errorf("page 0: expected 400, got %d", code)
			}
		})
	}
}
