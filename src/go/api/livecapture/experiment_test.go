package livecapture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/util/mm"
	"phenix/util/polltest"
)

// errListing is the error of a failed experiment capture listing.
var errListing = errors.New("minimega is busy")

// GetNamespaceCaptures lists experiment capture 3, written to interface 0's file, as running while the fake captures,
// and as stopped after. It fails while nsFails is above 0, one listing at a time.
func (c *fakeCluster) GetNamespaceCaptures(...mm.Option) ([]mm.NamespaceCapture, error) {
	c.nsLookups.Add(1)

	if c.nsFails.Add(-1) >= 0 {
		return nil, errListing
	}

	state := "stopped"
	if c.capturing.Load() {
		state = "running"
	}

	return []mm.NamespaceCapture{{ID: "3", State: state, Path: c.path(0), Format: mm.CaptureFormatPcap}}, nil
}

func (c *fakeCluster) Headnode() string { return "headnode" }

func TestOpenExperimentFollowsCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)

	var (
		header = pcapFile(binary.LittleEndian, 0xa1b2c3d4)
		one    = record(binary.LittleEndian, 60)
		two    = record(binary.LittleEndian, 90)
	)

	cluster.write(t, append(header, one...))

	tap, err := OpenExperiment(context.Background(), "exp", "3")
	if err != nil {
		t.Fatalf("OpenExperiment(exp, 3) error = %v", err)
	}

	defer tap.Release()

	want := Capture{Exp: "exp", ID: "3", Path: cluster.path(0), Host: "headnode"}
	if diff := cmp.Diff(want, tap.Capture()); diff != "" {
		t.Errorf("OpenExperiment(exp, 3).Capture() mismatch (-want +got):\n%s", diff)
	}

	// a VM interface's capture of the same file is another Tap
	vmTap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer vmTap.Release()

	if vmTap == tap {
		t.Error("Open(exp, vm, 0) = the Tap of OpenExperiment(exp, 3), want another Tap")
	}

	again, err := OpenExperiment(context.Background(), "exp", "3")
	if err != nil {
		t.Fatalf("OpenExperiment(exp, 3) again error = %v", err)
	}

	again.Release()

	if again != tap {
		t.Error("OpenExperiment(exp, 3) again = a new Tap, want the one that follows the capture")
	}

	s := collect(tap, false)

	cluster.write(t, two)
	waitFor(t, tap, "the second record", func(st State) bool {
		return st.Size == int64(len(header)+len(one)+len(two))
	})

	cluster.capturing.Store(false)

	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Stream(fromNow false) after the capture stops error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream(fromNow false) still running 5s after the capture stopped")
	}

	if diff := cmp.Diff(bytes.Join([][]byte{header, one, two}, nil), s.out.Bytes()); diff != "" {
		t.Errorf("Stream(fromNow false) bytes mismatch (-want +got):\n%s", diff)
	}
}

func TestOpenExperimentNotCapturing(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name      string
		id        string
		running   bool
		capturing bool
		lookups   int32
	}{
		{name: "an unknown ID", id: "9", running: true, capturing: true, lookups: 1},
		{name: "a stopped capture", id: "3", running: true, capturing: false, lookups: 1},
		{name: "a stopped experiment", id: "3", running: false, capturing: true, lookups: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := useFakeCluster(t, false)
			cluster.running.Store(test.running)
			cluster.capturing.Store(test.capturing)

			tap, err := OpenExperiment(context.Background(), "exp", test.id)
			if !errors.Is(err, ErrNotCapturing) {
				if tap != nil {
					tap.Release()
				}

				t.Fatalf("OpenExperiment(exp, %s) error = %v, want %v", test.id, err, ErrNotCapturing)
			}

			// minimega would make the namespace of a stopped experiment again
			if got := cluster.nsLookups.Load(); got != test.lookups {
				t.Errorf("OpenExperiment(exp, %s) listed the experiment captures %d times, want %d", test.id, got, test.lookups)
			}
		})
	}
}

