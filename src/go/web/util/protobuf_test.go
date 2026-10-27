package util

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

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

// TestVMToProtobufCarriesNotes checks that the VM message and its JSON carry the node notes apart from the tags.
func TestVMToProtobufCarriesNotes(t *testing.T) {
	notes := []string{"Domain controller.", "Reset the password\nbefore each run."}
	vm := mm.VM{Name: "host-01", Tags: map[string]string{"role": "dc"}, Notes: notes}

	pb := VMToProtobuf("test", vm, nil)
	if !slices.Equal(pb.GetNotes(), notes) {
		t.Errorf("VMToProtobuf notes = %q, want %q", pb.GetNotes(), notes)
	}

	body, err := protojson.Marshal(pb)
	if err != nil {
		t.Fatalf("marshaling the VM: %v", err)
	}

	var got struct {
		Notes []string          `json:"notes"`
		Tags  map[string]string `json:"tags"`
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshaling %s: %v", body, err)
	}

	if !slices.Equal(got.Notes, notes) {
		t.Errorf("JSON notes = %q, want %q", got.Notes, notes)
	}

	if want := map[string]string{"role": "dc"}; !maps.Equal(got.Tags, want) {
		t.Errorf("JSON tags = %v, want %v", got.Tags, want)
	}
}
