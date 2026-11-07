package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm"
)

// DELETE .../snapshots/{snapshot} deletes both files of a snapshot the VM has,
// and refuses a snapshot it does not have or whose disk a running VM uses.
func TestDeleteVMSnapshot(t *testing.T) {
	const snapFiles = "a__before"

	tests := []struct {
		name        string
		snapshot    string
		running     bool
		disk        string // the disk minimega reports for VM a
		role        string // the VM the role may delete snapshots of
		wantStatus  int
		wantDeleted []string
	}{
		{
			name: "a snapshot of a stopped experiment", snapshot: snapFiles, role: "exp/a",
			wantStatus: http.StatusNoContent, wantDeleted: []string{"exp/files/a__before.hdd", "exp/files/a__before.state"},
		},
		{
			name: "a name with an extension", snapshot: snapFiles + ".hdd", role: "exp/a",
			wantStatus: http.StatusNoContent, wantDeleted: []string{"exp/files/a__before.hdd", "exp/files/a__before.state"},
		},
		{
			name: "a snapshot that a running VM does not use", snapshot: snapFiles, running: true, disk: "/images/a.qc2",
			role: "exp/a", wantStatus: http.StatusNoContent,
			wantDeleted: []string{"exp/files/a__before.hdd", "exp/files/a__before.state"},
		},
		{
			name: "a snapshot that the restored VM runs from", snapshot: snapFiles, running: true,
			disk: "/phenix/images/exp/files/a__before.hdd", role: "exp/a", wantStatus: http.StatusConflict,
		},
		{name: "a snapshot the VM does not have", snapshot: "a__after", role: "exp/a", wantStatus: http.StatusNotFound},
		{name: "a VM the topology lacks", snapshot: snapFiles, role: "exp/*", wantStatus: http.StatusNotFound},
		{name: "a role that may not delete", snapshot: snapFiles, role: "exp/b", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			started := ""
			if tc.running {
				started = "2024-01-01T00:00:00Z"
			}

			useTestStore(t, namedExperiment(t, "exp", started, diskVM("a", "a.qc2"), diskVM("b", "a.qc2")))
			useTestMM(t, &testMM{vms: mm.VMs{{Name: "a", Disk: tc.disk, Running: tc.running}}})

			files := useTestFiles(t, "a__before.hdd", "a__before.state", "b__before.hdd", "b__before.state", "a__after.hdd")

			vmName := "a"
			if tc.name == "a VM the topology lacks" {
				vmName = "nope"
			}

			server := serveAs(t, "alice", testRole("vms/snapshots", "delete", tc.role)).URL
			resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/"+vmName+"/snapshots/"+tc.snapshot, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("DELETE snapshots/%s = %d, want %d: %s", tc.snapshot, resp.StatusCode, tc.wantStatus, body)
			}

			if diff := cmp.Diff(tc.wantDeleted, files.deleted, cmpopts.EquateEmpty(), cmpopts.SortSlices(func(a, b string) bool {
				return a < b
			})); diff != "" {
				t.Errorf("deleted files mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
