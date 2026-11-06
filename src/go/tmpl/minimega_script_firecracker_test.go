package tmpl_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
)

const scriptBaseDir = "/phenix/experiments/fc-exp"

// scriptSpec returns an experiment of the given nodes, with base directory
// scriptBaseDir.
func scriptSpec(schedules map[string]string, nodes ...*v1.Node) *v1.ExperimentSpec {
	return &v1.ExperimentSpec{
		ExperimentNameF: "fc-exp",
		BaseDirF:        scriptBaseDir,
		SchedulesF:      schedules,
		TopologyF:       &v1.TopologySpec{NodesF: nodes},
	}
}

// render returns what the named template writes for data.
func render(t *testing.T, name string, data any) string {
	t.Helper()

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate(name, data, &script); err != nil {
		t.Fatalf("GenerateFromTemplate(%s) error = %v", name, err)
	}

	return script.String()
}

// vmBlock returns the part of a minimega script from the VM comment on.
func vmBlock(t *testing.T, script string) string {
	t.Helper()

	start := strings.Index(script, "## VM: ")
	if start < 0 {
		t.Fatalf("the script has no VM block:\n%s", script)
	}

	return script[start:]
}

func scriptLines(l ...string) string {
	return strings.Join(l, "\n")
}

func intPtr(i int) *int { return &i }

func boolPtr(b bool) *bool { return &b }

// A kvm node gets the same script as before firecracker nodes existed.
func TestMinimegaScriptKVMUnchanged(t *testing.T) {
	node := &v1.Node{
		TypeF:    "VirtualMachine",
		GeneralF: &v1.General{HostnameF: "host-01", VMTypeF: "kvm", SnapshotF: boolPtr(true)},
		HardwareF: &v1.Hardware{
			CPUF: "Broadwell", VCPUF: 2, MemoryF: 1024, OSTypeF: "linux",
			DrivesF: []*v1.Drive{{ImageF: "ubuntu.qc2"}},
		},
		NetworkF:    &v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "EXP", BridgeF: "phenix"}}},
		InjectionsF: []*v1.Injection{{SrcF: "a.sh", DstF: "/etc/a.sh"}},
		DeletionsF:  []*v1.Deletion{{PathF: "/etc/old"}},
		AdvancedF:   map[string]string{"backchannel": "true"},
		OverridesF:  map[string]string{"Broadwell": "host"},
		LabelsF:     map[string]string{"role": "web"},
	}

	spec := scriptSpec(map[string]string{"host-01": "compute1"}, node)
	snap := spec.SnapshotName("host-01")

	want := scriptLines(
		"ns queueing true",
		"",
		"",
		"## VM: host-01 ##",
		"disk snapshot ubuntu.qc2 "+snap,
		"disk inject "+snap+`:1 files "`+scriptBaseDir+`/a.sh":"/etc/a.sh"`,
		"disk inject "+snap+`:1 delete files "/etc/old"`,
		"clear vm config",
		"vm config schedule compute1",
		"vm config vcpus 2",
		"vm config cpu Broadwell",
		"vm config memory 1024",
		"vm config snapshot true",
		"vm config disk "+snap+",writeback",
		"vm config qemu-append -vga qxl",
		"vm config net phenix,EXP",
		"vm config backchannel true",
		`vm config qemu-override "Broadwell" "host"`,
		`vm config tags "role" "web"`,
		"vm launch kvm host-01",
		"",
	)

	if diff := cmp.Diff(want, render(t, "minimega_script.tmpl", spec)); diff != "" {
		t.Errorf("minimega script of a kvm node mismatch (-want +got):\n%s", diff)
	}
}

// firecrackerNode returns a firecracker node fc-01 with kernel vmlinux, one
// drive and one interface.
func firecrackerNode(snapshot bool, args string) *v1.Node {
	return &v1.Node{
		TypeF:    "VirtualMachine",
		GeneralF: &v1.General{HostnameF: "fc-01", VMTypeF: "firecracker", SnapshotF: boolPtr(snapshot)},
		HardwareF: &v1.Hardware{
			VCPUF: 2, MemoryF: 512, OSTypeF: "linux",
			KernelF: "/phenix/images/vmlinux", KernelArgsF: args,
			DrivesF: []*v1.Drive{{ImageF: "debian.qc2"}},
		},
		NetworkF:   &v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "EXP", BridgeF: "phenix"}}},
		OverridesF: map[string]string{"Broadwell": "host"},
	}
}

