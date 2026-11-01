package web

import (
	"net/http"
	"testing"

	v1 "phenix/types/version/v1"
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

func patchVM(t *testing.T, role rbac.Role, body string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", role).URL

	return do(t, http.MethodPatch, server+"/api/v1/experiments/test-experiment/vms/test-vm", body)
}
