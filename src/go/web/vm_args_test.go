package web

import (
	"net/http"
	"strings"
	"testing"

	"phenix/api/experiment"
)

// A tag minimega would not take as one quoted argument is refused, in its key or its value; a newline or tab,
// which it takes, is not.
func TestUpdateVMRejectsUnsafeTags(t *testing.T) {
	for name, tc := range map[string]struct {
		tags string
		want int
	}{
		"a quote in the value":        {`{"zone": "a\" \"b"}`, http.StatusBadRequest},
		"a quote in the key":          {`{"zo\"ne": "a"}`, http.StatusBadRequest},
		"a backslash":                 {`{"zone": "a\\b"}`, http.StatusBadRequest},
		"a dollar sign":               {`{"zone": "$HOME"}`, http.StatusBadRequest},
		"a control character":         {`{"zone": "a\u0007b"}`, http.StatusBadRequest},
		"a scheme minimega fetches":   {`{"zone": "https://example.com/a"}`, http.StatusBadRequest},
		"a newline and a tab":         {`{"zone": "a\nb\tc"}`, http.StatusOK},
		"spaces in the key and value": {`{"my zone": "a b"}`, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			useAnnotatedVMExperiment(t)

			resp := patchVM(t, vmRole("get", "patch"), `{"tags": `+tc.tags+`, "tag_update_mode": "SET"}`)

			if body := readBody(t, resp); resp.StatusCode != tc.want {
				t.Errorf("PATCH vms/test-vm with tags %s = %d, want %d: %s", tc.tags, resp.StatusCode, tc.want, body)
			}
		})
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
