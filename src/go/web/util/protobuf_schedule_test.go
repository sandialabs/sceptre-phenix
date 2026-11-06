package util

import (
	"cmp"
	"math"
	"slices"
	"testing"
	"time"

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
		{
			VM: "vm-b", Interface: 2, Filepath: "/captures/b.pcapng", ID: "7", State: "full", Format: "pcapng",
			Packets: 12, KDrops: 3, Size: 4096, Error: "the file is full",
		},
	})

	want := []*proto.Capture{
		{Vm: "vm-a", Interface: 0, Filepath: "/captures/a.pcap"},
		{
			Vm: "vm-b", Interface: 2, Filepath: "/captures/b.pcapng", Id: "7", State: "full", Format: "pcapng",
			Packets: 12, Kdrops: 3, Size: 4096, Error: "the file is full",
		},
	}

	if diff := gocmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("CapturesToProtobuf mismatch (-want +got):\n%s", diff)
	}
}

// An experiment capture keeps its counters, with durations in milliseconds, and a stream warns of a skew of more than
// 500 ms either way.
func TestExperimentCaptureToProtobuf(t *testing.T) {
	t.Parallel()

	stream := func(host string, skew time.Duration) mm.CaptureStream {
		return mm.CaptureStream{Host: host, Bridge: "phenix", State: "running", CaptureCounters: mm.CaptureCounters{Skew: skew}}
	}

	got := ExperimentCaptureToProtobuf(mm.NamespaceCapture{
		ID: "3", State: "stalled", Path: "/f/exp/files/a.pcapng", Format: "pcapng",
		CaptureCounters: mm.CaptureCounters{Ports: 2, Packets: 9, Lag: 1500 * time.Microsecond, Skew: -2 * time.Millisecond},
		Streams: []mm.CaptureStream{
			stream("a", 500*time.Millisecond), stream("b", 501*time.Millisecond), stream("c", -600*time.Millisecond),
		},
	}, "a.pcapng")

	streamPB := func(host string, skewMs float64, warn bool) *proto.CaptureStream {
		return &proto.CaptureStream{
			Host: host, Bridge: "phenix", State: "running", Counters: &proto.CaptureCounters{SkewMs: skewMs}, SkewWarning: warn,
		}
	}

	want := &proto.ExperimentCapture{
		Id: "3", State: "stalled", Running: true, Path: "/f/exp/files/a.pcapng", File: "a.pcapng", Format: "pcapng",
		Vlans:    []string{},
		Counters: &proto.CaptureCounters{Ports: 2, Packets: 9, LagMs: 1.5, SkewMs: -2},
		Streams:  []*proto.CaptureStream{streamPB("a", 500, false), streamPB("b", 501, true), streamPB("c", -600, true)},
	}

	if diff := gocmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ExperimentCaptureToProtobuf mismatch (-want +got):\n%s", diff)
	}
}

func TestWithRoot(t *testing.T) {
	t.Parallel()

	want := map[string]any{"vms": []string{"a"}}
	if diff := gocmp.Diff(want, WithRoot("vms", []string{"a"})); diff != "" {
		t.Errorf("WithRoot(vms, [a]) mismatch (-want +got):\n%s", diff)
	}
}
