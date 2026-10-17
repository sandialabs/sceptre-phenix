package vm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// useCaptureVM makes test-vm the running experiment's one VM, with IF0 and IF1 on EXP_1 and EXP_2 and
// minimega reporting the given networks, and returns minimega.
func useCaptureVM(t *testing.T, fake *fakeMM, networks ...string) *fakeMM {
	t.Helper()

	useExperiment(t, startTime, vmNode(
		"test-vm", vmIface("IF0", "EXP_1", ""), vmIface("IF1", "EXP_2", ""),
	))

	reported := runningVM("test-vm", networks...)
	fake.vms = mm.VMs{reported}

	return useMM(t, fake)
}

func TestStartCapture(t *testing.T) {
	boom := errors.New("boom")

	given := runningVM("test-vm", "EXP_1 (101)", "EXP_2 (102)")

	stopped := given
	stopped.State, stopped.Running = "PAUSED", false

	tests := []struct {
		name       string
		forVM      bool   // start with StartCaptureForVM and details, not StartCapture
		details    *mm.VM // the VM details given to StartCaptureForVM
		exp, vm    string
		iface      int
		out        string
		reported   mm.VMs // minimega's report, default test-vm running on EXP_1 and EXP_2
		startErr   error
		wantErr    error
		wantStarts int
	}{
		{
			name: "starts a capture on the interface", exp: testExp, vm: "test-vm", iface: 1, out: "out",
			wantStarts: 1,
		},
		{name: "rejects an empty experiment name", vm: "test-vm", out: "out.pcap", wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, out: "out.pcap", wantErr: errAny},
		{name: "rejects an empty output file", exp: testExp, vm: "test-vm", wantErr: errAny},
		{
			name: "rejects a VM that is not running", exp: testExp, vm: "test-vm", out: "out.pcap",
			reported: mm.VMs{stopped}, wantErr: errAny,
		},
		{
			name: "rejects an interface below 0", exp: testExp, vm: "test-vm", iface: -1, out: "out.pcap",
			wantErr: errAny,
		},
		{
			name: "rejects an interface past the last", exp: testExp, vm: "test-vm", iface: 2, out: "out.pcap",
			wantErr: errAny,
		},
		{
			name: "rejects a disconnected interface", exp: testExp, vm: "test-vm", iface: 1, out: "out.pcap",
			reported: mm.VMs{runningVM("test-vm", "EXP_1 (101)", "disconnected")}, wantErr: errAny,
		},
		{
			name: "returns minimega's error", exp: testExp, vm: "test-vm", out: "out.pcap",
			startErr: boom, wantErr: boom, wantStarts: 1,
		},
		{
			name:  "uses the VM details it is given without looking the VM up",
			forVM: true, details: &given, exp: testExp, vm: "test-vm", iface: 1, out: "out.pcap",
			reported: mm.VMs{}, wantStarts: 1,
		},
		{
			name:  "rejects missing VM details",
			forVM: true, exp: testExp, vm: "test-vm", out: "out.pcap", wantErr: errAny,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useCaptureVM(t, &fakeMM{startErr: test.startErr}, "EXP_1 (101)", "EXP_2 (102)")
			if test.reported != nil {
				fake.vms = test.reported
			}

			args := fmt.Sprintf("%q, %q, %d, %q", test.exp, test.vm, test.iface, test.out)
			call := "StartCapture(" + args + ")"

			var err error

			if test.forVM {
				details := "VM details"
				if test.details == nil {
					details = "nil"
				}

				call = "StartCaptureForVM(" + details + ", " + args + ")"
				err = vm.StartCaptureForVM(t.Context(), test.details, test.exp, test.vm, test.iface, test.out)
			} else {
				err = vm.StartCapture(t.Context(), test.exp, test.vm, test.iface, test.out)
			}

			if !errMatches(err, test.wantErr) {
				t.Errorf("%s error = %v, want %v", call, err, test.wantErr)
			}

			if fake.starts != test.wantStarts {
				t.Errorf("%s minimega capture starts = %d, want %d", call, fake.starts, test.wantStarts)
			}
		})
	}
}

