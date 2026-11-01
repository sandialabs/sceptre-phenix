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

// Experiment endpoints list VMs as minimega reports them; vms=false counts from the topology instead,
// except for one experiment's screenshots. A starting experiment lists topology VMs without waiting on minimega.
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
	}{
		{name: "experiment", vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "experiment without VMs", query: "?vms=false", vms: running, wantCount: 1},
		// minimega reports no VMs, so there are none to take screenshots of
		{name: "experiment screenshots", query: "?vms=false&screenshot=200", wantListings: 1},
		{name: "list", list: true, vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "list without VMs", list: true, query: "?vms=false", vms: running, wantCount: 1},
		// the list takes no screenshots, so one asked for changes nothing
		{name: "list ignores screenshots", list: true, query: "?vms=false&screenshot=200", vms: running, wantCount: 1},
		// the VM as the topology configures it, with no state from minimega
		{name: "list while starting", list: true, vms: running, starting: true, wantVMs: 1, wantCount: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTestExperiment(t)

			fake := useTestMM(t, &testMM{vms: tt.vms})

			if tt.starting {
				if err := cache.LockExperimentForStarting("test-experiment"); err != nil {
					t.Fatalf("cache.LockExperimentForStarting(test-experiment) error = %v", err)
				}

				t.Cleanup(func() { cache.UnlockExperiment("test-experiment") })
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

// The experiment's VMs filter by label, which must be key=value, and the VM
// list offers the labels to filter by.
func TestVMListLabels(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{
		Name: "test-vm", Running: true, State: "RUNNING",
		Tags: map[string]string{"role": "web", "__notes_1": "a note"},
	}}})

	server := serveAs(t, "alice", experimentViewerRole()).URL

	for _, tt := range []struct {
		query string
		code  int
		vms   int
	}{
		{"?label=role=web", http.StatusOK, 1},
		{"?label=role=db&label=role=web", http.StatusOK, 1},
		{"?label=role=db", http.StatusOK, 0},
		{"?label=__notes_1=a+note", http.StatusOK, 0},
		{"?label=role", http.StatusBadRequest, 0},
	} {
		var exp proto.Experiment

		path := "/api/v1/experiments/test-experiment" + tt.query
		if code := getAPI(t, server+path, &exp); code != tt.code {
			t.Errorf("GET %s = %d, want %d", path, code, tt.code)

			continue
		}

		if got := len(exp.GetVms()); got != tt.vms {
			t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.vms)
		}
	}

	var list proto.VMList

	path := "/api/v1/experiments/test-experiment/vms"
	if code := getAPI(t, server+path, &list); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
	}

	got := make([]string, 0, len(list.GetLabels()))
	for _, l := range list.GetLabels() {
		got = append(got, l.GetKey()+"="+l.GetValue())
	}

	if diff := cmp.Diff([]string{"role=web"}, got); diff != "" {
		t.Errorf("GET %s labels mismatch (-want +got):\n%s", path, diff)
	}
}

// The experiment's VMs filter by the cluster host running them, which must not
// be empty, and the VM list offers the hosts to filter by.
func TestVMListHosts(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{Name: "test-vm", Host: "node1", Running: true, State: "RUNNING"}}})

	server := serveAs(t, "alice", experimentViewerRole()).URL

	for _, tt := range []struct {
		query string
		code  int
		vms   int
	}{
		{"?host=node1", http.StatusOK, 1},
		{"?host=node2&host=node1", http.StatusOK, 1},
		{"?host=node2", http.StatusOK, 0},
		{"?host=", http.StatusBadRequest, 0},
	} {
		var exp proto.Experiment

		path := "/api/v1/experiments/test-experiment" + tt.query
		if code := getAPI(t, server+path, &exp); code != tt.code {
			t.Errorf("GET %s = %d, want %d", path, code, tt.code)

			continue
		}

		if got := len(exp.GetVms()); got != tt.vms {
			t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.vms)
		}
	}

	var list proto.VMList

	path := "/api/v1/experiments/test-experiment/vms"
	if code := getAPI(t, server+path, &list); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
	}

	if diff := cmp.Diff([]string{"node1"}, list.GetHosts()); diff != "" {
		t.Errorf("GET %s hosts mismatch (-want +got):\n%s", path, diff)
	}

	if got := list.GetRunning(); got != 1 {
		t.Errorf("GET %s running = %d, want 1", path, got)
	}
}
