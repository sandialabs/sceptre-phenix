package vm_test

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/mm"
)

// webNode is the topology node of the VM web, with the given drives.
func webNode(drives ...map[string]any) map[string]any {
	return map[string]any{
		"type":        "VirtualMachine",
		"labels":      map[string]any{"role": "web"},
		"annotations": map[string]any{"vncBanner": "web"},
		"general":     map[string]any{"hostname": "web", "description": "web server"},
		"hardware": map[string]any{
			"vcpus": 2, "memory": 512, "os_type": "linux", "drives": drives,
		},
		"network": map[string]any{"interfaces": []map[string]any{
			vmIface("IF0", "EXP_1", "10.0.0.1"),
			vmIface("IF1", "EXP_2", ""),
		}},
	}
}

func TestGetAndList(t *testing.T) {
	var (
		drive  = map[string]any{"image": "/images/web.qc2", "inject_partition": 2}
		labels = map[string]string{"role": "web"}

		// what the topology configures, which is all List and Get report while
		// the experiment is stopped
		configured = mm.VM{
			Name:            "web",
			Description:     "web server",
			Type:            "VirtualMachine",
			Experiment:      testExp,
			Host:            "compute2", // its schedule
			IPv4:            []string{"10.0.0.1", ""},
			CPUs:            2,
			RAM:             512,
			Disk:            "/images/web.qc2",
			InjectPartition: 2,
			OSType:          "linux",
			Networks:        []string{"EXP_1", "EXP_2"},
			Tags:            labels,
			Interfaces:      map[string]string{"EXP_1": "10.0.0.1", "EXP_2": ""},
			IfaceNames:      []string{"IF0", "IF1"},
		}

		// minimega's report of the running VM, which lists its interfaces in
		// the reverse of the topology's order
		reported = mm.VM{
			Name:     "web",
			Host:     "compute1",
			State:    "RUNNING",
			Running:  true,
			Networks: []string{"EXP_2 (102)", "EXP_1 (101)"},
			Taps:     []string{"mega_tap1", "mega_tap0"},
			CPUs:     4,
			RAM:      1024,
			Disk:     "/tmp/web-snapshot.qc2",
			Uptime:   60,
		}
	)

	running := configured
	running.Host, running.State, running.Running = reported.Host, reported.State, true
	running.Networks, running.Taps, running.Tags = reported.Networks, reported.Taps, nil
	running.CPUs, running.RAM, running.Disk, running.Uptime = 4, 1024, reported.Disk, 60
	running.IPv4 = []string{"", "10.0.0.1"}
	running.IfaceNames = []string{"IF1", "IF0"}

	withoutDisk := func(v mm.VM) mm.VM {
		v.Disk, v.InjectPartition = "", 0

		return v
	}

	tests := []struct {
		name      string
		startTime string
		drives    []map[string]any
		reported  mm.VM
		want      mm.VM // Get adds the VM's app metadata, labels and annotations
	}{
		{
			name:     "stopped experiment reports the topology's settings",
			drives:   []map[string]any{drive},
			reported: reported,
			want:     configured,
		},
		{
			name:      "running experiment adds minimega's state in its interface order",
			startTime: startTime,
			drives:    []map[string]any{drive},
			reported:  reported,
			want:      running,
		},
		{
			name:     "stopped VM without drives has no disk",
			reported: reported,
			want:     withoutDisk(configured),
		},
		{
			name:      "running VM without drives has no disk",
			startTime: startTime,
			reported:  withoutDisk(reported),
			want:      withoutDisk(running),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := useExperiment(t, test.startTime, webNode(test.drives...))
			stored.experiment.Spec["schedules"] = map[string]any{"web": "compute2"}
			stored.experiment.Spec["scenario"] = map[string]any{"apps": []map[string]any{{
				"name": "test-app",
				"hosts": []map[string]any{{
					"hostname": "web", "metadata": map[string]any{"key": "value"},
				}},
			}}}

			useMM(t, &fakeMM{vms: mm.VMs{test.reported}})

			wantGet := test.want
			wantGet.Metadata = map[string]any{"test-app": map[string]any{"key": "value"}}
			wantGet.Labels = labels
			wantGet.Annotations = map[string]any{"vncBanner": "web"}

			got, err := vm.Get(t.Context(), testExp, "web")
			if err != nil {
				t.Fatalf("Get(%q, %q) error = %v", testExp, "web", err)
			}

			if diff := cmp.Diff(wantGet, *got); diff != "" {
				t.Errorf("Get(%q, %q) mismatch (-want +got):\n%s", testExp, "web", diff)
			}

			listed, err := vm.List(t.Context(), testExp)
			if err != nil {
				t.Fatalf("List(%q) error = %v", testExp, err)
			}

			if diff := cmp.Diff([]mm.VM{test.want}, listed); diff != "" {
				t.Errorf("List(%q) mismatch (-want +got):\n%s", testExp, diff)
			}

			exp, err := experiment.Get(testExp)
			if err != nil {
				t.Fatalf("experiment.Get(%q) error = %v", testExp, err)
			}

			reads := stored.gets

			gotFor, err := vm.GetFor(t.Context(), exp, "web")
			if err != nil {
				t.Errorf("GetFor(%s, %q) error = %v", testExp, "web", err)
			} else if diff := cmp.Diff(wantGet, *gotFor); diff != "" {
				t.Errorf("GetFor(%s, %q) mismatch (-want +got):\n%s", testExp, "web", diff)
			}

			if diff := cmp.Diff(listed, vm.ListFor(t.Context(), exp)); diff != "" {
				t.Errorf("ListFor(%s) mismatch with List (-want +got):\n%s", testExp, diff)
			}

			if got := stored.gets - reads; got != 0 {
				t.Errorf("store reads by GetFor and ListFor = %d, want 0", got)
			}
		})
	}
}

