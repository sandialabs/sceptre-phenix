package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/disk"
	"phenix/api/disk/disktest"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// getDiskDetails asks for the details of the image as the role, failing the test unless the status is want, and
// checks the body against openapi.yml.
func getDiskDetails(t *testing.T, role rbac.Role, image string, want int) disk.Inspection {
	t.Helper()

	path := "/api/v1/disks/details?disk=" + url.QueryEscape(image)
	resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+path, "")
	wantDocumentedBody(t, http.MethodGet, path, resp)

	body := readBody(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, want, body)
	}

	var found disk.Inspection

	if want == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &found); err != nil {
			t.Fatalf("GET %s = %s, not disk details: %v", path, body, err)
		}
	}

	return found
}

// useDetailsDisks makes a files directory with kali.hdd, a raw image with two partitions, over.qc2 backed by it, and
// o.hdd outside it, which only exp-b uses. exp-a boots vm1 from kali.hdd and vm2 from over.qc2.
func useDetailsDisks(t *testing.T) diskFiles {
	t.Helper()

	files := useDiskFilesDir(t)
	dir, outside := files.dir, files.outside

	disktest.WriteMBRImage(t, dir+"/kali.hdd")
	disktest.WriteMBRImage(t, outside+"/o.hdd")
	writeDisks(t, dir+"/over.qc2")
	backedBy(t, dir+"/over.qc2", "kali.hdd")

	useTestStore(t,
		namedExperiment(t, "exp-a", "", diskVM("vm1", "kali.hdd"), diskVM("vm2", "over.qc2")),
		namedExperiment(t, "exp-b", "", diskVM("vm1", outside+"/o.hdd", "kali.hdd")),
	)

	return files
}

func TestGetDiskDetails(t *testing.T) {
	files := useDetailsDisks(t)
	dir := files.dir

	found := getDiskDetails(t, disksRole([]string{"exp-a"}, "list"), "kali.hdd", http.StatusOK)

	ext4 := &disk.Filesystem{Type: "ext4", Label: "rootfs"}
	want := disk.Inspection{
		FullPath: dir + "/kali.hdd", Format: "raw", FileBytes: 8 << 20, VirtualBytes: 8 << 20,
		PartitionTable: "mbr", DiskID: "0xdeadbeef", SectorSize: 512,
		Partitions: []disk.Partition{
			{Number: 1, Type: "Linux", TypeID: "0x83", Start: 1 << 20, Size: 2 << 20, Bootable: true, Filesystem: ext4},
			{
				Number:     2,
				Type:       "Linux swap / Solaris",
				TypeID:     "0x82",
				Start:      3 << 20,
				Size:       5 << 20,
				Filesystem: &disk.Filesystem{Type: "swap"},
			},
		},
		PartitionNotes: []string{"partition 2 ends past the end of the disk, so phenix shows it cut"},
		Overlays:       []disk.Overlay{{FullPath: dir + "/over.qc2", Direct: true}},
		// exp-b uses kali.hdd too, but the role may not get exp-b
		VMs: []disk.VMUse{{Experiment: "exp-a", VM: "vm1", Drive: 0, InjectPartition: 1}},
	}

	opts := cmp.Options{cmpopts.IgnoreFields(disk.Inspection{}, "AllocatedBytes", "Modified"), cmpopts.EquateEmpty()}
	if diff := cmp.Diff(want, found, opts); diff != "" {
		t.Errorf("GET /disks/details of kali.hdd mismatch (-want +got):\n%s", diff)
	}

	// an image backed by a raw file that the fake qemu-img calls qcow2 but is not one
	over := getDiskDetails(t, disksRole([]string{"exp-a"}, "list"), dir+"/over.qc2", http.StatusOK)
	if want := "phenix cannot read the partitions: the image does not have a qcow2 header"; over.PartitionsUnavailable != want {
		t.Errorf("GET /disks/details of over.qc2 partitionsUnavailable = %q, want %q", over.PartitionsUnavailable, want)
	}
}

func TestGetDiskDetailsRefuses(t *testing.T) {
	files := useDetailsDisks(t)
	outside := files.outside

	expB := disksRole([]string{"exp-b"}, "list")
	expA := disksRole([]string{"exp-a"}, "list")

	tests := map[string]struct {
		role  rbac.Role
		image string
		want  int
	}{
		"a role that may list other disks": {
			role:  testRole("disks", "list", "other.qc2"),
			image: "kali.hdd",
			want:  http.StatusForbidden,
		},
		"a role with no permissions":                {role: rbac.Role{Spec: &v1.RoleSpec{}}, image: "kali.hdd", want: http.StatusForbidden},
		"a role that may get disks but not list":    {role: testRole("disks", "get", "*"), image: "kali.hdd", want: http.StatusForbidden},
		"no image":                                  {role: expA, image: "", want: http.StatusBadRequest},
		"an image the list does not hold":           {role: expA, image: "missing.hdd", want: http.StatusNotFound},
		"a path that leaves the files directory":    {role: expA, image: "../../etc/passwd", want: http.StatusBadRequest},
		"an image outside that the role cannot see": {role: expA, image: outside + "/o.hdd", want: http.StatusBadRequest},
		"an image outside that the role sees":       {role: expB, image: outside + "/o.hdd", want: http.StatusOK},
		"an image in a folder the list leaves out":  {role: expA, image: "exp-a/files/kali.hdd", want: http.StatusBadRequest},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			getDiskDetails(t, tt.role, tt.image, tt.want)
		})
	}
}
