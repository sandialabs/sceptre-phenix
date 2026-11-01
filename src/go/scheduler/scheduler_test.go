package scheduler

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// clusterMM fakes minimega's schedulable cluster hosts; other mm.MM methods panic.
type clusterMM struct {
	mm.MM

	hosts mm.Hosts
}

func (m clusterMM) GetClusterHosts(bool) (mm.Hosts, error) {
	return append(mm.Hosts(nil), m.hosts...), nil
}

// schedulerSpec is an experiment spec with VMs vm1 and vm2 on VLAN EXP, and
// the given schedules.
func schedulerSpec(t *testing.T, schedules map[string]any) ifaces.ExperimentSpec { //nolint:ireturn // the spec type
	t.Helper()

	node := func(name string) map[string]any {
		return map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": name},
			"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux"},
			"network":  map[string]any{"interfaces": []map[string]any{{"name": "eth0", "vlan": "EXP"}}},
		}
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
			"topology":       map[string]any{"nodes": []map[string]any{node("vm1"), node("vm2")}},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	return exp.Spec
}

func TestSchedulersWithoutSchedulableHosts(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	head := mm.Host{Name: "head", MemTotal: 4096, CPUs: 4}

	tests := []struct {
		name      string
		hosts     mm.Hosts
		schedules map[string]any
		wantErr   error
		want      map[string]string
	}{
		{name: "fails with no hosts", wantErr: ErrNoSchedulableHosts},
		{
			name:  "schedules every VM on the only host",
			hosts: mm.Hosts{head},
			want:  map[string]string{"vm1": "head", "vm2": "head"},
		},
		{
			name:      "needs no host when every VM is scheduled",
			schedules: map[string]any{"vm1": "compute1", "vm2": "compute1"},
			want:      map[string]string{"vm1": "compute1", "vm2": "compute1"},
		},
	}

	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment"} {
		for _, test := range tests {
			if name == "isolate-experiment" && test.schedules != nil {
				continue // isolates the experiment again on a free host
			}

			t.Run(name+"/"+test.name, func(t *testing.T) {
				original := mm.DefaultMM
				t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

				mm.DefaultMM = clusterMM{hosts: test.hosts} //nolint:reassign // install test double

				spec := schedulerSpec(t, test.schedules)

				err := Schedule(name, spec)
				if !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
					t.Fatalf("Schedule(%s) error = %v, want %v", name, err, test.wantErr)
				}

				if test.wantErr != nil {
					return
				}

				if diff := cmp.Diff(test.want, spec.Schedules()); diff != "" {
					t.Errorf("Schedule(%s) schedules mismatch (-want +got):\n%s", name, diff)
				}
			})
		}
	}
}
