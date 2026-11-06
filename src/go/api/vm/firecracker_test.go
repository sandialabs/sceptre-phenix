package vm_test

import (
	"errors"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vm"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
)

const (
	fcVM  = "fc-vm"
	kvmVM = "kvm-vm"
)

func TestRestartByVMType(t *testing.T) {
	tests := []struct {
		name   string
		vm     string
		vmType string
		want   []string
	}{
		{
			name:   "firecracker restarts cold",
			vm:     fcVM,
			vmType: "firecracker",
			want:   []string{"vm kill fc-vm", "vm start fc-vm"},
		},
		{
			name:   "kvm resets through QMP",
			vm:     kvmVM,
			vmType: "kvm",
			want:   []string{`vm qmp kvm-vm '{ "execute": "system_reset" }'`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.vm, test.vmType, true)
			fake := useFakeMinimega(t, test.vm, test.vmType, fakeHost)

			if err := vm.Restart(testExp, test.vm); err != nil {
				t.Fatalf("Restart(%q) = %v, want nil", test.vm, err)
			}

			if diff := cmp.Diff(test.want, fake.changes()); diff != "" {
				t.Errorf("Restart(%q) commands (-want +got):\n%s", test.vm, diff)
			}
		})
	}
}

func TestShutdownByVMType(t *testing.T) {
	tests := []struct {
		name   string
		vm     string
		vmType string
		want   []string
	}{
		{
			name:   "firecracker uses vm shutdown",
			vm:     fcVM,
			vmType: "firecracker",
			want:   []string{"vm shutdown fc-vm"},
		},
		{
			name:   "kvm powers down through QMP",
			vm:     kvmVM,
			vmType: "kvm",
			want:   []string{`vm qmp kvm-vm '{ "execute": "system_powerdown" }'`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.vm, test.vmType, true)
			fake := useFakeMinimega(t, test.vm, test.vmType, fakeHost)

			if err := vm.Shutdown(testExp, test.vm); err != nil {
				t.Fatalf("Shutdown(%q) = %v, want nil", test.vm, err)
			}

			if diff := cmp.Diff(test.want, fake.changes()); diff != "" {
				t.Errorf("Shutdown(%q) commands (-want +got):\n%s", test.vm, diff)
			}
		})
	}
}

func TestResetDiskStateFirecracker(t *testing.T) {
	useExperiment(t, fcVM, "firecracker", true)
	fake := useFakeMinimega(t, fcVM, "firecracker", fakeHost)

	if err := vm.ResetDiskState(testExp, fcVM); err != nil {
		t.Fatalf("ResetDiskState(%q) = %v, want nil", fcVM, err)
	}

	want := []string{"vm kill fc-vm", "vm reset-disk fc-vm", "vm start fc-vm"}

	if diff := cmp.Diff(want, fake.changes()); diff != "" {
		t.Errorf("ResetDiskState(%q) commands (-want +got):\n%s", fcVM, diff)
	}
}

func TestRedeployByVMType(t *testing.T) {
	tests := []struct {
		name   string
		vm     string
		vmType string
		opts   []vm.RedeployOption
		want   []string
	}{
		{
			name:   "firecracker injects at launch",
			vm:     fcVM,
			vmType: "firecracker",
			opts:   []vm.RedeployOption{vm.Inject(true)},
			want: []string{
				"vm config clone fc-vm",
				"clear vm config firecracker-snapshot",
				"vm kill fc-vm",
				"vm flush",
				"vm config disk test.raw",
				"clear vm config inject",
				"vm config inject 1 /tmp/motd:/etc/motd",
				"vm launch firecracker fc-vm",
				"vm launch",
				"vm start fc-vm",
			},
		},
		{
			name:   "kvm launches as kvm",
			vm:     kvmVM,
			vmType: "kvm",
			want: []string{
				"vm config clone kvm-vm",
				"clear vm config state",
				"vm kill kvm-vm",
				"vm flush",
				"vm launch kvm kvm-vm",
				"vm launch",
				"vm start kvm-vm",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.vm, test.vmType, true)
			fake := useFakeMinimega(t, test.vm, test.vmType, fakeHost)

			if err := vm.Redeploy(testExp, test.vm, test.opts...); err != nil {
				t.Fatalf("Redeploy(%q) = %v, want nil", test.vm, err)
			}

			if diff := cmp.Diff(test.want, fake.changes()); diff != "" {
				t.Errorf("Redeploy(%q) commands (-want +got):\n%s", test.vm, diff)
			}
		})
	}
}

