package scheduler

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog/plogtest"
)

// builtinVM is a topology node: a VM with the given memory and CPUs (1 when
// 0) on VLAN vlan, or with no interfaces when vlan is empty. An external one
// is not a VM to schedule. A doNotBoot one is not booted at start.
type builtinVM struct {
	name, vlan   string
	memory, cpus int
	external     bool
	doNotBoot    bool
}

// builtinFailer is the part of a test, or of a rapid check, that builtinSpec
// uses.
type builtinFailer interface {
	Helper()
	Fatalf(format string, args ...any)
}

// builtinSpec is the spec of experiment exp with the given nodes and schedules.
func builtinSpec(t builtinFailer, vms []builtinVM, schedules map[string]any) ifaces.ExperimentSpec {
	t.Helper()

	nodes := make([]map[string]any, 0, len(vms))

	for _, vm := range vms {
		interfaces := []map[string]any{}
		if vm.vlan != "" {
			interfaces = append(interfaces, map[string]any{"name": "eth0", "vlan": vm.vlan})
		}

		cpus := vm.cpus
		if cpus == 0 {
			cpus = 1
		}

		node := map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": vm.name, "do_not_boot": vm.doNotBoot},
			"hardware": map[string]any{"vcpus": cpus, "memory": vm.memory, "os_type": "linux"},
			"network":  map[string]any{"interfaces": interfaces},
		}

		if vm.external {
			node["external"] = true
		}

		nodes = append(nodes, node)
	}

	if schedules == nil {
		schedules = map[string]any{}
	}

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"schedules":      schedules,
			"topology":       map[string]any{"nodes": nodes},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	return exp.Spec
}

// failingClusterMM fails to get the cluster hosts; other mm.MM methods panic.
type failingClusterMM struct {
	mm.MM
}

func (failingClusterMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	return nil, errors.New("minimega is not running")
}

// useClusterMM makes m the minimega that the schedulers ask until the test ends.
func useClusterMM(t *testing.T, m mm.MM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = m //nolint:reassign // install test double
}

// Each built-in scheduler has the name it is registered with and needs no
// options.
func TestBuiltinSchedulerNames(t *testing.T) {
	t.Parallel()

	for name, s := range schedulers {
		if s.Name() != name {
			t.Errorf("schedulers[%s].Name() = %q", name, s.Name())
		}

		if err := s.Init(); err != nil {
			t.Errorf("schedulers[%s].Init() error = %v", name, err)
		}
	}
}

// Each built-in scheduler fails for an experiment without VMs, or when it
// cannot get the cluster hosts.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestBuiltinSchedulerErrors(t *testing.T) {
	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment"} {
		t.Run(name+"/no VMs", func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1"}}})

			if _, err := Schedule(t.Context(), name, builtinSpec(t, nil, nil)); err == nil {
				t.Errorf("Schedule(%s, no VMs) = nil, want an error", name)
			}
		})

		t.Run(name+"/no cluster hosts", func(t *testing.T) {
			useClusterMM(t, failingClusterMM{})

			if _, err := Schedule(t.Context(), name, builtinSpec(t, []builtinVM{{name: "vm1", vlan: "EXP"}}, nil)); err == nil {
				t.Errorf("Schedule(%s, minimega not running) = nil, want an error", name)
			}
		})
	}
}

