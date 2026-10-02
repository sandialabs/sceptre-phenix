package vm_test

import (
	"errors"
	"reflect"
	"testing"

	"phenix/api/vm"
	"phenix/util/mm"
)

// useCaptureVM makes the running experiment's one VM test-vm, whose topology
// interfaces IF0 and IF1 are on EXP_1 and EXP_2 and which minimega reports on
// the given networks, and returns minimega.
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

	// forVM starts a capture with StartCaptureForVM and the given VM details
	forVM := func(v *mm.VM) func(string, string, int, string) error {
		return func(exp, name string, iface int, out string) error {
			return vm.StartCaptureForVM(v, exp, name, iface, out)
		}
	}

	given := runningVM("test-vm", "EXP_1 (101)", "EXP_2 (102)")

	stopped := given
	stopped.State, stopped.Running = "PAUSED", false

	tests := []struct {
		name       string
		start      func(exp, vm string, iface int, out string) error // default StartCapture
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
			start: forVM(&given), exp: testExp, vm: "test-vm", iface: 1, out: "out.pcap",
			reported: mm.VMs{}, wantStarts: 1,
		},
		{
			name:  "rejects missing VM details",
			start: forVM(nil), exp: testExp, vm: "test-vm", out: "out.pcap", wantErr: errAny,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useCaptureVM(t, &fakeMM{startErr: test.startErr}, "EXP_1 (101)", "EXP_2 (102)")
			if test.reported != nil {
				fake.vms = test.reported
			}

			start := test.start
			if start == nil {
				start = vm.StartCapture
			}

			checkErr(t, start(test.exp, test.vm, test.iface, test.out), test.wantErr)

			if fake.starts != test.wantStarts {
				t.Errorf("started %d captures, want %d", fake.starts, test.wantStarts)
			}
		})
	}
}

func TestStopCaptures(t *testing.T) {
	boom := errors.New("boom")

	// one stops the capture on one interface with StopCapture
	one := func(iface int) func(exp, vm string) error {
		return func(exp, name string) error { return vm.StopCapture(exp, name, iface) }
	}

	both := []mm.Capture{
		{VM: "test-vm", Interface: 0, Filepath: "test-experiment/files/0.pcap"},
		{VM: "test-vm", Interface: 1, Filepath: "test-experiment/files/1.pcap"},
	}

	tests := []struct {
		name      string
		stop      func(exp, vm string) error // default StopCaptures
		exp, vm   string
		captures  []mm.Capture
		stopErr   error
		wantErr   error
		wantStops int
	}{
		{name: "stops every interface's capture", exp: testExp, vm: "test-vm", captures: both, wantStops: 1},
		{name: "stops one interface's capture", stop: one(1), exp: testExp, vm: "test-vm", captures: both, wantStops: 1},
		{name: "rejects an empty experiment name", vm: "test-vm", captures: both, wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, captures: both, wantErr: errAny},
		{
			name: "rejects an empty experiment name for one interface", stop: one(0), vm: "test-vm", captures: both,
			wantErr: errAny,
		},
		{
			name: "rejects an empty VM name for one interface", stop: one(0), exp: testExp, captures: both,
			wantErr: errAny,
		},
		{name: "rejects an interface below 0", stop: one(-1), exp: testExp, vm: "test-vm", captures: both, wantErr: errAny},
		{name: "reports a VM with no captures", exp: testExp, vm: "test-vm", wantErr: vm.ErrNoCaptures},
		{
			name: "reports an interface with no capture, leaving the others",
			stop: one(0), exp: testExp, vm: "test-vm", captures: both[1:], wantErr: vm.ErrNoCaptures,
		},
		{
			name: "returns minimega's error", exp: testExp, vm: "test-vm", captures: both,
			stopErr: boom, wantErr: boom, wantStops: 1,
		},
		{
			name: "returns minimega's error for one interface", stop: one(0), exp: testExp, vm: "test-vm",
			captures: both, stopErr: boom, wantErr: boom, wantStops: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useCaptureVM(t, &fakeMM{captures: test.captures, stopErr: test.stopErr})

			stop := test.stop
			if stop == nil {
				stop = vm.StopCaptures
			}

			checkErr(t, stop(test.exp, test.vm), test.wantErr)

			if fake.stops != test.wantStops {
				t.Errorf("stopped captures %d times, want %d", fake.stops, test.wantStops)
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

			v, err := vm.Get(testExp, "test-vm")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			got, err := vm.ResolveInterface(v, test.id)
			if (err != nil) != test.wantErr || got != test.want {
				t.Errorf("ResolveInterface(%q) = %d, %v, want %d (error %t)",
					test.id, got, err, test.want, test.wantErr)
			}
		})
	}

	if _, err := vm.ResolveInterface(nil, "0"); err == nil {
		t.Error("ResolveInterface accepted missing VM details")
	}
}

// Captures started and stopped by interface name, as the CLI does.
func TestCaptureByInterfaceName(t *testing.T) {
	fake := useCaptureVM(t, &fakeMM{
		captures: []mm.Capture{{VM: "test-vm", Interface: 0, Filepath: "test-experiment/files/out.pcap"}},
	}, "EXP_2 (102)", "EXP_1 (101)")

	v, err := vm.Get(testExp, "test-vm")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	iface, err := vm.ResolveInterface(v, "IF1")
	if err != nil {
		t.Fatalf("ResolveInterface: %v", err)
	}

	if err := vm.StartCaptureForVM(v, testExp, "test-vm", iface, "out.pcap"); err != nil {
		t.Fatalf("StartCaptureForVM: %v", err)
	}

	if err := vm.StopCapture(testExp, "test-vm", iface); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}

	if fake.starts != 1 || fake.stops != 1 {
		t.Errorf("started %d and stopped %d captures, want 1 each", fake.starts, fake.stops)
	}
}

// useSubnetVMs makes the experiment's VMs a, b, c and d, running unless
// stopped. a has two addresses in 10.0.0.0/24 and one outside it, c one in it,
// and b and d one each in 10.0.1.0/24. minimega reports c paused and the others
// running, and a, b and c capturing.
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

			got, err := vm.CaptureSubnet(testExp, test.subnet, test.vms)
			checkErr(t, err, test.wantErr)

			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("CaptureSubnet = %v, want %v", got, test.want)
			}

			if fake.starts != test.wantStarts {
				t.Errorf("started %d captures, want %d", fake.starts, test.wantStarts)
			}

			// one listing of the experiment's captures, if any started
			wantListings := 0
			if test.want != nil {
				wantListings = 1
			}

			if fake.captureListings != wantListings {
				t.Errorf("listed captures %d times, want %d", fake.captureListings, wantListings)
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

			got, err := vm.StopCaptureSubnet(testExp, test.subnet, test.vms)
			checkErr(t, err, test.wantErr)

			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("StopCaptureSubnet = %v, want %v", got, test.want)
			}

			if fake.stops != test.wantStops {
				t.Errorf("stopped captures %d times, want %d", fake.stops, test.wantStops)
			}
		})
	}
}