// A VM interface capture that minimega lists as failed or full has ended.
func TestOpenEndedVMCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	for _, state := range []string{"failed", "full"} {
		t.Run(state, func(t *testing.T) {
			cluster := useFakeCluster(t, false)
			cluster.vmState.Store(state)

			tap, err := Open("exp", "vm", 0)
			if !errors.Is(err, ErrNotCapturing) {
				if tap != nil {
					tap.Release()
				}

				t.Fatalf("Open(exp, vm, 0) of a %s capture error = %v, want %v", state, err, ErrNotCapturing)
			}
		})
	}
}

// A reader from now on that joins while the capture file is still empty gets the header and every record written
// after it joined, even when they arrive together.
func TestTapStreamFromNowBeforeHeader(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer tap.Release()

	waitFor(t, tap, "the empty file", func(st State) bool { return st.CaughtUp })

	s := collect(tap, true)

	var (
		header = pcapFile(binary.LittleEndian, 0xa1b2c3d4)
		one    = record(binary.LittleEndian, 60)
		two    = record(binary.LittleEndian, 70)
		whole  = bytes.Join([][]byte{header, one, two}, nil)
	)

	cluster.write(t, whole)
	waitFor(t, tap, "the records", func(st State) bool { return st.Size == int64(len(whole)) })

	cluster.capturing.Store(false)

	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Stream(fromNow true) after the capture stops error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream(fromNow true) still running 5s after the capture stopped")
	}

	if diff := cmp.Diff(whole, s.out.Bytes()); diff != "" {
		t.Errorf("Stream(fromNow true) bytes mismatch (-want +got):\n%s", diff)
	}
}

// The key of experiment capture 3 differs from the key of interface 3 of a VM named capture.
func TestCaptureKeysDiffer(t *testing.T) {
	t.Parallel()

	vm := Capture{Exp: "exp", VM: "capture", Interface: 3, ID: "", Path: "", Host: ""}
	exp := Capture{Exp: "exp", VM: "", Interface: 0, ID: "3", Path: "", Host: ""}

	if vm.key() == exp.key() {
		t.Errorf("key() of VM capture interface 3 = key() of experiment capture 3 = %q, want different keys", vm.key())
	}
}

// A failed listing of the experiment captures does not end a Tap; maxReadErrors failures in a row do, as a successful
// listing without the capture does.
func TestTapOfExperimentCaptureListingFailures(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name  string
		fails int32
		// whether the Tap still follows the capture after the failed listings
		follows bool
	}{
		{name: "fewer failures than the limit", fails: maxReadErrors - 1, follows: true},
		{name: "as many failures as the limit", fails: maxReadErrors, follows: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := useFakeCluster(t, false)
			cluster.write(t, pcapFile(binary.LittleEndian, 0xa1b2c3d4))

			tap, err := OpenExperiment(context.Background(), "exp", "3")
			if err != nil {
				t.Fatalf("OpenExperiment(exp, 3) error = %v", err)
			}

			defer tap.Release()

			lookups := cluster.nsLookups.Load()
			cluster.nsFails.Store(test.fails)

			polltest.Until(t, "the failed listings and one more check", func() bool {
				return cluster.nsLookups.Load() > lookups+test.fails || tap.State().Done
			})

			st := tap.State()
			if st.Done == test.follows {
				t.Fatalf("State() after %d failed listings = done %t, %v; want done %t", test.fails, st.Done, st.Err, !test.follows)
			}

			if !test.follows {
				if !errors.Is(st.Err, errListing) {
					t.Errorf("State().Err after %d failed listings = %v, want %v", test.fails, st.Err, errListing)
				}

				return
			}

			// a successful listing of the stopped capture ends it
			cluster.capturing.Store(false)
			waitFor(t, tap, "the end of the capture", func(st State) bool { return st.Done })

			if st := tap.State(); st.Err != nil {
				t.Errorf("State().Err after the capture stopped = %v, want nil", st.Err)
			}
		})
	}
}
