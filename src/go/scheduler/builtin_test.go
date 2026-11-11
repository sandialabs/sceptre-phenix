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

// builtinVM is a topology node: a VM with the given memory on VLAN vlan, or
// with no interfaces when vlan is empty. An external one is not a VM to
// schedule.
type builtinVM struct {
	name, vlan string
	memory     int
	external   bool
}

// builtinSpec is the spec of experiment exp with the given nodes and schedules.
func builtinSpec(t *testing.T, vms []builtinVM, schedules map[string]any) ifaces.ExperimentSpec { //nolint:ireturn // the spec type
	t.Helper()

	nodes := make([]map[string]any, 0, len(vms))

	for _, vm := range vms {
		interfaces := []map[string]any{}
		if vm.vlan != "" {
			interfaces = append(interfaces, map[string]any{"name": "eth0", "vlan": vm.vlan})
		}

		node := map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": vm.name},
			"hardware": map[string]any{"vcpus": 1, "memory": vm.memory, "os_type": "linux"},
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

func (failingClusterMM) GetClusterHosts(bool) (mm.Hosts, error) {
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

			if err := Schedule(t.Context(), name, builtinSpec(t, nil, nil)); err == nil {
				t.Errorf("Schedule(%s, no VMs) = nil, want an error", name)
			}
		})

		t.Run(name+"/no cluster hosts", func(t *testing.T) {
			useClusterMM(t, failingClusterMM{})

			if err := Schedule(t.Context(), name, builtinSpec(t, []builtinVM{{name: "vm1", vlan: "EXP"}}, nil)); err == nil {
				t.Errorf("Schedule(%s, minimega not running) = nil, want an error", name)
			}
		})
	}
}

// isolate-experiment puts every VM on one host that has no VMs: the host of
// the first VM when that host is free, even if it becomes overloaded; else
// the free host with the most free memory that fits all VMs; else any free
// host. It fails when every host has VMs. It logs a warning when it
// overloads the first VM's host, or when that host is busy.
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

	tests := map[string]struct {
		hosts     mm.Hosts
		schedules map[string]any
		wantErr   bool
		want      map[string]string
		wantLogs  []map[string]any
	}{
		"first VM's free host, overloaded": {
			hosts: mm.Hosts{big, small}, schedules: map[string]any{"vm1": "small"},
			want:     map[string]string{"vm1": "small", "vm2": "small"},
			wantLogs: []map[string]any{{"level": "WARN", "type": "SYSTEM", "msg": overloaded, "host": "small", "exp": "exp"}},
		},
		"first VM's host is busy": {
			hosts: mm.Hosts{big, busy}, schedules: map[string]any{"vm1": "busy"},
			want:     map[string]string{"vm1": "big", "vm2": "big"},
			wantLogs: []map[string]any{{"level": "WARN", "type": "SYSTEM", "msg": inUse, "host": "busy", "exp": "exp"}},
		},
		"first VM's host is not in the cluster": {
			hosts: mm.Hosts{big}, schedules: map[string]any{"vm1": "gone"},
			want: map[string]string{"vm1": "big", "vm2": "big"},
		},
		"a free host where all VMs fit": {
			hosts: mm.Hosts{small, busy, big},
			want:  map[string]string{"vm1": "big", "vm2": "big"},
		},
		"a free host where the VMs do not fit": {
			hosts: mm.Hosts{busy, small},
			want:  map[string]string{"vm1": "small", "vm2": "small"},
		},
		"no free host": {hosts: mm.Hosts{busy}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: tc.hosts})
			logs := plogtest.Capture(t)

			spec := builtinSpec(t, vms, tc.schedules)

			err := Schedule(t.Context(), "isolate-experiment", spec)

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
		})
	}
}

// subnet-compute puts a VM on a host that has a VM of the same VLAN and
// memory left for it, and otherwise on the host with the least committed
// memory. It fails for a VM without interfaces.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestSubnetCompute(t *testing.T) {
	h1 := mm.Host{Name: "h1", MemTotal: 4096}
	h2 := mm.Host{Name: "h2", MemTotal: 1024, MemCommit: 256}

	tests := map[string]struct {
		vms       []builtinVM
		schedules map[string]any
		wantErr   bool
		want      map[string]string
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
		"scheduled to a host not in the cluster": {
			vms:       []builtinVM{{name: "vm1", vlan: "EXP", memory: 256}, {name: "vm2", vlan: "EXP", memory: 256}},
			schedules: map[string]any{"vm1": "gone"},
			want:      map[string]string{"vm1": "gone", "vm2": "h1"},
		},
		"scheduled VM without interfaces": {
			vms: []builtinVM{{name: "vm1", memory: 256}}, schedules: map[string]any{"vm1": "h1"}, wantErr: true,
		},
		"VM without interfaces": {vms: []builtinVM{{name: "vm1", memory: 256}}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useClusterMM(t, clusterMM{hosts: mm.Hosts{h1, h2}})

			spec := builtinSpec(t, tc.vms, tc.schedules)

			err := Schedule(t.Context(), "subnet-compute", spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Schedule(subnet-compute) error = %v, want error %t", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if diff := cmp.Diff(tc.want, spec.Schedules()); diff != "" {
				t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
			}
		})
	}
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

	if err := Schedule(t.Context(), "round-robin", spec); err != nil {
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

	if err := Schedule(t.Context(), "round-robin", spec); err != nil {
		t.Fatalf("Schedule(round-robin) error = %v", err)
	}

	want := map[string]string{"gone": "h1", "ext": "h1", "vm1": "h1", "vm2": "h2"}
	if diff := cmp.Diff(want, spec.Schedules()); diff != "" {
		t.Errorf("Schedule(round-robin) schedules mismatch (-want +got):\n%s", diff)
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

		if err := Schedule(t.Context(), "isolate-experiment", spec); err != nil {
			t.Fatalf("Schedule(isolate-experiment) error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{"vm1": "exact", "vm2": "exact"}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(isolate-experiment) schedules mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("external nodes only", func(t *testing.T) {
		useClusterMM(t, failingClusterMM{})

		spec := builtinSpec(t, []builtinVM{{name: "ext", external: true}}, nil)

		if err := Schedule(t.Context(), "isolate-experiment", spec); err != nil {
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

		if err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
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

			if err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
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

		if err := Schedule(t.Context(), "subnet-compute", spec); err != nil {
			t.Fatalf("Schedule(subnet-compute) with a pinned external node error = %v", err)
		}

		if diff := cmp.Diff(map[string]string{"ext": "h1", "vm1": "h1"}, spec.Schedules()); diff != "" {
			t.Errorf("Schedule(subnet-compute) schedules mismatch (-want +got):\n%s", diff)
		}
	})
}
