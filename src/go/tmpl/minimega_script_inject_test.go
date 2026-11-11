package tmpl_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

func TestMinimegaScriptDiskInjects(t *testing.T) {
	snapshot := true

	node := func(hostname, osType string) *v1.Node {
		return &v1.Node{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: hostname,
				VMTypeF:   "kvm",
				SnapshotF: &snapshot,
			},
			HardwareF: &v1.Hardware{
				OSTypeF: osType,
				DrivesF: []*v1.Drive{{ImageF: osType + ".qc2"}},
			},
			InjectionsF: []*v1.Injection{
				{SrcF: "startup.ps1", DstF: "/phenix/startup.ps1"},
				{SrcF: "/abs/sched.cmd", DstF: "/Start Menu/Programs/Startup/sched.cmd"},
			},
			DeletionsF: []*v1.Deletion{{PathF: "/old.txt"}, {PathF: "/Start Menu/old.cmd"}},
		}
	}

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "inject-exp",
		BaseDirF:        "/phenix/experiments/inject-exp",
		TopologyF: &v1.TopologySpec{
			NodesF: []*v1.Node{node("linux-01", "linux"), node("win-01", "windows")},
		},
	}

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate("minimega_script.tmpl", spec, &script); err != nil {
		t.Fatalf("GenerateFromTemplate(minimega_script.tmpl) error = %v", err)
	}

	var got []string

	for line := range strings.SplitSeq(script.String(), "\n") {
		if strings.HasPrefix(line, "disk inject ") {
			got = append(got, line)
		}
	}

	var (
		linux   = spec.SnapshotName("linux-01") + ":1"
		windows = spec.SnapshotName("win-01") + ":1"
		options = `options "-t lowntfs-3g -o ignore_case"`
	)

	want := []string{
		"disk inject " + linux + ` files "/phenix/experiments/inject-exp/startup.ps1":"/phenix/startup.ps1"` +
			` "/abs/sched.cmd":"/Start Menu/Programs/Startup/sched.cmd"`,
		"disk inject " + linux + ` delete files "/old.txt","/Start Menu/old.cmd"`,
		"disk inject " + windows + " " + options +
			` files "/phenix/experiments/inject-exp/startup.ps1":"/phenix/startup.ps1"`,
		"disk inject " + windows + " " + options +
			` files "/abs/sched.cmd":"/Start Menu/Programs/Startup/sched.cmd"`,
		"disk inject " + windows + " " + options + ` delete files "/old.txt","/Start Menu/old.cmd"`,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("minimega script disk inject lines mismatch (-want +got):\n%s", diff)
	}
}