func TestStopCaptures(t *testing.T) {
	boom := errors.New("boom")

	both := []mm.Capture{
		{VM: "test-vm", Interface: 0, Filepath: "test-experiment/files/0.pcap"},
		{VM: "test-vm", Interface: 1, Filepath: "test-experiment/files/1.pcap"},
	}

	tests := []struct {
		name      string
		one       bool // stop iface's capture with StopCapture, not all with StopCaptures
		iface     int
		exp, vm   string
		captures  []mm.Capture
		stopErr   error
		wantErr   error
		wantStops int
	}{
		{name: "stops every interface's capture", exp: testExp, vm: "test-vm", captures: both, wantStops: 1},
		{name: "stops one interface's capture", one: true, iface: 1, exp: testExp, vm: "test-vm", captures: both, wantStops: 1},
		{name: "rejects an empty experiment name", vm: "test-vm", captures: both, wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, captures: both, wantErr: errAny},
		{
			name: "rejects an empty experiment name for one interface", one: true, vm: "test-vm", captures: both,
			wantErr: errAny,
		},
		{
			name: "rejects an empty VM name for one interface", one: true, exp: testExp, captures: both,
			wantErr: errAny,
		},
		{
			name: "rejects an interface below 0", one: true, iface: -1, exp: testExp, vm: "test-vm", captures: both,
			wantErr: errAny,
		},
		{name: "reports a VM with no captures", exp: testExp, vm: "test-vm", wantErr: vm.ErrNoCaptures},
		{
			name: "reports an interface with no capture, leaving the others",
			one:  true, exp: testExp, vm: "test-vm", captures: both[1:], wantErr: vm.ErrNoCaptures,
		},
		{
			name: "returns minimega's error", exp: testExp, vm: "test-vm", captures: both,
			stopErr: boom, wantErr: boom, wantStops: 1,
		},
		{
			name: "returns minimega's error for one interface", one: true, exp: testExp, vm: "test-vm",
			captures: both, stopErr: boom, wantErr: boom, wantStops: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useCaptureVM(t, &fakeMM{captures: test.captures, stopErr: test.stopErr})

			var (
				call string
				err  error
			)

			if test.one {
				call = fmt.Sprintf("StopCapture(%q, %q, %d)", test.exp, test.vm, test.iface)
				err = vm.StopCapture(t.Context(), test.exp, test.vm, test.iface)
			} else {
				call = fmt.Sprintf("StopCaptures(%q, %q)", test.exp, test.vm)
				err = vm.StopCaptures(t.Context(), test.exp, test.vm)
			}

			if !errMatches(err, test.wantErr) {
				t.Errorf("%s error = %v, want %v", call, err, test.wantErr)
			}

			if fake.stops != test.wantStops {
				t.Errorf("%s minimega capture stops = %d, want %d", call, fake.stops, test.wantStops)
			}
		})
	}
}

func TestResolveInterface(t *testing.T) {
	inOrder := []string{"EXP_1 (101)", "EXP_2 (102)"}

	tests := []struct {
		name     string
		networks []string // minimega's order of the VM's networks
		id       string
		want     int
		wantErr  bool
	}{
		{name: "resolves an index", networks: inOrder, id: "1", want: 1},
		{name: "resolves a name", networks: inOrder, id: "IF1", want: 1},
		{name: "resolves a name in any case", networks: inOrder, id: "if1", want: 1},
		{
			name: "resolves a name to minimega's index", networks: []string{"EXP_2 (102)", "EXP_1 (101)"},
			id: "IF0", want: 1,
		},
		{name: "rejects an empty identifier", networks: inOrder, id: "", wantErr: true},
		{name: "rejects an index below 0", networks: inOrder, id: "-1", wantErr: true},
		{name: "rejects an index past the last", networks: inOrder, id: "2", wantErr: true},
		{name: "rejects an unknown name", networks: inOrder, id: "bogus", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCaptureVM(t, &fakeMM{}, test.networks...)

			v, err := vm.Get(t.Context(), testExp, "test-vm")
			if err != nil {
				t.Fatalf("Get(%q, %q) error = %v", testExp, "test-vm", err)
			}

			got, err := vm.ResolveInterface(v, test.id)

			switch {
			case test.wantErr && err == nil:
				t.Errorf("ResolveInterface(test-vm, %q) = %d, nil; want an error", test.id, got)
			case !test.wantErr && err != nil:
				t.Errorf("ResolveInterface(test-vm, %q) error = %v, want nil", test.id, err)
			case got != test.want:
				t.Errorf("ResolveInterface(test-vm, %q) = %d, want %d", test.id, got, test.want)
			}
		})
	}

	if got, err := vm.ResolveInterface(nil, "0"); err == nil {
		t.Errorf("ResolveInterface(nil, %q) = %d, nil; want an error", "0", got)
	}
}

