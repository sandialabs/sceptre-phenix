package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "phenix/types/version/v1"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// useAnnotatedVMExperiment stores test-experiment, stopped, with one VM,
// test-vm, that has annotations.
func useAnnotatedVMExperiment(t *testing.T) {
	t.Helper()

	useTestStore(t, testExperiment(t, "", map[string]any{
		"type":        "VirtualMachine",
		"general":     map[string]any{"hostname": "test-vm"},
		"hardware":    map[string]any{"vcpus": 1, "memory": 256, "os_type": "linux"},
		"annotations": map[string]any{"vncBanner": "hello"},
	}))
}

func vmRole(verbs ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{
				Resources:     []string{"vms"},
				ResourceNames: []string{"test-experiment/*"},
				Verbs:         verbs,
			}},
		},
	}
}

func decodeVM(t *testing.T, resp *http.Response) *proto.VM {
	t.Helper()

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s = %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, http.StatusOK, body)
	}

	var vm proto.VM
	if err := protojson.Unmarshal([]byte(body), &vm); err != nil {
		t.Fatalf("%s %s = %q, not a VM: %v", resp.Request.Method, resp.Request.URL.Path, body, err)
	}

	return &vm
}

func patchVM(t *testing.T, role rbac.Role, body string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", role).URL

	return do(t, http.MethodPatch, server+"/api/v1/experiments/test-experiment/vms/test-vm", body)
}

// A single VM's GET carries its annotations; VM lists do not.
func TestGetVMCarriesAnnotations(t *testing.T) {
	useAnnotatedVMExperiment(t)

	server := serveAs(t, "alice", vmRole("get", "list")).URL + "/api/v1/experiments/test-experiment/vms"

	vm := decodeVM(t, do(t, http.MethodGet, server+"/test-vm", ""))
	if diff := cmp.Diff(map[string]any{"vncBanner": "hello"}, vm.GetAnnotations().AsMap()); diff != "" {
		t.Errorf("GET vms/test-vm annotations mismatch (-want +got):\n%s", diff)
	}

	resp := do(t, http.MethodGet, server, "")

	var list proto.VMList
	if err := protojson.Unmarshal([]byte(readBody(t, resp)), &list); err != nil {
		t.Fatalf("GET vms is not a VM list: %v", err)
	}

	if len(list.GetVms()) != 1 {
		t.Fatalf("GET vms = %v, want one VM", list.GetVms())
	}

	if got := list.GetVms()[0].GetAnnotations(); got != nil {
		t.Errorf("GET vms annotations = %v, want none", got)
	}
}

// forwardRole may create port forwards to every VM of test-experiment.
func forwardRole() rbac.Role {
	return testRole("vms/forwards", "create", "test-experiment/*")
}

func TestUpdateVMReplacesAnnotations(t *testing.T) {
	useAnnotatedVMExperiment(t)

	role := combinedRole(vmRole("get", "patch"), forwardRole())

	resp := patchVM(t, role, `{"annotations": {"phenix/startup-autotunnel": ["8080"]}}`)

	want := map[string]any{"phenix/startup-autotunnel": []any{"8080"}}
	if diff := cmp.Diff(want, decodeVM(t, resp).GetAnnotations().AsMap()); diff != "" {
		t.Errorf("PATCH vms/test-vm annotations mismatch (-want +got):\n%s", diff)
	}

	// the replaced annotations are what the next read sees
	resp = patchVM(t, role, `{"annotations": {}}`)

	annotations := decodeVM(t, resp).GetAnnotations()
	if annotations == nil {
		t.Error("PATCH vms/test-vm with {} has no annotations, want an empty object")
	} else if n := len(annotations.GetFields()); n != 0 {
		t.Errorf("PATCH vms/test-vm with {} annotations = %v, want an empty object", annotations)
	}
}

func TestUpdateVMRejectsInvalidAnnotations(t *testing.T) {
	useAnnotatedVMExperiment(t)

	resp := patchVM(t, vmRole("get", "patch"), `{"annotations": {"phenix/default-apps": "no"}}`)

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH vms/test-vm with an invalid annotation = %d, want %d: %s", resp.StatusCode, http.StatusBadRequest, body)
	}

	if !strings.Contains(body, "phenix/default-apps") {
		t.Errorf("PATCH vms/test-vm with an invalid annotation = %q, want it to name phenix/default-apps", body)
	}
}

// A role that may patch but not get a VM does not read its annotations back.
func TestUpdateVMWithoutGetOmitsAnnotations(t *testing.T) {
	useAnnotatedVMExperiment(t)

	resp := patchVM(t, vmRole("patch"), `{"annotations": {"vncBanner": "new"}}`)

	if got := decodeVM(t, resp).GetAnnotations(); got != nil {
		t.Errorf("PATCH vms/test-vm without vms get annotations = %v, want none", got)
	}
}

// Setting a VM's autotunnel annotation needs permission to create forwards to the VM;
// keeping its current value does not.
func TestUpdateVMAutotunnelRequiresForwards(t *testing.T) {
	tests := map[string]struct {
		current map[string]any
		role    rbac.Role
		body    string
		status  int
	}{
		"set with forward permission": {
			role:   combinedRole(vmRole("get", "patch"), forwardRole()),
			body:   `{"annotations": {"phenix/startup-autotunnel": ["9000:127.0.0.1:8080"]}}`,
			status: http.StatusOK,
		},
		"set without forward permission": {
			role:   vmRole("get", "patch"),
			body:   `{"annotations": {"phenix/startup-autotunnel": ["9000:127.0.0.1:8080"]}}`,
			status: http.StatusForbidden,
		},
		"changed without forward permission": {
			current: map[string]any{"phenix/startup-autotunnel": []any{"8080"}},
			role:    vmRole("get", "patch"),
			body:    `{"annotations": {"phenix/startup-autotunnel": ["8080", "22"]}}`,
			status:  http.StatusForbidden,
		},
		"kept without forward permission": {
			current: map[string]any{"phenix/startup-autotunnel": []any{"8080"}},
			role:    vmRole("get", "patch"),
			body:    `{"annotations": {"phenix/startup-autotunnel": ["8080"], "vncBanner": "hi"}}`,
			status:  http.StatusOK,
		},
		"no autotunnel annotation": {
			role:   vmRole("get", "patch"),
			body:   `{"annotations": {"vncBanner": "hi"}}`,
			status: http.StatusOK,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, testExperiment(t, "", map[string]any{
				"type":        "VirtualMachine",
				"general":     map[string]any{"hostname": "test-vm"},
				"hardware":    map[string]any{"vcpus": 1, "memory": 256, "os_type": "linux"},
				"annotations": tc.current,
			}))

			resp := patchVM(t, tc.role, tc.body)
			if body := readBody(t, resp); resp.StatusCode != tc.status {
				t.Errorf("PATCH vms/test-vm %s = %d, want %d: %s", tc.body, resp.StatusCode, tc.status, body)
			}
		})
	}
}
