package web

import (
	"net/http"
	"strings"
	"testing"

	"phenix/api/experiment"
)

func TestUpdateVMRejectsUnsafeTags(t *testing.T) {
	useAnnotatedVMExperiment(t)

	resp := patchVM(t, vmRole("get", "patch"), `{"tags": {"zone": "a\" \"b"}, "tag_update_mode": "SET"}`)

	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH vms/test-vm with a quote in a tag = %d, want %d: %s", resp.StatusCode, http.StatusBadRequest, body)
	}
}

// A request updating several VMs, one of them onto a VLAN minimega would
// split, is refused whole: no VM changes, even those listed before it.
func TestUpdateVMsRefusesAnUnsafeVLANWhole(t *testing.T) {
	useTestStore(t, testExperiment(t, "", diskVM("vm1", "a.qc2"), diskVM("vm2", "b.qc2")))

	server := serveAs(t, "alice", testRole("vms", "patch", "test-experiment/*")).URL

	const path = "/api/v1/experiments/test-experiment/vms"

	resp := do(t, http.MethodPatch, server+path,
		`{"vms": [{"name": "vm1", "cpus": 4}, {"name": "vm2", "interface": {"index": 0, "vlan": "EXP_1 #"}}]}`)

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH %s with vm2 on VLAN %q = %d, want %d: %s", path, "EXP_1 #", resp.StatusCode, http.StatusBadRequest, body)
	}

	if !strings.Contains(body, "vm2") {
		t.Errorf("PATCH %s with vm2 on VLAN %q = %q, want it to name vm2", path, "EXP_1 #", body)
	}

	exp, err := experiment.Get("test-experiment")
	if err != nil {
		t.Fatalf("experiment.Get(test-experiment) error = %v", err)
	}

	if got := exp.Spec.Topology().FindNodeByName("vm1").Hardware().VCPU(); got != 1 {
		t.Errorf("after the refused PATCH %s, vm1 has %d CPUs, want 1", path, got)
	}
}