// A firecracker node gets its kernel, initrd and kernel arguments, its base
// image as the disk, and its files injected at launch. It gets no disk
// snapshot, disk inject, CPU model, -vga qxl or QEMU override.
func TestMinimegaScriptFirecracker(t *testing.T) {
	withFiles := func(node *v1.Node) *v1.Node {
		node.InjectionsF = []*v1.Injection{
			{SrcF: "a.sh", DstF: "/etc/a.sh"}, {SrcF: "/abs/b.sh", DstF: "/etc/b.sh"},
		}
		node.DeletionsF = []*v1.Deletion{{PathF: "/etc/old"}, {PathF: "/etc/old2"}}

		return node
	}

	tests := []struct {
		name string
		node *v1.Node
		want string
	}{
		{
			name: "kernel args without root, injections and deletions",
			node: withFiles(firecrackerNode(true, "console=ttyS0 quiet")),
			want: scriptLines(
				"## VM: fc-01 ##",
				"clear vm config",
				"vm config vcpus 2",
				"vm config memory 512",
				"vm config kernel /phenix/images/vmlinux",
				`vm config append "console=ttyS0 quiet root=/dev/vda1 rw"`,
				"vm config snapshot true",
				"vm config disk debian.qc2",
				`vm config inject 1 "`+scriptBaseDir+`/a.sh":"/etc/a.sh" "/abs/b.sh":"/etc/b.sh"`,
				`vm config inject delete 1 "/etc/old" "/etc/old2"`,
				"vm config net phenix,EXP",
				"vm launch firecracker fc-01",
				"",
			),
		},
		{
			name: "kernel args with root and an initrd",
			node: func() *v1.Node {
				node := firecrackerNode(true, "root=/dev/vda rw console=ttyS0")
				node.HardwareF.InitrdF = "/phenix/images/initrd.img"

				return node
			}(),
			want: scriptLines(
				"## VM: fc-01 ##",
				"clear vm config",
				"vm config vcpus 2",
				"vm config memory 512",
				"vm config kernel /phenix/images/vmlinux",
				"vm config initrd /phenix/images/initrd.img",
				`vm config append "root=/dev/vda rw console=ttyS0"`,
				"vm config snapshot true",
				"vm config disk debian.qc2",
				"vm config net phenix,EXP",
				"vm launch firecracker fc-01",
				"",
			),
		},
		{
			name: "no kernel args, quotes escaped",
			node: firecrackerNode(true, `init="/sbin/init"`),
			want: scriptLines(
				"## VM: fc-01 ##",
				"clear vm config",
				"vm config vcpus 2",
				"vm config memory 512",
				"vm config kernel /phenix/images/vmlinux",
				`vm config append "init=\"/sbin/init\" root=/dev/vda1 rw"`,
				"vm config snapshot true",
				"vm config disk debian.qc2",
				"vm config net phenix,EXP",
				"vm launch firecracker fc-01",
				"",
			),
		},
		{
			name: "no snapshot, so no injections",
			node: withFiles(firecrackerNode(false, "")),
			want: scriptLines(
				"## VM: fc-01 ##",
				"clear vm config",
				"vm config vcpus 2",
				"vm config memory 512",
				"vm config kernel /phenix/images/vmlinux",
				`vm config append "root=/dev/vda1 rw"`,
				"vm config snapshot false",
				"vm config disk debian.qc2",
				"vm config net phenix,EXP",
				"vm launch firecracker fc-01",
				"",
			),
		},
		{
			name: "inject partition 0, so no injections",
			node: func() *v1.Node {
				node := withFiles(firecrackerNode(true, ""))
				node.HardwareF.DrivesF[0].InjectPartitionF = intPtr(0)

				return node
			}(),
			want: scriptLines(
				"## VM: fc-01 ##",
				"clear vm config",
				"vm config vcpus 2",
				"vm config memory 512",
				"vm config kernel /phenix/images/vmlinux",
				`vm config append "root=/dev/vda1 rw"`,
				"vm config snapshot true",
				"vm config disk debian.qc2",
				"vm config net phenix,EXP",
				"vm launch firecracker fc-01",
				"",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmBlock(t, render(t, "minimega_script.tmpl", scriptSpec(nil, tt.node)))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("minimega script of a firecracker node mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The packet capture script gives a firecracker node the same commands as the
// minimega script, and a kvm node the commands it got before.
func TestPacketCaptureScriptFirecracker(t *testing.T) {
	server := &v1.Node{
		TypeF:       "VirtualMachine",
		GeneralF:    &v1.General{HostnameF: "es", VMTypeF: "firecracker"},
		HardwareF:   &v1.Hardware{VCPUF: 1, MemoryF: 512, KernelF: "vmlinux", DrivesF: []*v1.Drive{{ImageF: "es.qc2"}}},
		NetworkF:    &v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "EXP", BridgeF: "phenix"}}},
		InjectionsF: []*v1.Injection{{SrcF: "/abs/es.yml", DstF: "/etc/es.yml"}},
		LabelsF:     map[string]string{"soh-elastic-server": ""},
	}

	monitor := &v1.Node{
		TypeF:     "VirtualMachine",
		GeneralF:  &v1.General{HostnameF: "mon", VMTypeF: "kvm"},
		HardwareF: &v1.Hardware{CPUF: "Broadwell", VCPUF: 1, MemoryF: 512, DrivesF: []*v1.Drive{{ImageF: "mon.qc2"}}},
		NetworkF:  &v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "EXP", BridgeF: "phenix"}}},
		LabelsF:   map[string]string{"soh-monitor-node": ""},
	}

	spec := scriptSpec(nil, server, monitor)
	snap := spec.SnapshotName("mon")

	data := struct {
		Exp ifaces.ExperimentSpec
		Mon map[string][]string
	}{Exp: spec, Mon: map[string][]string{}}

	want := scriptLines(
		"## ELASTIC SERVER ##",
		"## VM: es ##",
		"",
		"clear vm config",
		"vm config vcpus 1",
		"vm config memory 512",
		"vm config kernel vmlinux",
		`vm config append "root=/dev/vda1 rw"`,
		"vm config snapshot true",
		"vm config disk es.qc2",
		`vm config inject 1 "/abs/es.yml":"/etc/es.yml"`,
		"vm config net phenix,EXP",
		"vm launch firecracker es",
		"",
		"## CAPTURE/MONITOR VMS ##",
		"## VM: mon ##",
		"        ",
		"disk snapshot mon.qc2 "+snap,
		"clear vm config",
		"vm config vcpus 1",
		"vm config cpu Broadwell",
		"vm config memory 512",
		"vm config snapshot true",
		"vm config disk "+snap+",writeback",
		"vm config qemu-append -vga qxl",
		"vm config net phenix,EXP",
		"vm launch kvm mon",
		"",
		"vm launch",
		"vm start all",
	)

	if diff := cmp.Diff(want, render(t, "packet_capture_script.tmpl", data)); diff != "" {
		t.Errorf("packet capture script mismatch (-want +got):\n%s", diff)
	}
}