// useSubnetVMs makes VMs a-d, running unless stopped: a has two 10.0.0.0/24 addresses and one outside, c one
// in it, b and d one each in 10.0.1.0/24; minimega reports c paused and a-c capturing.
func useSubnetVMs(t *testing.T, stopped bool, fake *fakeMM) *fakeMM {
	t.Helper()

	started := startTime
	if stopped {
		started = ""
	}

	useExperiment(t, started,
		vmNode("a",
			vmIface("IF0", "EXP_1", "10.0.0.1"),
			vmIface("IF1", "EXP_2", "10.0.0.2"),
			vmIface("IF2", "EXP_3", "192.168.0.1"),
		),
		vmNode("b", vmIface("IF0", "EXP_4", "10.0.1.3")),
		vmNode("c", vmIface("IF0", "EXP_1", "10.0.0.4")),
		vmNode("d", vmIface("IF0", "EXP_4", "10.0.1.5")),
	)

	a := runningVM("a", "EXP_1 (101)", "EXP_2 (102)", "EXP_3 (103)")
	a.Captures = []mm.Capture{{VM: "a", Interface: 0}, {VM: "a", Interface: 1}}

	b := runningVM("b", "EXP_4 (104)")
	b.Captures = []mm.Capture{{VM: "b", Interface: 0}}

	c := runningVM("c", "EXP_1 (101)")
	c.State, c.Running, c.Captures = "PAUSED", false, []mm.Capture{{VM: "c", Interface: 0}}

	fake.vms = mm.VMs{a, b, c, runningVM("d", "EXP_4 (104)")}

	return useMM(t, fake)
}

func TestCaptureSubnet(t *testing.T) {
	var (
		a0 = mm.Capture{VM: "a", Interface: 0, Filepath: "a0"}
		a1 = mm.Capture{VM: "a", Interface: 1, Filepath: "a1"}
		b0 = mm.Capture{VM: "b", Interface: 0, Filepath: "b0"}
		d0 = mm.Capture{VM: "d", Interface: 0, Filepath: "d0"}
		z0 = mm.Capture{VM: "z", Interface: 0, Filepath: "z0"}
	)

	tests := []struct {
		name       string
		stopped    bool
		subnet     string
		vms        []string
		startErr   error
		want       []mm.Capture
		wantStarts int
		wantErr    error
	}{
		{
			name:   "captures each interface in the subnet and lists each capture once",
			subnet: "10.0.0.0/24", want: []mm.Capture{a0, a1}, wantStarts: 2,
		},
		{
			name:   "lists the captures of every VM in the subnet, in the VMs' order",
			subnet: "10.0.0.0/16", want: []mm.Capture{a0, a1, b0, d0}, wantStarts: 4,
		},
		{
			name:   "captures only the listed VMs that are running",
			subnet: "10.0.0.0/16", vms: []string{"b", "c"}, want: []mm.Capture{b0}, wantStarts: 1,
		},
		{name: "captures nothing outside the subnet", subnet: "172.16.0.0/12"},
		{
			name:   "lists no captures when minimega fails to start them",
			subnet: "10.0.0.0/24", startErr: errors.New("boom"), wantStarts: 2,
		},
		{name: "rejects an invalid subnet", subnet: "10.0.0.0", wantErr: errAny},
		{name: "rejects a stopped experiment", stopped: true, subnet: "10.0.0.0/24", wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useSubnetVMs(t, test.stopped, &fakeMM{
				captures: []mm.Capture{b0, a0, z0, a1, d0},
				startErr: test.startErr,
			})

			got, err := vm.CaptureSubnet(t.Context(), testExp, test.subnet, test.vms)
			if !errMatches(err, test.wantErr) {
				t.Errorf("CaptureSubnet(%q, %q, %q) error = %v, want %v", testExp, test.subnet, test.vms, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("CaptureSubnet(%q, %q, %q) mismatch (-want +got):\n%s", testExp, test.subnet, test.vms, diff)
			}

			if fake.starts != test.wantStarts {
				t.Errorf("CaptureSubnet(%q, %q, %q) minimega capture starts = %d, want %d",
					testExp, test.subnet, test.vms, fake.starts, test.wantStarts)
			}

			// one listing of the experiment's captures, if any started
			wantListings := 0
			if test.want != nil {
				wantListings = 1
			}

			if fake.captureListings != wantListings {
				t.Errorf("CaptureSubnet(%q, %q, %q) minimega capture listings = %d, want %d",
					testExp, test.subnet, test.vms, fake.captureListings, wantListings)
			}
		})
	}
}

