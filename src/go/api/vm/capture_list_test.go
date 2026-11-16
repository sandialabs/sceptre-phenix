package vm_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vm"
	"phenix/util/mm"
)

// The capture lists are sorted by VM and interface, empty and not nil when
// there are none, and do not ask minimega about a stopped experiment.
func TestCaptureLists(t *testing.T) {
	captures := []mm.Capture{
		{VM: "web", Interface: 1, Filepath: "web-1.pcap"},
		{VM: "db", Interface: 0, Filepath: "db-0.pcap"},
		{VM: "web", Interface: 0, Filepath: "web-0.pcap"},
	}

	sorted := []mm.Capture{captures[1], captures[2], captures[0]}

	tests := []struct {
		name         string
		started      string
		captures     []mm.Capture
		want         []mm.Capture
		wantListings int
	}{
		{name: "a running experiment", started: startTime, captures: captures, want: sorted, wantListings: 1},
		{name: "no captures", started: startTime, want: []mm.Capture{}, wantListings: 1},
		{name: "a stopped experiment", captures: captures, want: []mm.Capture{}, wantListings: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useExperiment(t, tc.started, vmNode("web"), vmNode("db"))
			fake := useMM(t, &fakeMM{captures: tc.captures})

			got := vm.ExperimentCaptures(context.Background(), testExp)
			if diff := cmp.Diff(tc.want, got); diff != "" || got == nil {
				t.Errorf("ExperimentCaptures() = %v, mismatch (-want +got):\n%s", got, diff)
			}

			got = vm.Captures(context.Background(), testExp, "web")
			if diff := cmp.Diff(tc.want, got); diff != "" || got == nil {
				t.Errorf("Captures(web) = %v, mismatch (-want +got):\n%s", got, diff)
			}

			if fake.captureListings != tc.wantListings || fake.vmCaptureListings != tc.wantListings {
				t.Errorf("listings = %d and %d, want %d each", fake.captureListings, fake.vmCaptureListings, tc.wantListings)
			}

			if len(tc.captures) > 0 && tc.captures[0] != captures[0] {
				t.Errorf("the listing changed what minimega returned: %v", tc.captures)
			}
		})
	}
}
