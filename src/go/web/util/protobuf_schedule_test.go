package util

import (
	"cmp"
	"math"
	"slices"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/structpb"

	"phenix/util/mm"
	"phenix/web/proto"
)

func TestExperimentScheduleToProtobuf(t *testing.T) {
	tests := map[string]struct {
		schedule map[string]string
		want     []*proto.Schedule
	}{
		"none": {nil, []*proto.Schedule{}},
		"each VM with its host": {
			map[string]string{"vm-b": "host-2", "vm-a": "host-1", "vm-c": "host-1"},
			[]*proto.Schedule{{Vm: "vm-a", Host: "host-1"}, {Vm: "vm-b", Host: "host-2"}, {Vm: "vm-c", Host: "host-1"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			exp := testExperiment(t, "")
			exp.Spec.SetSchedule(tc.schedule)

			got := ExperimentScheduleToProtobuf(exp)
			slices.SortFunc(got.GetSchedule(), func(a, b *proto.Schedule) int { return cmp.Compare(a.GetVm(), b.GetVm()) })

			want := &proto.ExperimentSchedule{Schedule: tc.want}
			if diff := gocmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ExperimentScheduleToProtobuf(%v) mismatch (-want +got):\n%s", tc.schedule, diff)
			}
		})
	}
}

func TestAnnotationsToProtobuf(t *testing.T) {
	t.Parallel()

	nested, err := structpb.NewStruct(map[string]any{
		"owner": "alice",
		"ports": []any{80.0, 443.0},
		"meta":  map[string]any{"enabled": true, "note": nil},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		annotations map[string]any
		want        *structpb.Struct
	}{
		"nil is an empty object":   {nil, &structpb.Struct{}},
		"empty is an empty object": {map[string]any{}, &structpb.Struct{}},
		"nested lists and maps": {
			map[string]any{
				"owner": "alice",
				"ports": []any{80, 443},
				"meta":  map[string]any{"enabled": true, "note": nil},
			},
			nested,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := AnnotationsToProtobuf(tc.annotations)
			if err != nil {
				t.Fatalf("AnnotationsToProtobuf(%v) error = %v, want nil", tc.annotations, err)
			}

			if diff := gocmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("AnnotationsToProtobuf(%v) mismatch (-want +got):\n%s", tc.annotations, diff)
			}
		})
	}
}

func TestAnnotationsToProtobufRefusesValuesJSONCannotHold(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]any{"NaN": math.NaN(), "func": func() {}, "channel": make(chan int)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, err := AnnotationsToProtobuf(map[string]any{"bad": value}); err == nil {
				t.Errorf("AnnotationsToProtobuf(%s) = %v, want an error", name, got)
			}
		})
	}
}

func TestCapturesToProtobuf(t *testing.T) {
	t.Parallel()

	got := CapturesToProtobuf([]mm.Capture{
		{VM: "vm-a", Interface: 0, Filepath: "/captures/a.pcap"},
		{VM: "vm-b", Interface: 2, Filepath: "/captures/b.pcap"},
	})

	want := []*proto.Capture{
		{Vm: "vm-a", Interface: 0, Filepath: "/captures/a.pcap"},
		{Vm: "vm-b", Interface: 2, Filepath: "/captures/b.pcap"},
	}

	if diff := gocmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("CapturesToProtobuf mismatch (-want +got):\n%s", diff)
	}
}

func TestWithRoot(t *testing.T) {
	t.Parallel()

	want := map[string]any{"vms": []string{"a"}}
	if diff := gocmp.Diff(want, WithRoot("vms", []string{"a"})); diff != "" {
		t.Errorf("WithRoot(vms, [a]) mismatch (-want +got):\n%s", diff)
	}
}