func TestCommitToDiskFirecracker(t *testing.T) {
	out := common.PhenixBase + "/images/out.qc2"
	headnode, _ := os.Hostname()

	tests := []struct {
		name       string
		host       string
		wantCopied []string
	}{
		{
			name: "on the headnode",
			host: fakeHost,
		},
		{
			name:       "on a compute node",
			host:       "compute-1",
			wantCopied: []string{"out.qc2 to " + headnode},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, fcVM, "firecracker", true)
			fake := useFakeMinimega(t, fcVM, "firecracker", test.host)

			files := new(fakeFiles)
			useFakeFiles(t, files)

			var progress []float64

			got, err := vm.CommitToDisk(testExp, fcVM, "out.qc2", func(p float64) { progress = append(progress, p) })
			if err != nil {
				t.Fatalf("CommitToDisk(%q) = %v, want nil", fcVM, err)
			}

			if got != "out.qc2" {
				t.Errorf("CommitToDisk(%q) = %q, want %q", fcVM, got, "out.qc2")
			}

			want := []string{"vm export-disk fc-vm " + out + " qcow2", "vm export-disk", "vm export-disk"}

			if diff := cmp.Diff(want, fake.changes()); diff != "" {
				t.Errorf("CommitToDisk(%q) commands (-want +got):\n%s", fcVM, diff)
			}

			if diff := cmp.Diff(test.wantCopied, files.copied); diff != "" {
				t.Errorf("CommitToDisk(%q) copies to headnode (-want +got):\n%s", fcVM, diff)
			}

			if diff := cmp.Diff([]string{"out.qc2"}, files.synced); diff != "" {
				t.Errorf("CommitToDisk(%q) syncs (-want +got):\n%s", fcVM, diff)
			}

			if diff := cmp.Diff([]float64{0.5}, progress); diff != "" {
				t.Errorf("CommitToDisk(%q) progress (-want +got):\n%s", fcVM, diff)
			}
		})
	}
}

func TestMemorySnapshotFirecracker(t *testing.T) {
	useExperiment(t, fcVM, "firecracker", true)
	fake := useFakeMinimega(t, fcVM, "firecracker", fakeHost)

	var progress []string

	got, err := vm.MemorySnapshot(testExp, fcVM, "mem.elf", func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatalf("MemorySnapshot(%q) = %v, want nil", fcVM, err)
	}

	dir := common.PhenixBase + "/images/" + testExp + "/files"

	if want := dir + "/mem.elf"; got != want {
		t.Errorf("MemorySnapshot(%q) = %q, want %q", fcVM, got, want)
	}

	want := []string{
		"shell mkdir -p " + dir,
		"vm memory-dump fc-vm " + dir + "/mem.elf elf",
		"vm memory-dump",
		"vm memory-dump",
	}

	if diff := cmp.Diff(want, fake.changes()); diff != "" {
		t.Errorf("MemorySnapshot(%q) commands (-want +got):\n%s", fcVM, diff)
	}

	if diff := cmp.Diff([]string{"0.5", "completed"}, progress); diff != "" {
		t.Errorf("MemorySnapshot(%q) progress (-want +got):\n%s", fcVM, diff)
	}
}

func TestSnapshotMovesFiles(t *testing.T) {
	var (
		images = common.PhenixBase + "/images/"
		dir    = images + testExp + "/files"
	)

	tests := []struct {
		name   string
		vm     string
		vmType string
		exts   []string
	}{
		{name: "firecracker", vm: fcVM, vmType: "firecracker", exts: []string{"state", "hdd", "mem", "json"}},
		{name: "kvm", vm: kvmVM, vmType: "kvm", exts: []string{"state", "hdd"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.vm, test.vmType, true)
			fake := useFakeMinimega(t, test.vm, test.vmType, fakeHost)

			if err := vm.Snapshot(testExp, test.vm, "snap1", nil); err != nil {
				t.Fatalf("Snapshot(%q) = %v, want nil", test.vm, err)
			}

			out := testExp + "_" + test.vm + "__snap1"
			want := []string{
				"vm save " + test.vm + " " + out,
				"vm save",
				"vm save",
				"vm start " + test.vm,
				"shell mkdir -p " + dir,
			}

			for _, ext := range test.exts {
				want = append(want, "shell mv "+images+out+"."+ext+" "+dir+"/"+test.vm+"__snap1."+ext)
			}

			if diff := cmp.Diff(want, fake.changes()); diff != "" {
				t.Errorf("Snapshot(%q) commands (-want +got):\n%s", test.vm, diff)
			}
		})
	}
}

