package tmpl_test

import (
	"bytes"
	"strings"
	"testing"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
)

type headnodeMM struct {
	mm.MM
}

func (headnodeMM) Headnode() string {
	return "headnode"
}

func TestMinimegaScriptSnapshotsEveryDrive(t *testing.T) {
	originalMM := mm.DefaultMM
	mm.DefaultMM = headnodeMM{} //nolint:reassign // install test double

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
	})

	snapshot := true
	spec := &v1.ExperimentSpec{
		ExperimentNameF: "exp",
		TopologyF: &v1.TopologySpec{
			NodesF: []*v1.Node{{
				GeneralF: &v1.General{HostnameF: "vm", VMTypeF: "kvm", SnapshotF: &snapshot},
				HardwareF: &v1.Hardware{
					OSTypeF: "linux",
					DrivesF: []*v1.Drive{
						{ImageF: "os.qc2"},
						{ImageF: "data.qc2", IfaceF: "virtio"},
					},
				},
				InjectionsF: []*v1.Injection{{SrcF: "/src/file", DstF: "/dst/file"}},
			}},
		},
	}

	if err := spec.Init(); err != nil {
		t.Fatalf("initializing experiment spec: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.GenerateFromTemplate("minimega_script.tmpl", spec, &buf); err != nil {
		t.Fatalf("generating minimega script: %v", err)
	}

	script := buf.String()

	for _, want := range []string{
		"## VM: vm ##\n" +
			"disk snapshot os.qc2 headnode_exp_vm_snapshot\n" +
			"disk snapshot data.qc2 headnode_exp_vm_snapshot_1\n" +
			"disk inject headnode_exp_vm_snapshot:1 files ",
		"\nvm config disk headnode_exp_vm_snapshot,writeback headnode_exp_vm_snapshot_1,virtio,writeback\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}

	if strings.Contains(script, "disk inject headnode_exp_vm_snapshot_1") {
		t.Errorf("injections must only target the first drive:\n%s", script)
	}
}

func TestPacketCaptureScriptSnapshotsEveryDrive(t *testing.T) {
	originalMM := mm.DefaultMM
	mm.DefaultMM = headnodeMM{} //nolint:reassign // install test double

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
	})

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "exp",
		TopologyF: &v1.TopologySpec{
			NodesF: []*v1.Node{{
				LabelsF:  map[string]string{"soh-monitor-node": "true"},
				GeneralF: &v1.General{HostnameF: "mon", VMTypeF: "kvm"},
				HardwareF: &v1.Hardware{
					DrivesF: []*v1.Drive{
						{ImageF: "os.qc2"},
						{ImageF: "data.qc2"},
					},
				},
				NetworkF: &v1.Network{
					InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "MGMT"}},
				},
			}},
		},
	}

	if err := spec.Init(); err != nil {
		t.Fatalf("initializing experiment spec: %v", err)
	}

	data := struct {
		Exp *v1.ExperimentSpec
		Mon map[string][]string
	}{Exp: spec}

	var buf bytes.Buffer
	if err := tmpl.GenerateFromTemplate("packet_capture_script.tmpl", data, &buf); err != nil {
		t.Fatalf("generating packet capture script: %v", err)
	}

	script := buf.String()

	for _, want := range []string{
		"## VM: mon ##\n" +
			"disk snapshot os.qc2 headnode_exp_mon_snapshot\n" +
			"disk snapshot data.qc2 headnode_exp_mon_snapshot_1\n" +
			"clear vm config\n",
		"\nvm config disk headnode_exp_mon_snapshot,writeback headnode_exp_mon_snapshot_1,writeback\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}