// vmIfaceCount is the number of interfaces of the VM in TestGetFollowsMinimegaInterfaceOrder.
const vmIfaceCount = 4

// A running VM's interface names and addresses follow the order minimega reports its networks in,
// whatever that order is, with an address minimega reports taking the topology's place.
func TestGetFollowsMinimegaInterfaceOrder(t *testing.T) {
	ifaces := make([]map[string]any, vmIfaceCount)
	addresses := make([]string, vmIfaceCount)

	for i := range vmIfaceCount {
		if i%2 == 0 { // the others use DHCP
			addresses[i] = "10.0.0." + strconv.Itoa(i+1)
		}

		ifaces[i] = vmIface("IF"+strconv.Itoa(i), "EXP_"+strconv.Itoa(i), addresses[i])
	}

	useExperiment(t, startTime, vmNode("web", ifaces...))
	fake := useMM(t, &fakeMM{})

	rapid.Check(t, func(t *rapid.T) {
		order := rapid.Permutation([]int{0, 1, 2, 3}).Draw(t, "order")
		reported := rapid.SliceOfN(rapid.SampledFrom([]string{"", "192.168.0.9"}), vmIfaceCount, vmIfaceCount).Draw(t, "reported")

		details := runningVM("web")
		for _, i := range order {
			details.Networks = append(details.Networks, "EXP_"+strconv.Itoa(i)+" (10"+strconv.Itoa(i)+")")
		}

		if rapid.Bool().Draw(t, "reportsAddresses") {
			details.IPv4 = reported
		}

		fake.vms = mm.VMs{details}

		got, err := vm.Get(t.Context(), testExp, "web")
		if err != nil {
			t.Fatalf("Get(%q, %q) error = %v", testExp, "web", err)
		}

		wantNames := make([]string, vmIfaceCount)
		wantAddresses := make([]string, vmIfaceCount)

		for pos, i := range order {
			wantNames[pos] = "IF" + strconv.Itoa(i)
			wantAddresses[pos] = addresses[i]

			if details.IPv4 != nil && reported[pos] != "" {
				wantAddresses[pos] = reported[pos]
			}

			if idx, err := vm.ResolveInterface(got, "if"+strconv.Itoa(i)); err != nil || idx != pos {
				t.Errorf("ResolveInterface(Get(%v), %q) = %d, %v; want %d, nil", details.Networks, "if"+strconv.Itoa(i), idx, err, pos)
			}
		}

		if diff := cmp.Diff(wantNames, got.IfaceNames); diff != "" {
			t.Errorf("Get(%v).IfaceNames mismatch (-want +got):\n%s", details.Networks, diff)
		}

		if diff := cmp.Diff(wantAddresses, got.IPv4); diff != "" {
			t.Errorf("Get(%v).IPv4 mismatch (-want +got):\n%s", details.Networks, diff)
		}
	})
}

