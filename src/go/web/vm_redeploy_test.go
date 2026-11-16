package web

import (
	"net/http"
	"testing"

	"phenix/util/mm/mmtest"
	"phenix/web/cache"
)

// Redeploy takes a disk spec that sets an interface or a cache mode, as
// minimega does, and refuses one that minimega would not parse. A request that
// passes the check reaches the VM lock, which another action holds here.
func TestRedeployChecksTheDiskSpec(t *testing.T) {
	useAnnotatedVMExperiment(t)

	received := mmtest.Use(t, nil)

	if err := cache.LockVMForRedeploying("test-experiment", "test-vm"); err != nil {
		t.Fatalf("cache.LockVMForRedeploying(test-experiment, test-vm) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockVM("test-experiment", "test-vm") })

	const path = "/api/v1/experiments/test-experiment/vms/test-vm/redeploy"

	server := serveAs(t, "alice", testRole("vms/redeploy", "update", "test-experiment/test-vm")).URL + path

	tests := []struct {
		name, request string
		want          int
	}{
		{name: "a path", request: `{"disk": "a.qc2"}`, want: http.StatusConflict},
		{name: "an interface", request: `{"disk": "a.qc2,virtio"}`, want: http.StatusConflict},
		{name: "a cache mode", request: `{"disk": "a.qc2,writeback"}`, want: http.StatusConflict},
		{name: "an interface and a cache mode", request: `{"disk": "a.qc2,ide,none"}`, want: http.StatusConflict},
		{name: "an unknown interface", request: `{"disk": "a.qc2,usb"}`, want: http.StatusBadRequest},
		{name: "no path", request: `{"disk": ",virtio"}`, want: http.StatusBadRequest},
		{name: "a path minimega would expand", request: `{"disk": "$HOME.qc2,virtio"}`, want: http.StatusBadRequest},
		// the VM name comes from the path; an older client's name field is ignored
		{name: "a name in the body", request: `{"name": "other-vm", "disk": "a.qc2"}`, want: http.StatusConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := do(t, http.MethodPost, server, test.request)
			if body := readBody(t, resp); resp.StatusCode != test.want {
				t.Errorf("POST %s %s = %d, want %d: %s", path, test.request, resp.StatusCode, test.want, body)
			}
		})
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("POST %s requests sent minimega %q, want nothing", path, sent)
	}
}