// isolate-experiment puts every VM on one host that has no VMs: the host of
// the first VM when that host is free, even if it becomes overloaded; else
// the free host with the most free memory that fits all VMs; else any free
// host. It fails when every host has VMs. It logs a warning when it
// overloads the first VM's host, or when that host is busy. It warns of an
// overcommitted host and of a VM set to a host that is not in the cluster.
//
//nolint:paralleltest // replaces mm.DefaultMM and captures the plog records
func TestIsolateExperiment(t *testing.T) {
	const (
		overloaded = "using host for isolated experiment; host may become overloaded"
		inUse      = "scheduled host is in use; not using it for isolated experiment"
	)

	var (
		big   = mm.Host{Name: "big", CPUs: 8, MemTotal: 8192}
		small = mm.Host{Name: "small", CPUs: 1, MemTotal: 600}
		busy  = mm.Host{Name: "busy", CPUs: 64, MemTotal: 65536, VMs: 1}
	)

	vms := []builtinVM{{name: "vm1", vlan: "EXP", memory: 512}, {name: "ext", external: true}, {name: "vm2", vlan: "EXP", memory: 512}}

	const smallOver = "host small: 1 GiB of memory committed, 600 MiB available; 2 CPUs committed, 1 available"

	tests := map[string]struct {
		hosts        mm.Hosts
		schedules    map[string]any
		wantErr      bool
		want         map[string]string
		wantLogs     []map[string]any
		wantWarnings []string
	}{
		"first VM's free host, overloaded": {
			hosts: mm.Hosts{big, small}, schedules: map[string]any{"vm1": "small"},
			want:         map[string]string{"vm1": "small", "vm2": "small"},
			wantLogs:     []map[string]any{{"level": "WARN", "type": "SYSTEM", "msg": overloaded, "host": "small", "exp": "exp"}},
			wantWarnings: []string{smallOver},
		},
		"first VM's host is busy": {
			hosts: mm.Hosts{big, busy}, schedules: map[string]any{"vm1": "busy"},
			want:     map[string]string{"vm1": "big", "vm2": "big"},
			wantLogs: []map[string]any{{"level": "WARN", "type": "SYSTEM", "msg": inUse, "host": "busy", "exp": "exp"}},
		},
		"first VM's host is not in the cluster": {
			hosts: mm.Hosts{big}, schedules: map[string]any{"vm1": "gone", "ext": "gone"},
			want: map[string]string{"vm1": "big", "vm2": "big", "ext": "gone"},
			wantWarnings: []string{
				"VM vm1 was set to host gone, which is not a schedulable cluster host; it goes to host big instead",
			},
		},
		"a free host where all VMs fit": {
			hosts: mm.Hosts{small, busy, big},
			want:  map[string]string{"vm1": "big", "vm2": "big"},
		},
		"a free host where the VMs do not fit": {
			hosts:        mm.Hosts{busy, small},
			want:         map[string]string{"vm1": "small", "vm2": "small"},
			wantWarnings: []string{smallOver},
		},
		"no free host": {hosts: mm.Hosts{busy}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: tc.hosts})
			logs := plogtest.Capture(t)

			spec := builtinSpec(t, vms, tc.schedules)

			warnings, err := Schedule(t.Context(), "isolate-experiment", spec)

			records := logs.Records(t, func(r map[string]any) bool { return r["msg"] == overloaded || r["msg"] == inUse })
			gotLogs := make([]map[string]any, 0, len(records))

			for _, r := range records {
				gotLogs = append(gotLogs, map[string]any{
					"level": r["level"], "type": r["type"], "msg": r["msg"], "host": r["host"], "exp": r["exp"],
				})
			}

			if diff := cmp.Diff(tc.wantLogs, gotLogs, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(isolate-experiment) log records mismatch (-want +got):\n%s", diff)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("Schedule(isolate-experiment) error = %v, want error %t", err, tc.wantErr)
			}

			if errors.Is(err, ErrNoSchedulableHosts) {
				t.Errorf("Schedule(isolate-experiment) error = %v, want one that is not %v", err, ErrNoSchedulableHosts)
			}

			if tc.wantErr {
				return
			}

			if diff := cmp.Diff(tc.want, spec.Schedules()); diff != "" {
				t.Errorf("Schedule(isolate-experiment) schedules mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(isolate-experiment) warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// subnet-compute puts a VM on a host that has a VM of the same VLAN and
// memory left for it, and otherwise on the host with the least committed
// memory, even when that host does not have the memory. It keeps a pin, and a
// VM without interfaces. It warns of an overcommitted host and of a pin to a
// host that is not in the cluster.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestSubnetCompute(t *testing.T) {
	h1 := mm.Host{Name: "h1", MemTotal: 4096}
	h2 := mm.Host{Name: "h2", MemTotal: 1024, MemCommit: 256}

	tests := map[string]struct {
		vms          []builtinVM
		schedules    map[string]any
		want         map[string]string
		wantWarnings []string
	}{
		"same VLAN on the same host": {
			vms: []builtinVM{
				{name: "vm1", vlan: "EXP", memory: 256}, {name: "vm2", vlan: "EXP", memory: 256},
				{name: "vm3", vlan: "OTHER", memory: 256}, {name: "ext", vlan: "EXP", external: true},
			},
			schedules: map[string]any{"vm1": "h2"},
			want:      map[string]string{"vm1": "h2", "vm2": "h2", "vm3": "h1"},
		},
		"same VLAN without memory left": {
			vms:       []builtinVM{{name: "vm1", vlan: "EXP", memory: 256}, {name: "vm2", vlan: "EXP", memory: 1024}},
			schedules: map[string]any{"vm1": "h2"},
			want:      map[string]string{"vm1": "h2", "vm2": "h1"},
		},
		"new VLANs by least committed memory": {
			vms: []builtinVM{
				{name: "vm1", vlan: "A", memory: 512},
				{name: "vm2", vlan: "B", memory: 512},
				{name: "vm3", vlan: "A", memory: 512},
			},
			want: map[string]string{"vm1": "h1", "vm2": "h2", "vm3": "h1"},
		},
		"a new VLAN on a host without the memory": {
			vms:          []builtinVM{{name: "vm1", vlan: "A", memory: 8192}},
			want:         map[string]string{"vm1": "h1"},
			wantWarnings: []string{"host h1: 8 GiB of memory committed, 4 GiB available"},
		},
		"scheduled to a host not in the cluster": {
			vms:          []builtinVM{{name: "vm1", vlan: "EXP", memory: 256}, {name: "vm2", vlan: "EXP", memory: 256}},
			schedules:    map[string]any{"vm1": "gone"},
			want:         map[string]string{"vm1": "gone", "vm2": "h1"},
			wantWarnings: []string{"VM vm1 stays on host gone, which is not a schedulable cluster host"},
		},
		"scheduled VM without interfaces": {
			vms: []builtinVM{{name: "vm1", memory: 256}}, schedules: map[string]any{"vm1": "h2"},
			want: map[string]string{"vm1": "h2"},
		},
		"VM without interfaces": {vms: []builtinVM{{name: "vm1", memory: 256}}, want: map[string]string{"vm1": "h1"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: mm.Hosts{h1, h2}})

			spec := builtinSpec(t, tc.vms, tc.schedules)

			warnings, err := Schedule(t.Context(), "subnet-compute", spec)
			if err != nil {
				t.Fatalf("Schedule(subnet-compute) error = %v", err)
			}

			if diff := cmp.Diff(tc.want, spec.Schedules()); diff != "" {
				t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(subnet-compute) warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// subnet-compute places the VMs without interfaces after the other VMs, each
// on the host that holds the most VMs of the experiment, pinned or placed,
// with ties taken by host name. It takes the next host in that order when
// that host does not have the memory, and the first host, with a warning,
// when no host has it. It keeps their pins and skips external nodes.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestSubnetComputeVMsWithoutInterfaces(t *testing.T) {
	roomy := mm.Hosts{{Name: "a", MemTotal: 4096}, {Name: "b", MemTotal: 4096}, {Name: "c", MemTotal: 4096}}

	tests := map[string]struct {
		hosts        mm.Hosts
		vms          []builtinVM
		schedules    map[string]any
		want         map[string]string
		wantWarnings []string
	}{
		"the host with the most VMs": {
			hosts: roomy,
			vms: []builtinVM{
				{name: "u1", memory: 256}, {name: "v1", vlan: "EXP", memory: 256}, {name: "v2", vlan: "EXP", memory: 256},
				{name: "ext", external: true}, {name: "ext2", external: true},
			},
			schedules: map[string]any{"v1": "b", "ext": "c", "ext2": "c"},
			want:      map[string]string{"u1": "b", "v1": "b", "v2": "b", "ext": "c", "ext2": "c"},
		},
		"after the VMs with interfaces": {
			hosts: roomy,
			vms:   []builtinVM{{name: "u1", memory: 256}, {name: "v1", vlan: "EXP", memory: 256}, {name: "v2", vlan: "EXP", memory: 256}},
			want:  map[string]string{"u1": "a", "v1": "a", "v2": "a"},
		},
		"ties by host name": {
			hosts: mm.Hosts{{Name: "c", MemTotal: 4096}, {Name: "a", MemTotal: 4096}},
			vms:   []builtinVM{{name: "u1", memory: 256}, {name: "u2", memory: 256}},
			want:  map[string]string{"u1": "a", "u2": "a"},
		},
		"a pinned VM without interfaces counts": {
			hosts:     roomy,
			vms:       []builtinVM{{name: "u1", memory: 256}, {name: "u2", memory: 256}},
			schedules: map[string]any{"u1": "c"},
			want:      map[string]string{"u1": "c", "u2": "c"},
		},
		"the next host with the memory": {
			hosts: mm.Hosts{{Name: "a", MemTotal: 4096}, {Name: "b", MemTotal: 1024}},
			vms: []builtinVM{
				{name: "v1", vlan: "EXP", memory: 512}, {name: "v2", vlan: "EXP", memory: 512}, {name: "u1", memory: 256},
			},
			schedules: map[string]any{"v1": "b", "v2": "b"},
			want:      map[string]string{"v1": "b", "v2": "b", "u1": "a"},
		},
		"no host with the memory": {
			hosts:        mm.Hosts{{Name: "a", MemTotal: 1024}, {Name: "b", MemTotal: 1024}},
			vms:          []builtinVM{{name: "v1", vlan: "EXP", memory: 1024}, {name: "u1", memory: 2048}},
			schedules:    map[string]any{"v1": "b"},
			want:         map[string]string{"v1": "b", "u1": "b"},
			wantWarnings: []string{"host b: 3 GiB of memory committed, 1 GiB available"},
		},
		// the memory of a VM placed before counts for the next one
		"one after the other": {
			hosts: mm.Hosts{{Name: "a", MemTotal: 1024}, {Name: "b", MemTotal: 1024}},
			vms:   []builtinVM{{name: "u1", memory: 768}, {name: "u2", memory: 512}},
			want:  map[string]string{"u1": "a", "u2": "b"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: tc.hosts})

			spec := builtinSpec(t, tc.vms, tc.schedules)

			warnings, err := Schedule(t.Context(), "subnet-compute", spec)
			if err != nil {
				t.Fatalf("Schedule(subnet-compute) error = %v", err)
			}

			if diff := cmp.Diff(tc.want, spec.Schedules()); diff != "" {
				t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(subnet-compute) warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("no schedulable hosts", func(t *testing.T) {
		useClusterMM(t, clusterMM{})

		_, err := Schedule(t.Context(), "subnet-compute", builtinSpec(t, []builtinVM{{name: "u1", memory: 256}}, nil))
		if !errors.Is(err, ErrNoSchedulableHosts) {
			t.Errorf("Schedule(subnet-compute) of a VM without interfaces and no hosts error = %v, want %v",
				err, ErrNoSchedulableHosts)
		}
	})
}

// round-robin puts each VM that is not scheduled on the host with the fewest
// VMs, counting the VMs already scheduled, and skips external nodes.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestRoundRobin(t *testing.T) {
	useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1"}, {Name: "h2", VMs: 3}}})

	spec := builtinSpec(t, []builtinVM{
		{name: "vm1", vlan: "EXP"}, {name: "vm2", vlan: "EXP"}, {name: "vm3", vlan: "EXP"}, {name: "ext", external: true},
	}, map[string]any{"vm1": "h1"})

	if _, err := Schedule(t.Context(), "round-robin", spec); err != nil {
		t.Fatalf("Schedule(round-robin) error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"vm1": "h1", "vm2": "h1", "vm3": "h1"}, spec.Schedules()); diff != "" {
		t.Errorf("Schedule(round-robin) schedules mismatch (-want +got):\n%s", diff)
	}
}

// round-robin counts only the pins of VMs that the topology has and that are
// not external, and takes hosts that tie by name, whatever order minimega
// gives them in.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestRoundRobinCountsOnlyPinnedVMs(t *testing.T) {
	useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h2"}, {Name: "h1"}}})

	spec := builtinSpec(t, []builtinVM{
		{name: "vm1", vlan: "EXP"}, {name: "vm2", vlan: "EXP"}, {name: "ext", external: true},
	}, map[string]any{"gone": "h1", "ext": "h1"})

	if _, err := Schedule(t.Context(), "round-robin", spec); err != nil {
		t.Fatalf("Schedule(round-robin) error = %v", err)
	}

	want := map[string]string{"gone": "h1", "ext": "h1", "vm1": "h1", "vm2": "h2"}
	if diff := cmp.Diff(want, spec.Schedules()); diff != "" {
		t.Errorf("Schedule(round-robin) schedules mismatch (-want +got):\n%s", diff)
	}
}

// round-robin ignores the capacity of the hosts, and warns of each host that
// it overcommits, in host name order, and of a pin to a host that is not in
// the cluster. A host that does not give a capacity is not checked.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestRoundRobinWarnings(t *testing.T) {
	useClusterMM(t, clusterMM{hosts: mm.Hosts{
		{Name: "h2", CPUs: 2, CPUCommit: 2, MemTotal: 1024, MemCommit: 1024, VMs: 3},
		{Name: "h1", CPUs: 2, MemTotal: 1024},
		{Name: "unknown", VMs: 9},
	}})

	spec := builtinSpec(t, []builtinVM{
		{name: "vm1", vlan: "EXP", memory: 512}, {name: "vm2", vlan: "EXP", memory: 512},
		{name: "vm3", vlan: "EXP", memory: 512}, {name: "vm4", vlan: "EXP", memory: 512},
		{name: "vm5", vlan: "EXP", memory: 64}, {name: "vm6", vlan: "EXP", memory: 64},
	}, map[string]any{"vm4": "gone", "vm5": "h2", "vm6": "unknown"})

	warnings, err := Schedule(t.Context(), "round-robin", spec)
	if err != nil {
		t.Fatalf("Schedule(round-robin) error = %v", err)
	}

	want := map[string]string{"vm1": "h1", "vm2": "h1", "vm3": "h1", "vm4": "gone", "vm5": "h2", "vm6": "unknown"}
	if diff := cmp.Diff(want, spec.Schedules()); diff != "" {
		t.Errorf("Schedule(round-robin) schedules mismatch (-want +got):\n%s", diff)
	}

	wantWarnings := []string{
		"VM vm4 stays on host gone, which is not a schedulable cluster host",
		"host h1: 1.5 GiB of memory committed, 1 GiB available; 3 CPUs committed, 2 available",
		"host h2: 1088 MiB of memory committed, 1 GiB available; 3 CPUs committed, 2 available",
	}
	if diff := cmp.Diff(wantWarnings, warnings); diff != "" {
		t.Errorf("Schedule(round-robin) warnings mismatch (-want +got):\n%s", diff)
	}
}

// The capacity checks count the VMs that do not boot at start, as they can
// boot later, and leave out external nodes.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestOvercommitCountsVMsThatDoNotBoot(t *testing.T) {
	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment"} {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1", CPUs: 4, MemTotal: 1024}}})

			spec := builtinSpec(t, []builtinVM{
				{name: "vm1", vlan: "EXP", memory: 768},
				{name: "vm2", vlan: "EXP", memory: 768, cpus: 4, doNotBoot: true},
				{name: "ext", vlan: "EXP", memory: 4096, cpus: 64, external: true},
			}, map[string]any{"ext": "h1"})

			warnings, err := Schedule(t.Context(), name, spec)
			if err != nil {
				t.Fatalf("Schedule(%s) error = %v", name, err)
			}

			want := []string{"host h1: 1.5 GiB of memory committed, 1 GiB available; 5 CPUs committed, 4 available"}
			if diff := cmp.Diff(want, warnings); diff != "" {
				t.Errorf("Schedule(%s) warnings mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// formatMemory writes memory exactly: in GiB for a multiple of half a GiB
// from 1 GiB up, else in MiB.
func TestFormatMemory(t *testing.T) {
	t.Parallel()

	for mib, want := range map[int]string{
		0: "0 MiB", 512: "512 MiB", 1023: "1023 MiB", 1024: "1 GiB", 1536: "1.5 GiB", 1100: "1100 MiB",
		98304: "96 GiB", 2049: "2049 MiB",
	} {
		if got := formatMemory(mib); got != want {
			t.Errorf("formatMemory(%d) = %q, want %q", mib, got, want)
		}
	}
}

// isolate-experiment takes a free host where the VMs use exactly all of its
// CPUs and memory, and reads no hosts for a topology of external nodes only.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestIsolateExperimentEdges(t *testing.T) {
	t.Run("exact fit", func(t *testing.T) {
		// wide has the most free memory but too few CPUs
		useClusterMM(t, clusterMM{hosts: mm.Hosts{
			{Name: "wide", CPUs: 1, MemTotal: 4096}, {Name: "exact", CPUs: 2, MemTotal: 1024},
		}})

		spec := builtinSpec(t, []builtinVM{{name: "vm1", vlan: "EXP", memory: 512}, {name: "vm2", vlan: "EXP", memory: 512}}, nil)

		if _, err := Schedule(t.Context(), "isolate-experiment", spec); err != nil {
			t.Fatalf("Schedule(isolate-experiment) error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{"vm1": "exact", "vm2": "exact"}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(isolate-experiment) schedules mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("external nodes only", func(t *testing.T) {
		useClusterMM(t, failingClusterMM{})

		spec := builtinSpec(t, []builtinVM{{name: "ext", external: true}}, nil)

		if _, err := Schedule(t.Context(), "isolate-experiment", spec); err != nil {
			t.Fatalf("Schedule(isolate-experiment) of external nodes only error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(isolate-experiment) schedules mismatch (-want +got):\n%s", diff)
		}
	})
}

// subnet-compute puts a VM on the host of its VLAN when it fills that host's
// memory exactly, tries the hosts of a VLAN in the order of the names of the
// VMs pinned to them, and does not need interfaces on a pinned external node.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestSubnetComputeEdges(t *testing.T) {
	t.Run("exact fit", func(t *testing.T) {
		useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1", MemTotal: 1024}, {Name: "h2", MemTotal: 4096}}})

		spec := builtinSpec(t, []builtinVM{
			{name: "vm1", vlan: "A", memory: 512}, {name: "vm2", vlan: "A", memory: 512},
		}, map[string]any{"vm1": "h1"})

		if _, err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
			t.Fatalf("Schedule(subnet-compute) error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{"vm1": "h1", "vm2": "h1"}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("same order every time", func(t *testing.T) {
		for range 20 {
			useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1", MemTotal: 4096}, {Name: "h2", MemTotal: 4096}}})

			spec := builtinSpec(t, []builtinVM{
				{name: "vmA", vlan: "X", memory: 256}, {name: "vmB", vlan: "X", memory: 256}, {name: "vmC", vlan: "X", memory: 256},
			}, map[string]any{"vmA": "h2", "vmB": "h1"})

			if _, err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
				t.Fatalf("Schedule(subnet-compute) error = %v", err)
			}

			if diff := cmp.Diff(map[string]string{"vmA": "h2", "vmB": "h1", "vmC": "h2"}, spec.Schedules()); diff != "" {
				t.Fatalf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
			}
		}
	})

	t.Run("pinned external node", func(t *testing.T) {
		useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1", MemTotal: 4096}}})

		spec := builtinSpec(t, []builtinVM{{name: "ext", external: true}, {name: "vm1", vlan: "A", memory: 256}},
			map[string]any{"ext": "h1"})

		if _, err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
			t.Fatalf("Schedule(subnet-compute) with a pinned external node error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{"ext": "h1", "vm1": "h1"}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
		}
	})
}