func TestGetAndListRejectUnknownNames(t *testing.T) {
	useExperiment(t, "", vmNode("web"))
	useMM(t, &fakeMM{})

	exp, err := experiment.Get(testExp)
	if err != nil {
		t.Fatalf("experiment.Get(%q) error = %v", testExp, err)
	}

	tests := []struct {
		name, exp, vm string
	}{
		{name: "rejects an empty experiment name", exp: "", vm: "web"},
		{name: "rejects an experiment the store does not have", exp: "missing", vm: "web"},
		{name: "rejects an empty VM name", exp: testExp, vm: ""},
		{name: "rejects a VM the topology does not have", exp: testExp, vm: "missing"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := vm.Get(t.Context(), test.exp, test.vm); err == nil {
				t.Errorf("Get(%q, %q) = %+v, nil; want an error", test.exp, test.vm, got)
			}

			if test.exp != testExp {
				if got, err := vm.List(t.Context(), test.exp); err == nil {
					t.Errorf("List(%q) = %+v, nil; want an error", test.exp, got)
				}

				return
			}

			if got, err := vm.GetFor(t.Context(), exp, test.vm); err == nil {
				t.Errorf("GetFor(%s, %q) = %+v, nil; want an error", testExp, test.vm, got)
			}
		})
	}
}

func TestListOfRunningExperiment(t *testing.T) {
	dnb := vmNode("dnb")
	dnb["general"] = map[string]any{"hostname": "dnb", "do_not_boot": true}

	external := vmNode("ext")
	external["external"] = true

	tests := []struct {
		name      string
		startTime string
		reported  mm.VMs
		want      []string // name/state/host of each VM List lists
	}{
		{
			name:      "lists the VMs minimega reports, not to boot, or external",
			startTime: startTime,
			reported:  mm.VMs{runningVM("web")},
			want:      []string{"web/RUNNING/compute1", "dnb//", "ext/EXTERNAL/"},
		},
		{
			name:      "leaves out a VM minimega does not report",
			startTime: startTime,
			want:      []string{"dnb//", "ext/EXTERNAL/"},
		},
		{
			name:      "lists every VM of a dry run",
			startTime: startTime + "-DRYRUN",
			want:      []string{"web//compute2", "dnb//", "ext/EXTERNAL/"},
		},
	}

	states := func(vms []mm.VM) []string {
		got := make([]string, 0, len(vms))

		for _, v := range vms {
			got = append(got, v.Name+"/"+v.State+"/"+v.Host)
		}

		return got
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := useExperiment(t, test.startTime, vmNode("web"), dnb, external)
			stored.experiment.Spec["schedules"] = map[string]any{"web": "compute2"}
			useMM(t, &fakeMM{vms: test.reported})

			listed, err := vm.List(t.Context(), testExp)
			if err != nil {
				t.Fatalf("List(%q) error = %v", testExp, err)
			}

			if diff := cmp.Diff(test.want, states(listed)); diff != "" {
				t.Errorf("List(%q) name/state/host mismatch (-want +got):\n%s", testExp, diff)
			}

			exp, err := experiment.Get(testExp)
			if err != nil {
				t.Fatalf("experiment.Get(%q) error = %v", testExp, err)
			}

			// ListConfigured keeps every VM, without minimega's state
			want := []string{"web//compute2", "dnb//", "ext/EXTERNAL/"}
			if diff := cmp.Diff(want, states(vm.ListConfigured(*exp))); diff != "" {
				t.Errorf("ListConfigured(%s) name/state/host mismatch (-want +got):\n%s", testExp, diff)
			}

			if n, err := vm.Count(testExp); err != nil || n != 3 {
				t.Errorf("Count(%q) = %d, %v; want 3, nil", testExp, n, err)
			}
		})
	}
}

func TestConnect(t *testing.T) {
	tests := []struct {
		name, exp, vm string
		wantErr       error
		wantConnects  int
	}{
		{name: "connects the interface through minimega", exp: testExp, vm: "web", wantConnects: 1},
		{name: "rejects an empty experiment name", exp: "", vm: "web", wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, vm: "", wantErr: errAny},
		{name: "rejects an experiment the store does not have", exp: "missing", vm: "web", wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, startTime, vmNode("web", vmIface("IF0", "EXP_1", "")))
			fake := useMM(t, &fakeMM{})

			if err := vm.Connect(test.exp, test.vm, 0, "EXP_2"); !errMatches(err, test.wantErr) {
				t.Errorf("Connect(%q, %q, 0, %q) error = %v, want %v", test.exp, test.vm, "EXP_2", err, test.wantErr)
			}

			if fake.connects != test.wantConnects {
				t.Errorf("Connect(%q, %q, 0, %q) minimega connects = %d, want %d",
					test.exp, test.vm, "EXP_2", fake.connects, test.wantConnects)
			}
		})
	}
}
