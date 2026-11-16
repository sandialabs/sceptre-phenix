package web

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm"
	"phenix/web/rbac"
)

// vlanCaptureMM is testMM that also starts captures.
type vlanCaptureMM struct {
	*testMM

	captureStarts atomic.Int32
}

func (m *vlanCaptureMM) StartVMCapture(...mm.Option) error {
	m.captureStarts.Add(1)

	return nil
}

// captureNode is a topology node, name, with IF0 on EXP_1 and IF1 on EXP_2.
func captureNode(name string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name},
		"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux"},
		"network": map[string]any{"interfaces": []map[string]any{
			{"name": "IF0", "vlan": "EXP_1"},
			{"name": "IF1", "vlan": "EXP_2"},
		}},
	}
}

// useVLANCaptureVMs stores test-experiment, running unless stopped, with VMs a and b that minimega runs, each with a
// capture on its EXP_2 interface, and returns minimega.
func useVLANCaptureVMs(t *testing.T, stopped bool) *vlanCaptureMM {
	t.Helper()

	started := "2024-01-01T00:00:00Z"
	if stopped {
		started = ""
	}

	useTestStore(t, testExperiment(t, started, captureNode("a"), captureNode("b")))

	captures := []mm.Capture{
		{VM: "a", Interface: 1, Filepath: "/files/a1.pcap"},
		{VM: "b", Interface: 1, Filepath: "/files/b1.pcap"},
	}

	running := func(name string, capture mm.Capture) mm.VM {
		return mm.VM{
			Name: name, State: "RUNNING", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"},
			Captures: []mm.Capture{capture},
		}
	}

	fake := &vlanCaptureMM{testMM: &testMM{
		vms:      mm.VMs{running("a", captures[0]), running("b", captures[1])},
		captures: captures,
	}}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// vlanCaptureBody is the body of a VLAN capture response: a capture list or a VM name list.
type vlanCaptureBody struct {
	Captures []struct {
		VM        string `json:"vm"`
		Interface int    `json:"interface"`
		Filepath  string `json:"filepath"`
	} `json:"captures"`
	VMs []string `json:"vms"`
}

func TestCaptureVLANRoutes(t *testing.T) {
	const (
		start = "/api/v1/experiments/test-experiment/captureVlan"
		stop  = "/api/v1/experiments/test-experiment/stopCaptureVlan"
	)

	var (
		creator = testRole("vms/captures", "create", "test-experiment/*")
		deleter = testRole("vms/captures", "delete", "test-experiment/*")
	)

	type capture = struct {
		VM        string `json:"vm"`
		Interface int    `json:"interface"`
		Filepath  string `json:"filepath"`
	}

	tests := map[string]struct {
		path      string
		body      string
		role      rbac.Role
		stopped   bool
		want      int
		wantBody  vlanCaptureBody
		wantCalls int32 // capture starts or stops
		wantAudit string
	}{
		"start on every VM's VLAN interface": {
			path: start, body: `{"vlan":"EXP_2"}`, role: creator, want: http.StatusOK, wantCalls: 2,
			wantBody: vlanCaptureBody{Captures: []capture{
				{VM: "a", Interface: 1, Filepath: "/files/a1.pcap"},
				{VM: "b", Interface: 1, Filepath: "/files/b1.pcap"},
			}},
			wantAudit: "success",
		},
		"start on the VMs it names": {
			path: start, body: `{"vlan":"EXP_2","vms":["b"]}`, role: creator, want: http.StatusOK, wantCalls: 1,
			wantBody:  vlanCaptureBody{Captures: []capture{{VM: "b", Interface: 1, Filepath: "/files/b1.pcap"}}},
			wantAudit: "success",
		},
		"start only on the VMs the role may capture": {
			path: start, body: `{"vlan":"EXP_2"}`, role: testRole("vms/captures", "create", "test-experiment/a"),
			want: http.StatusOK, wantCalls: 1,
			wantBody:  vlanCaptureBody{Captures: []capture{{VM: "a", Interface: 1, Filepath: "/files/a1.pcap"}}},
			wantAudit: "success",
		},
		"start refused for VMs it names that the role may not capture": {
			path: start, body: `{"vlan":"EXP_2","vms":["b"]}`, role: testRole("vms/captures", "create", "test-experiment/a"),
			want: http.StatusForbidden, wantAudit: "failure",
		},
		"start refused for a role that may capture no VM": {
			path: start, body: `{"vlan":"EXP_2"}`, role: deleter, want: http.StatusForbidden, wantAudit: "failure",
		},
		"start refused for a role that may capture no VM of the experiment": {
			path: start, body: `{"vlan":"EXP_2"}`, role: testRole("vms/captures", "create", "test-experiment/c"),
			want: http.StatusForbidden, wantAudit: "failure",
		},
		"start on the VMs a filter selects": {
			path: start, body: `{"vlan":"EXP_2","filter":"b"}`, role: creator, want: http.StatusOK, wantCalls: 1,
			wantBody:  vlanCaptureBody{Captures: []capture{{VM: "b", Interface: 1, Filepath: "/files/b1.pcap"}}},
			wantAudit: "success",
		},
		"start only on the filtered VMs the role may capture": {
			path: start, body: `{"vlan":"EXP_2","filter":"b"}`,
			role: testRole("vms/captures", "create", "test-experiment/a"), want: http.StatusForbidden,
		},
		"start refused for a filter that matches no VM": {
			path: start, body: `{"vlan":"EXP_2","filter":"zzz"}`, role: creator, want: http.StatusBadRequest,
		},
		"start refused for VMs and a filter": {
			path: start, body: `{"vlan":"EXP_2","vms":["a"],"filter":"b"}`, role: creator, want: http.StatusBadRequest,
		},
		"stop on the VMs a filter selects": {
			path: stop, body: `{"vlan":"EXP_2","filter":"a"}`, role: deleter, want: http.StatusOK, wantCalls: 1,
			wantBody: vlanCaptureBody{VMs: []string{"a"}}, wantAudit: "success",
		},
		"start without a VLAN": {path: start, body: `{"vms":["a"]}`, role: creator, want: http.StatusBadRequest},
		"start in an unknown format": {
			path: start, body: `{"vlan":"EXP_2","format":"erf"}`, role: creator, want: http.StatusBadRequest,
			wantAudit: "failure",
		},
		"start with a body that does not decode": {path: start, body: `{`, role: creator, want: http.StatusBadRequest},
		"start of a stopped experiment": {
			path: start, body: `{"vlan":"EXP_2"}`, role: creator, stopped: true,
			want: http.StatusInternalServerError, wantAudit: "failure",
		},
		"stop only the VLAN's captures": {
			path: stop, body: `{"vlan":"EXP_2"}`, role: deleter, want: http.StatusOK, wantCalls: 2,
			wantBody: vlanCaptureBody{VMs: []string{"a", "b"}}, wantAudit: "success",
		},
		"stop nothing on a VLAN without captures": {
			path: stop, body: `{"vlan":"EXP_1"}`, role: deleter, want: http.StatusOK, wantBody: vlanCaptureBody{VMs: []string{}},
		},
		"stop only on the VMs the role may capture": {
			path: stop, body: `{"vlan":"EXP_2"}`, role: testRole("vms/captures", "delete", "test-experiment/b"),
			want: http.StatusOK, wantCalls: 1, wantBody: vlanCaptureBody{VMs: []string{"b"}}, wantAudit: "success",
		},
		"stop refused for a role that may capture no VM": {
			path: stop, body: `{"vlan":"EXP_2"}`, role: creator, want: http.StatusForbidden, wantAudit: "failure",
		},
		"stop without a VLAN": {path: stop, body: `{}`, role: deleter, want: http.StatusBadRequest},
		"stop of a stopped experiment": {
			path: stop, body: `{"vlan":"EXP_2"}`, role: deleter, stopped: true,
			want: http.StatusInternalServerError, wantAudit: "failure",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := useVLANCaptureVMs(t, tc.stopped)
			server := serveAs(t, "alice", tc.role).URL
			events := captureAudit(t)

			resp := do(t, http.MethodPost, server+tc.path, tc.body)
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("POST %s %s = %d, want %d: %s", tc.path, tc.body, resp.StatusCode, tc.want, body)
			}

			if calls := fake.captureStarts.Load() + fake.captureStops.Load(); calls != tc.wantCalls {
				t.Errorf("POST %s %s started or stopped %d captures, want %d", tc.path, tc.body, calls, tc.wantCalls)
			}

			if tc.wantAudit != "" {
				action := "capture-start"
				if tc.path == stop {
					action = "capture-stop"
				}

				wantAudit(t, events(), action, tc.wantAudit, map[string]any{"phenix.experiment.name": "test-experiment"})
			}

			if tc.want != http.StatusOK {
				return
			}

			var got vlanCaptureBody
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("POST %s %s = %s, not JSON: %v", tc.path, tc.body, body, err)
			}

			if diff := cmp.Diff(tc.wantBody, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("POST %s %s body mismatch (-want +got):\n%s", tc.path, tc.body, diff)
			}
		})
	}
}

