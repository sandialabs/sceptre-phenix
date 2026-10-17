package web

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"

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
		// the audit event's outcome and phenix.vm.interface ("" for none)
		wantOutcome, wantIface string
	}{
		"every interface": {"", stopper, both, http.StatusNoContent, 1, "success", ""},
		"one interface":   {"?iface=1", stopper, both, http.StatusNoContent, 1, "success", "1"},
		// rather than stop every capture
		"interface not captured": {"?iface=0", stopper, both[1:], http.StatusInternalServerError, 0, "failure", "0"},
		"interface not a number": {"?iface=not-a-number", stopper, both, http.StatusBadRequest, 0, "", ""},
		"list only": {
			"?iface=0", testRole("vms/captures", "list", "*/*"), both, http.StatusForbidden, 0, "failure", "",
		},
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
			events := captureAudit(t)

			path := "/api/v1/experiments/test-experiment/vms/test-vm/captures" + tt.query

			resp := do(t, http.MethodDelete, server+path, "")
			if resp.StatusCode != tt.want {
				t.Errorf("DELETE %s = %d, want %d: %s", path, resp.StatusCode, tt.want, readBody(t, resp))
			}

			if n := fake.captureStops.Load(); n != tt.wantStops {
				t.Errorf("DELETE %s stopped captures %d times, want %d", path, n, tt.wantStops)
			}

			got := events()
			if tt.wantOutcome == "" {
				if len(got) != 0 {
					t.Errorf("DELETE %s audit events = %v, want none before the RBAC check", path, got)
				}

				return
			}

			e := wantAudit(t, got, "capture-stop", tt.wantOutcome, map[string]any{
				"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.vm.name": "test-vm",
			})
			if e == nil {
				return
			}

			iface := ""
			if v, ok := e["phenix.vm.interface"]; ok {
				iface = fmt.Sprint(v)
			}

			if iface != tt.wantIface {
				t.Errorf("DELETE %s audit phenix.vm.interface = %q, want %q", path, iface, tt.wantIface)
			}
		})
	}
}

func TestGetExperimentCaptures(t *testing.T) {
	tests := map[string]struct {
		started      string
		role         rbac.Role
		want         *proto.CaptureList
		wantListings int32
	}{
		// a VM is named exp/vm, like every other VM resource name
		"of the VMs the role names": {
			started:      "2024-01-01T00:00:00Z",
			role:         testRole("experiments/captures", "list", "test-experiment", "test-experiment/vm-a"),
			want:         &proto.CaptureList{Captures: []*proto.Capture{{Vm: "vm-a", Filepath: "/tmp/a.pcap"}}},
			wantListings: 1,
		},
		// asking minimega would recreate the experiment's namespace
		"none of a stopped experiment": {
			role: testRole("experiments/captures", "list", "test-experiment", "test-experiment/*"),
			want: &proto.CaptureList{},
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
				t.Fatalf("GET /api/v1/experiments/test-experiment/captures = %d, want %d: %s",
					resp.StatusCode, http.StatusOK, body)
			}

			list := new(proto.CaptureList)
			if err := protojson.Unmarshal([]byte(body), list); err != nil {
				t.Fatalf("GET /api/v1/experiments/test-experiment/captures = %s, not a capture list: %v", body, err)
			}

			if diff := cmp.Diff(tt.want, list, protocmp.Transform()); diff != "" {
				t.Errorf("GET /api/v1/experiments/test-experiment/captures mismatch (-want +got):\n%s", diff)
			}

			if n := fake.captureListings.Load(); n != tt.wantListings {
				t.Errorf("GET /api/v1/experiments/test-experiment/captures listed minimega's captures %d times, want %d",
					n, tt.wantListings)
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
			events := captureAudit(t)

			const path = "/api/v1/experiments/test-experiment/stopCaptureSubnet"

			resp := do(t, http.MethodPost, server+path, "not json")
			if resp.StatusCode != tc.wantCode {
				t.Errorf("POST %s = %d, want %d: %s", path, resp.StatusCode, tc.wantCode, readBody(t, resp))
			}

			// a body refused after the RBAC check is not audited
			denied := countAudit(events(), "capture-stop", "failure", nil)
			if forbidden := tc.wantCode == http.StatusForbidden; forbidden && denied == 0 {
				t.Errorf("POST %s refused by RBAC audited no capture-stop failure, want one", path)
			} else if !forbidden && denied != 0 {
				t.Errorf("POST %s audited %d capture-stop failures, want none after the RBAC check", path, denied)
			}
		})
	}
}