func TestStopCaptureSubnet(t *testing.T) {
	tests := []struct {
		name      string
		stopped   bool
		subnet    string
		vms       []string
		stopErr   error
		want      []string
		wantStops int
		wantErr   error
	}{
		{
			name: "stops every running VM's captures without a subnet",
			want: []string{"a", "b"}, wantStops: 2,
		},
		{
			name:   "stops the captures of each VM in the subnet once",
			subnet: "10.0.0.0/24", want: []string{"a"}, wantStops: 1,
		},
		{
			name: "stops only the listed VMs' captures",
			vms:  []string{"b", "c", "d"}, want: []string{"b"}, wantStops: 1,
		},
		{name: "stops nothing outside the subnet", subnet: "172.16.0.0/12"},
		{
			name:   "leaves out VMs whose captures minimega fails to stop",
			subnet: "10.0.0.0/24", stopErr: errors.New("boom"), wantStops: 1,
		},
		{name: "rejects a stopped experiment", stopped: true, wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useSubnetVMs(t, test.stopped, &fakeMM{
				captures: []mm.Capture{{VM: "a", Interface: 0}},
				stopErr:  test.stopErr,
			})

			got, err := vm.StopCaptureSubnet(t.Context(), testExp, test.subnet, test.vms)
			if !errMatches(err, test.wantErr) {
				t.Errorf("StopCaptureSubnet(%q, %q, %q) error = %v, want %v", testExp, test.subnet, test.vms, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("StopCaptureSubnet(%q, %q, %q) mismatch (-want +got):\n%s", testExp, test.subnet, test.vms, diff)
			}

			if fake.stops != test.wantStops {
				t.Errorf("StopCaptureSubnet(%q, %q, %q) minimega capture stops = %d, want %d",
					testExp, test.subnet, test.vms, fake.stops, test.wantStops)
			}

			// the VM list already holds each VM's captures
			if fake.vmCaptureListings != 0 {
				t.Errorf("StopCaptureSubnet(%q, %q, %q) minimega VM capture listings = %d, want 0",
					testExp, test.subnet, test.vms, fake.vmCaptureListings)
			}
		})
	}
}

// Stopping a VM's captures lists them once, however many it stops.
func TestStopCapturesListsCapturesOnce(t *testing.T) {
	for name, stop := range map[string]func(ctx context.Context) error{
		"StopCaptures": func(ctx context.Context) error { return vm.StopCaptures(ctx, testExp, "test-vm") },
		"StopCapture":  func(ctx context.Context) error { return vm.StopCapture(ctx, testExp, "test-vm", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			useExperiment(t, startTime, vmNode("test-vm", vmIface("IF0", "EXP_1", ""), vmIface("IF1", "EXP_2", "")))

			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

			mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base != "capture" {
					return nil
				}

				return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"},
					[]string{"test-vm:0", "/0.pcap"}, []string{"test-vm:1", "/1.pcap"})}
			})

			if err := stop(t.Context()); err != nil {
				t.Fatalf("%s(%q, test-vm) error = %v", name, testExp, err)
			}

			var listings int

			for _, base := range mmtest.Bases(received()) {
				if base == "capture" {
					listings++
				}
			}

			if listings != 1 {
				t.Errorf("%s(%q, test-vm) capture listings = %d, want 1 (minimega received %q)",
					name, testExp, listings, mmtest.Bases(received()))
			}
		})
	}
}

// Subnet captures stop trying once their context is done, and a failure as it ends stops the rest.
func TestSubnetCapturesGiveUpWithTheirContext(t *testing.T) {
	calls := map[string]func(ctx context.Context) error{
		"CaptureSubnet": func(ctx context.Context) error {
			_, err := vm.CaptureSubnet(ctx, testExp, "10.0.0.0/24", nil)

			return err
		},
		"StopCaptureSubnet": func(ctx context.Context) error {
			_, err := vm.StopCaptureSubnet(ctx, testExp, "", nil)

			return err
		},
	}

	for name, call := range calls {
		t.Run(name+" after its context ended", func(t *testing.T) {
			fake := useSubnetVMs(t, false, &fakeMM{captures: []mm.Capture{{VM: "a", Interface: 0}}})

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := call(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("%s(canceled context) error = %v, want %v", name, err, context.Canceled)
			}

			if got := fake.starts + fake.stops; got != 0 {
				t.Errorf("%s(canceled context) minimega capture calls = %d, want 0", name, got)
			}
		})

		t.Run(name+" failing as its context ends", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())

			boom := errors.New("boom")
			fake := useSubnetVMs(t, false, &fakeMM{
				captures: []mm.Capture{{VM: "a", Interface: 0}},
				startErr: boom, stopErr: boom, onCapture: cancel,
			})

			if err := call(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("%s(context canceled by the first capture) error = %v, want %v", name, err, context.Canceled)
			}

			if got := fake.starts + fake.stops; got != 1 {
				t.Errorf("%s(context canceled by the first capture) minimega capture calls = %d, want 1", name, got)
			}
		})
	}
}
