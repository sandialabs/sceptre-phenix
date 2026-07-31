package util

import (
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
)

func TestExperimentToProtobufVMCount(t *testing.T) {
	external := true
	vms := []mm.VM{
		{Name: "booted"},
		{Name: "delayed"},
		{Name: "disabled", DoNotBoot: true},
		{Name: "external"},
	}

	tests := []struct {
		name      string
		startTime string
		want      uint32
	}{
		{
			name: "stopped experiment counts all configured VMs",
			want: 4,
		},
		{
			name:      "running experiment excludes do-not-boot and external VMs",
			startTime: "2026-07-31T12:00:00Z",
			want:      2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exp := types.NewExperiment(store.ConfigMetadata{Name: "test"})
			exp.Spec.SetExperimentName("test")
			exp.Spec.SetTopology(&v1.TopologySpec{
				NodesF: []*v1.Node{
					{GeneralF: &v1.General{HostnameF: "external"}, ExternalF: &external},
				},
			})
			exp.Status.SetStartTime(test.startTime)

			got := ExperimentToProtobuf(*exp, "", vms).GetVmCount()
			if got != test.want {
				t.Fatalf("VM count = %d, want %d", got, test.want)
			}
		})
	}
}