func TestRestoreChecksSnapshotType(t *testing.T) {
	tests := []struct {
		name    string
		vm      string
		vmType  string
		sidecar bool
		want    []string
		wantErr error
	}{
		{
			name:    "firecracker snapshot on a firecracker VM",
			vm:      fcVM,
			vmType:  "firecracker",
			sidecar: true,
			want: []string{
				"vm config clone fc-vm",
				"vm config uuid " + fakeUUID,
				"vm config firecracker-snapshot test-experiment/files/fc-vm__snap1",
				"vm config disk test-experiment/files/fc-vm__snap1.hdd,writeback",
				"vm kill fc-vm",
				"vm flush fc-vm",
				"vm launch firecracker fc-vm",
				"vm launch",
				"vm start fc-vm",
			},
		},
		{
			name:    "kvm snapshot on a firecracker VM",
			vm:      fcVM,
			vmType:  "firecracker",
			wantErr: vm.ErrNotSupported,
		},
		{
			name:    "firecracker snapshot on a kvm VM",
			vm:      kvmVM,
			vmType:  "kvm",
			sidecar: true,
			wantErr: vm.ErrNotSupported,
		},
		{
			name:   "kvm snapshot on a kvm VM",
			vm:     kvmVM,
			vmType: "kvm",
			want: []string{
				"vm config clone kvm-vm",
				"vm config uuid " + fakeUUID,
				"vm config state test-experiment/files/kvm-vm__snap1.state",
				"vm config disk test-experiment/files/kvm-vm__snap1.hdd,writeback",
				"vm kill kvm-vm",
				"vm flush kvm-vm",
				"vm launch kvm kvm-vm",
				"vm launch",
				"vm start kvm-vm",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.vm, test.vmType, true)
			fake := useFakeMinimega(t, test.vm, test.vmType, fakeHost)

			snap := test.vm + "__snap1"
			files := &fakeFiles{
				snapshots: []string{snap},
				files: file.Files{
					{Name: snap + ".hdd", Path: snap + ".hdd"},
					{Name: snap + ".state", Path: snap + ".state"},
				},
			}

			if test.sidecar {
				files.files = append(files.files, file.File{Name: snap + ".json", Path: snap + ".json"})
			}

			useFakeFiles(t, files)

			err := vm.Restore(testExp, test.vm, snap)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Restore(%q, %q) = %v, want %v", test.vm, snap, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, fake.changes()); diff != "" {
				t.Errorf("Restore(%q, %q) commands (-want +got):\n%s", test.vm, snap, diff)
			}
		})
	}
}

func TestOpticalDiscNotSupportedForFirecracker(t *testing.T) {
	useExperiment(t, fcVM, "firecracker", true)
	fake := useFakeMinimega(t, fcVM, "firecracker", fakeHost)

	if err := vm.ChangeOpticalDisc(testExp, fcVM, "/phenix/images/test.iso"); !errors.Is(err, vm.ErrNotSupported) {
		t.Errorf("ChangeOpticalDisc(%q) = %v, want %v", fcVM, err, vm.ErrNotSupported)
	}

	if err := vm.EjectOpticalDisc(testExp, fcVM); !errors.Is(err, vm.ErrNotSupported) {
		t.Errorf("EjectOpticalDisc(%q) = %v, want %v", fcVM, err, vm.ErrNotSupported)
	}

	if got := fake.changes(); len(got) != 0 {
		t.Errorf("optical disc commands = %q, want none", got)
	}
}

func TestListAndGetSetVMType(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		running  bool
		vms      mm.VMs
		want     string
	}{
		{
			name:     "stopped experiment uses the topology",
			topology: "firecracker",
			want:     "firecracker",
		},
		{
			name:     "topology without a type is kvm",
			topology: "",
			want:     "kvm",
		},
		{
			name:     "running experiment uses minimega",
			topology: "kvm",
			running:  true,
			vms:      mm.VMs{{Name: fcVM, Running: true, VMType: "firecracker"}},
			want:     "firecracker",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, fcVM, test.topology, test.running)

			originalMM := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double
			mm.DefaultMM = listMM{vms: test.vms}            //nolint:reassign // install test double

			vms, err := vm.List(testExp)
			if err != nil {
				t.Fatalf("List(%q) = %v, want nil", testExp, err)
			}

			if len(vms) != 1 {
				t.Fatalf("List(%q) returned %d VMs, want 1", testExp, len(vms))
			}

			if got := vms[0].VMType; got != test.want {
				t.Errorf("List(%q)[0].VMType = %q, want %q", testExp, got, test.want)
			}

			got, err := vm.Get(testExp, fcVM)
			if err != nil {
				t.Fatalf("Get(%q) = %v, want nil", fcVM, err)
			}

			if got.VMType != test.want {
				t.Errorf("Get(%q).VMType = %q, want %q", fcVM, got.VMType, test.want)
			}
		})
	}
}
