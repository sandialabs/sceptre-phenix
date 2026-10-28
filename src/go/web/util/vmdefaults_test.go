package util

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	"phenix/api/experiment"
	"phenix/web/proto"
)

func TestVMDefaultsToProtobuf(t *testing.T) {
	t.Parallel()

	three := 3

	tests := []struct {
		name  string
		saved experiment.VMDefaults
		want  *proto.VMDefaults
	}{
		{
			name: "a disk and its inject partition",
			saved: experiment.VMDefaults{
				CPUs: 2, RAM: 1024, Disk: "/images/../images/a.qc2", InjectPartition: &three, Snapshot: true, DNB: true, Host: "node1",
			},
			want: &proto.VMDefaults{
				Cpus: 2, Ram: 1024, Disk: "/images/a.qc2", InjectPartition: 3, Snapshot: true, Dnb: true, Host: "node1",
			},
		},
		{
			name:  "a disk without an inject partition",
			saved: experiment.VMDefaults{CPUs: 1, RAM: 512, Disk: "/images/b.qc2"},
			want:  &proto.VMDefaults{Cpus: 1, Ram: 512, Disk: "/images/b.qc2", InjectPartition: 1},
		},
		{
			name:  "no disk",
			saved: experiment.VMDefaults{CPUs: 1, RAM: 512},
			want:  &proto.VMDefaults{Cpus: 1, Ram: 512},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, VMDefaultsToProtobuf(tc.saved), protocmp.Transform()); diff != "" {
				t.Errorf("VMDefaultsToProtobuf(%+v) mismatch (-want +got):\n%s", tc.saved, diff)
			}
		})
	}
}

// SetVMDefaults gives each VM its saved settings, and leaves null the defaults
// of a VM with none, or of every VM when the settings are absent or do not decode.
func TestSetVMDefaults(t *testing.T) {
	t.Parallel()

	saved := `{"a":{"cpus":4,"ram":2048,"disk":"/images/a.qc2","snapshot":false,"dnb":false,"host":""}}`

	tests := []struct {
		name        string
		annotations map[string]string
		want        map[string]*proto.VMDefaults
	}{
		{
			name:        "saved settings",
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved},
			want: map[string]*proto.VMDefaults{
				"a": {Cpus: 4, Ram: 2048, Disk: "/images/a.qc2", InjectPartition: 1},
				"b": nil,
			},
		},
		{
			name:        "no saved settings",
			annotations: map[string]string{"topology": "t"},
			want:        map[string]*proto.VMDefaults{"a": nil, "b": nil},
		},
		{
			name:        "saved settings that do not decode",
			annotations: map[string]string{experiment.VMDefaultsAnnotation: "{"},
			want:        map[string]*proto.VMDefaults{"a": nil, "b": nil},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vms := []*proto.VM{{Name: "a"}, {Name: "b"}}
			SetVMDefaults("exp", tc.annotations, vms...)

			got := map[string]*proto.VMDefaults{}
			for _, vm := range vms {
				got[vm.GetName()] = vm.GetDefaults()
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("SetVMDefaults(%v) mismatch (-want +got):\n%s", tc.annotations, diff)
			}
		})
	}
}
