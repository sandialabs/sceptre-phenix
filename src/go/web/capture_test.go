package web

import (
	"net/http"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"phenix/util/mm"
	"phenix/web/proto"
	"phenix/web/rbac"
)

func TestStopVMCaptures(t *testing.T) {
	var (
		both = []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/tmp/0.pcap"},
			{VM: "test-vm", Interface: 1, Filepath: "/tmp/1.pcap"},
		}
		stopper = testRole("vms/captures", "delete", "*/*")
	)

	tests := map[string]struct {
		query     string
		role      rbac.Role
		captures  []mm.Capture
		want      int
		wantStops int32
	}{
		"every interface": {"", stopper, both, http.StatusNoContent, 1},
		"one interface":   {"?iface=1", stopper, both, http.StatusNoContent, 1},
		// rather than stop every capture
		"interface not captured": {"?iface=0", stopper, both[1:], http.StatusInternalServerError, 0},
		"interface not a number": {"?iface=not-a-number", stopper, both, http.StatusBadRequest, 0},
		"list only":              {"?iface=0", testRole("vms/captures", "list", "*/*"), both, http.StatusForbidden, 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)

			fake := useTestMM(t, &testMM{
				vms: mm.VMs{{
					Name:     "test-vm",
					Running:  true,
					Networks: []string{"EXP_1 (101)", "EXP_2 (102)"},
				}},
				captures: tt.captures,
			})

			server := serveAs(t, "alice", tt.role).URL

			resp := do(t, http.MethodDelete, server+"/api/v1/experiments/test-experiment/vms/test-vm/captures"+tt.query, "")
			if resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, resp.StatusCode, readBody(t, resp))
			}

			if n := fake.captureStops.Load(); n != tt.wantStops {
				t.Fatalf("minimega stopped captures %d times, want %d", n, tt.wantStops)
			}
		})
	}
}

func TestGetExperimentCaptures(t *testing.T) {
	tests := map[string]struct {
		started      string
		role         rbac.Role
		wantVMs      []string
		wantListings int32
	}{
		// a VM is named exp/vm, like every other VM resource name
		"of the VMs the role names": {
			started:      "2024-01-01T00:00:00Z",
			role:         testRole("experiments/captures", "list", "test-experiment", "test-experiment/vm-a"),
			wantVMs:      []string{"vm-a"},
			wantListings: 1,
		},
		// asking minimega would recreate the experiment's namespace
		"none of a stopped experiment": {
			role: testRole("experiments/captures", "list", "test-experiment", "test-experiment/*"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, testExperiment(t, tt.started))

			fake := useTestMM(t, &testMM{captures: []mm.Capture{
				{VM: "vm-a", Interface: 0, Filepath: "/tmp/a.pcap"},
				{VM: "vm-b", Interface: 0, Filepath: "/tmp/b.pcap"},
			}})

			server := serveAs(t, "alice", tt.role).URL

			resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/captures", "")

			body := readBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
			}

			var list proto.CaptureList
			if err := protojson.Unmarshal([]byte(body), &list); err != nil {
				t.Fatalf("unmarshaling captures: %v", err)
			}

			vms := make([]string, 0, len(list.GetCaptures()))
			for _, c := range list.GetCaptures() {
				vms = append(vms, c.GetVm())
			}

			if !slices.Equal(vms, tt.wantVMs) {
				t.Errorf("captures of %v, want %v", vms, tt.wantVMs)
			}

			if n := fake.captureListings.Load(); n != tt.wantListings {
				t.Errorf("minimega listed captures %d times, want %d", n, tt.wantListings)
			}
		})
	}
}

func TestStopCaptureSubnetNeedsDelete(t *testing.T) {
	tests := map[string]struct {
		role     rbac.Role
		wantCode int
	}{
		// the handler reads the (invalid) body only once the role check passes
		"delete allowed": {
			role:     testRole("exp/captureSubnet", "delete", "test-experiment"),
			wantCode: http.StatusBadRequest,
		},
		"create only": {
			role:     testRole("exp/captureSubnet", "create", "test-experiment"),
			wantCode: http.StatusForbidden,
		},
		"other experiment": {
			role:     testRole("exp/captureSubnet", "delete", "other-experiment"),
			wantCode: http.StatusForbidden,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, "alice", tc.role).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/stopCaptureSubnet", "not json")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}
		})
	}
}
