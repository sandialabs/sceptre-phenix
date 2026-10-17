package web

import (
	"io"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		t.Fatalf("reading GET %s: %v", url, err)
	}

	if resp.StatusCode == http.StatusOK {
		if err := protojson.Unmarshal(body, msg); err != nil {
			t.Fatalf("GET %s = %s, not a %T: %v", url, body, msg, err)
		}
	}

	return resp.StatusCode
}

// Experiment endpoints list VMs as minimega reports them; vms=false skips minimega and counts from the topology.
// A screenshot overrides vms=false, and a starting experiment lists topology VMs without waiting on minimega.
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
					t.Fatalf("cache.LockExperimentForStarting(test-experiment) error = %v", err)
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
				path   = "/api/v1/experiments/test-experiment" + tt.query
			)

			if tt.list {
				var list proto.ExperimentList

				path = "/api/v1/experiments" + tt.query
				if code := getAPI(t, server+path, &list); code != http.StatusOK {
					t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
				}

				if n := len(list.GetExperiments()); n != 1 {
					t.Fatalf("GET %s listed %d experiments, want 1: %v", path, n, list.GetExperiments())
				}

				exp = list.GetExperiments()[0]
			} else if code := getAPI(t, server+path, exp); code != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
			}

			if n := fake.vmListings.Load(); n != tt.wantListings {
				t.Errorf("GET %s listed minimega's VMs %d times, want %d", path, n, tt.wantListings)
			}

			if !exp.GetRunning() {
				t.Errorf("GET %s running = false, want true", path)
			}

			if got := exp.GetVmCount(); got != tt.wantCount {
				t.Errorf("GET %s VM count = %d, want %d", path, got, tt.wantCount)
			}

			if got := len(exp.GetVms()); got != tt.wantVMs {
				t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.wantVMs)
			} else if tt.wantVMs == 1 {
				if got := exp.GetVms()[0].GetState(); got != tt.wantState {
					t.Errorf("GET %s VM state = %q, want %q", path, got, tt.wantState)
				}
			}

			if got := exp.GetPercent(); got != tt.wantPercent {
				t.Errorf("GET %s percent = %v, want %v", path, got, tt.wantPercent)
			}
		})
	}
}

// Both VM lists page alike: the count covers every VM, and an invalid page is a 400.
func TestVMListPaging(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{Name: "test-vm", Running: true, State: "RUNNING"}}})

	var (
		server = serveAs(t, "alice", experimentViewerRole()).URL
		exp    proto.Experiment
		list   proto.VMList
	)

	for _, tt := range []struct {
		name string
		path string
		msg  gproto.Message
		page func() (vms, total int)
	}{
		{
			name: "experiment",
			path: "/api/v1/experiments/test-experiment",
			msg:  &exp,
			page: func() (int, int) { return len(exp.GetVms()), int(exp.GetVmCount()) },
		},
		{
			name: "VM list",
			path: "/api/v1/experiments/test-experiment/vms",
			msg:  &list,
			page: func() (int, int) { return len(list.GetVms()), int(list.GetTotal()) },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			second := tt.path + "?pageNum=2&perPage=1"
			if code := getAPI(t, server+second, tt.msg); code != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d", second, code, http.StatusOK)
			}

			vms, total := tt.page()
			if vms != 0 {
				t.Errorf("GET %s listed %d VMs, want 0", second, vms)
			}

			if total != 1 {
				t.Errorf("GET %s total = %d, want 1", second, total)
			}

			invalid := tt.path + "?pageNum=0&perPage=10"
			if code := getAPI(t, server+invalid, tt.msg); code != http.StatusBadRequest {
				t.Errorf("GET %s = %d, want %d", invalid, code, http.StatusBadRequest)
			}
		})
	}
}

// Updating several VMs answers with the VMs it updated, ignoring the request's
// total, and leaves out a VM the role may not patch.
func TestUpdateVMsIgnoresTotal(t *testing.T) {
	node := func(hostname string) map[string]any {
		return map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": hostname, "do_not_boot": false},
			"hardware": map[string]any{"os_type": "linux", "drives": []map[string]any{{"image": "test.qc2"}}},
		}
	}

	useTestStore(t, namedExperiment(t, "stopped", "", node("vm-a"), node("vm-b")))

	server := serveAs(t, "alice", testRole("vms", "patch", "stopped/vm-a"))
	body := `{"total": 5, "vms": [{"name": "vm-b", "dnb": true}, {"name": "vm-a", "dnb": true}]}`

	resp := do(t, http.MethodPatch, server.URL+"/api/v1/experiments/stopped/vms", body)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading PATCH /api/v1/experiments/stopped/vms: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /api/v1/experiments/stopped/vms = %d, want %d: %s", resp.StatusCode, http.StatusOK, raw)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(raw, &list); err != nil {
		t.Fatalf("PATCH /api/v1/experiments/stopped/vms = %s, not a VM list: %v", raw, err)
	}

	type patched struct {
		Name      string
		DoNotBoot bool
	}

	got := make([]patched, 0, len(list.GetVms()))
	for _, vm := range list.GetVms() {
		got = append(got, patched{Name: vm.GetName(), DoNotBoot: vm.GetDoNotBoot()})
	}

	if diff := cmp.Diff([]patched{{Name: "vm-a", DoNotBoot: true}}, got); diff != "" {
		t.Errorf("PATCH /api/v1/experiments/stopped/vms %s VMs mismatch (-want +got):\n%s", body, diff)
	}

	if got := list.GetTotal(); got != 1 {
		t.Errorf("PATCH /api/v1/experiments/stopped/vms %s total = %d, want 1", body, got)
	}
}