// A VLAN capture of an experiment that does not exist is 404 for a role that may change the captures of all its VMs,
// and 403 for other roles, so that the status does not tell them which experiments exist.
func TestCaptureVLANMissingExperiment(t *testing.T) {
	const (
		start = "/api/v1/experiments/" + contractOther + "/captureVlan"
		stop  = "/api/v1/experiments/" + contractOther + "/stopCaptureVlan"
	)

	tests := map[string]struct {
		path      string
		role      rbac.Role
		want      int
		wantAudit string
	}{
		"start with a role for every experiment": {
			path: start, role: testRole("vms/captures", "create", "*/*"), want: http.StatusNotFound,
		},
		"start with a role for every VM of the experiment": {
			path: start, role: testRole("vms/captures", "create", contractOther+"/*"), want: http.StatusNotFound,
		},
		"stop with a role for every experiment": {
			path: stop, role: testRole("vms/captures", "delete", "*/*"), want: http.StatusNotFound,
		},
		"start with no capture permission": {
			path: start, role: testRole("vms", "list", "*/*"), want: http.StatusForbidden, wantAudit: "capture-start",
		},
		"start with only the stop permission": {
			path: start, role: testRole("vms/captures", "delete", "*/*"), want: http.StatusForbidden, wantAudit: "capture-start",
		},
		"stop with no capture permission": {
			path: stop, role: testRole("vms/captures", "create", "*/*"), want: http.StatusForbidden, wantAudit: "capture-stop",
		},
		"start with a role for another experiment": {
			path: start, role: testRole("vms/captures", "create", "test-experiment/*"),
			want: http.StatusForbidden, wantAudit: "capture-start",
		},
		"start with a role for one VM of the experiment": {
			path: start, role: testRole("vms/captures", "create", contractOther+"/a"),
			want: http.StatusForbidden, wantAudit: "capture-start",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useVLANCaptureVMs(t, false)

			server := serveAs(t, "alice", tc.role).URL
			events := captureAudit(t)

			resp := do(t, http.MethodPost, server+tc.path, `{"vlan":"EXP_2"}`)
			if resp.StatusCode != tc.want {
				t.Fatalf("POST %s = %d, want %d: %s", tc.path, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if tc.wantAudit != "" {
				wantAudit(t, events(), tc.wantAudit, "failure", map[string]any{"phenix.experiment.name": contractOther})
			}
		})
	}
}

// The subnet capture routes take a filter in place of a VM list, and refuse a
// request that gives both, or a filter that matches no VM.
func TestCaptureSubnetFilter(t *testing.T) {
	const (
		start = "/api/v1/experiments/test-experiment/captureSubnet"
		stop  = "/api/v1/experiments/test-experiment/stopCaptureSubnet"
	)

	tests := map[string]struct {
		path string
		body string
		want int
	}{
		"start, filter matches no VM": {path: start, body: `{"subnet":"10.0.0.0/24","filter":"zzz"}`, want: http.StatusBadRequest},
		"start, VMs and a filter": {
			path: start,
			body: `{"subnet":"10.0.0.0/24","vms":["a"],"filter":"a"}`,
			want: http.StatusBadRequest,
		},
		"stop, filter matches no VM": {path: stop, body: `{"subnet":"10.0.0.0/24","filter":"zzz"}`, want: http.StatusBadRequest},
		"stop, VMs and a filter": {
			path: stop,
			body: `{"subnet":"10.0.0.0/24","vms":["a"],"filter":"a"}`,
			want: http.StatusBadRequest,
		},
		"stop, the filter selects a VM": {path: stop, body: `{"subnet":"10.0.0.0/24","filter":"a"}`, want: http.StatusOK},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useVLANCaptureVMs(t, false)

			verb := "delete"
			if tc.path == start {
				verb = "create"
			}

			role := testRole("exp/captureSubnet", verb, "test-experiment")

			server := serveAs(t, "alice", role).URL

			resp := do(t, http.MethodPost, server+tc.path, tc.body)
			if resp.StatusCode != tc.want {
				t.Errorf("POST %s %s = %d, want %d: %s", tc.path, tc.body, resp.StatusCode, tc.want, readBody(t, resp))
			}
		})
	}
}
