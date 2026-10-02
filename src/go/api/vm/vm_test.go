package vm_test

import (
	"reflect"
	"testing"

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

			got, err := vm.Get(testExp, "web")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			if !reflect.DeepEqual(*got, wantGet) {
				t.Errorf("Get = %+v\nwant %+v", *got, wantGet)
			}

			listed, err := vm.List(testExp)
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			if !reflect.DeepEqual(listed, []mm.VM{test.want}) {
				t.Errorf("List = %+v\nwant %+v", listed, []mm.VM{test.want})
			}

			exp, err := experiment.Get(testExp)
			if err != nil {
				t.Fatalf("getting the experiment: %v", err)
			}

			reads := stored.gets

			gotFor, err := vm.GetFor(exp, "web")
			if err != nil || !reflect.DeepEqual(*gotFor, wantGet) {
				t.Errorf("GetFor = %+v, %v\nwant %+v", gotFor, err, wantGet)
			}

			if listedFor := vm.ListFor(exp); !reflect.DeepEqual(listedFor, listed) {
				t.Errorf("ListFor = %+v\nwant %+v", listedFor, listed)
			}

			if stored.gets != reads {
				t.Errorf("GetFor and ListFor read the store %d times, want none", stored.gets-reads)
			}
		})
	}
}

func TestGetAndListRejectUnknownNames(t *testing.T) {
	useExperiment(t, "", vmNode("web"))
	useMM(t, &fakeMM{})

	exp, err := experiment.Get(testExp)
	if err != nil {
		t.Fatalf("getting the experiment: %v", err)
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
			if got, err := vm.Get(test.exp, test.vm); err == nil {
				t.Errorf("Get = %+v, want an error", got)
			}

			if test.exp != testExp {
				if got, err := vm.List(test.exp); err == nil {
					t.Errorf("List = %+v, want an error", got)
				}

				return
			}

			if got, err := vm.GetFor(exp, test.vm); err == nil {
				t.Errorf("GetFor = %+v, want an error", got)
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

			listed, err := vm.List(testExp)
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			if got := states(listed); !reflect.DeepEqual(got, test.want) {
				t.Errorf("List = %q, want %q", got, test.want)
			}

			exp, err := experiment.Get(testExp)
			if err != nil {
				t.Fatalf("getting the experiment: %v", err)
			}

			// ListConfigured keeps every VM, without minimega's state
			want := []string{"web//compute2", "dnb//", "ext/EXTERNAL/"}
			if got := states(vm.ListConfigured(*exp)); !reflect.DeepEqual(got, want) {
				t.Errorf("ListConfigured = %q, want %q", got, want)
			}

			if n, err := vm.Count(testExp); err != nil || n != 3 {
				t.Errorf("Count = %d, %v, want 3", n, err)
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

			checkErr(t, vm.Connect(test.exp, test.vm, 0, "EXP_2"), test.wantErr)

			if fake.connects != test.wantConnects {
				t.Errorf("connected %d times, want %d", fake.connects, test.wantConnects)
			}
		})
	}
}
