package vm_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// savedSpec decodes a saved experiment spec.
func savedSpec(t *testing.T, spec map[string]any) ifaces.ExperimentSpec {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec:     spec,
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig(saved experiment) error = %v", err)
	}

	return exp.Spec
}

func TestUpdate(t *testing.T) { //nolint:maintidx // one table
	var (
		annotations = map[string]any{"vncBanner": "old", "phenix/default-apps": true}
		labels      = map[string]string{"role": "edge"}
		replaced    = map[string]any{"vncBanner": "new", "phenix/startup-autotunnel": []any{"8080"}}
	)

	tests := []struct {
		name    string
		running bool
		opts    []vm.UpdateOption
		wantErr error

		// the VM's labels before the update, if not labels
		labels map[string]string

		// the experiment's schedules before the update
		schedules map[string]string

		wantConnects int

		// the saved VM, unless Update fails
		wantCPUs        int
		wantLabels      map[string]string
		wantAnnotations map[string]any
		wantTagUpdates  int

		// the saved VM's memory, if not 512
		wantMem       int
		wantDNB       bool
		wantSnapshot  bool
		wantSchedules map[string]string
	}{
		{
			name:     "replaces the annotations of a stopped experiment's VM",
			opts:     []vm.UpdateOption{vm.UpdateWithAnnotations(replaced)},
			wantCPUs: 2, wantLabels: labels, wantAnnotations: replaced,
		},
		{
			name:     "replaces the annotations of a running experiment's VM",
			running:  true,
			opts:     []vm.UpdateOption{vm.UpdateWithAnnotations(replaced)},
			wantCPUs: 2, wantLabels: labels, wantAnnotations: replaced,
		},
		{
			name:     "removes all annotations for a nil map",
			opts:     []vm.UpdateOption{vm.UpdateWithAnnotations(nil)},
			wantCPUs: 2, wantLabels: labels, wantAnnotations: map[string]any{},
		},
		{
			name:    "rejects annotations the default apps would ignore",
			running: true,
			opts: []vm.UpdateOption{
				vm.UpdateWithAnnotations(map[string]any{"phenix/default-apps": "false"}),
			},
			wantErr: vm.ErrInvalidAnnotations,
		},
		{
			name: "changes a stopped VM's CPUs and adds tags to its labels",
			opts: []vm.UpdateOption{
				vm.UpdateWithCPU(4), vm.UpdateWithTags(map[string]string{"zone": "a"}, true),
			},
			wantCPUs:        4,
			wantLabels:      map[string]string{"role": "edge", "zone": "a"},
			wantAnnotations: annotations,
		},
		{
			name:     "replaces a running VM's tags in minimega and its labels",
			running:  true,
			opts:     []vm.UpdateOption{vm.UpdateWithTags(map[string]string{"zone": "a"}, false)},
			wantCPUs: 2, wantLabels: map[string]string{"zone": "a"}, wantAnnotations: annotations,
			wantTagUpdates: 1,
		},
		{
			name:    "rejects a tag minimega would split into other arguments",
			running: true,
			opts:    []vm.UpdateOption{vm.UpdateWithTags(map[string]string{"zone": `a" "b`}, true)},
			wantErr: mm.ErrUnsafeArg,
		},
		{
			name:     "changes a stopped VM's memory, boot and snapshot settings",
			opts:     []vm.UpdateOption{vm.UpdateWithMem(2048), vm.UpdateWithDNB(true), vm.UpdateWithSnapshot(true)},
			wantCPUs: 2, wantLabels: labels, wantAnnotations: annotations,
			wantMem: 2048, wantDNB: true, wantSnapshot: true,
		},
		{
			name:      "schedules a stopped VM on a host",
			schedules: map[string]string{"other": "compute2"},
			opts:      []vm.UpdateOption{vm.UpdateWithHost("compute1")},
			wantCPUs:  2, wantLabels: labels, wantAnnotations: annotations,
			wantSchedules: map[string]string{"router": "compute1", "other": "compute2"},
		},
		{
			name:      "clears a stopped VM's schedule for an empty host",
			schedules: map[string]string{"router": "compute1", "other": "compute2"},
			opts:      []vm.UpdateOption{vm.UpdateWithHost("")},
			wantCPUs:  2, wantLabels: labels, wantAnnotations: annotations,
			wantSchedules: map[string]string{"other": "compute2"},
		},
		{
			name:    "rejects a memory change while the experiment runs",
			running: true,
			opts:    []vm.UpdateOption{vm.UpdateWithMem(2048), vm.UpdateWithAnnotations(replaced)},
			wantErr: vm.ErrRunningUpdate,
		},
		{
			name:    "rejects a stopped VM's tag minimega would expand",
			opts:    []vm.UpdateOption{vm.UpdateWithTags(map[string]string{"zone": "$HOME"}, false)},
			wantErr: mm.ErrUnsafeArg,
		},
		{
			name:    "reconnects a running VM's interface and appends tags to its labels",
			running: true,
			opts: []vm.UpdateOption{
				vm.UpdateWithInterface(0, "EXP_1"), vm.UpdateWithTags(map[string]string{"zone": "a"}, true),
			},
			wantConnects: 1,
			wantCPUs:     2, wantLabels: map[string]string{"role": "edge", "zone": "a"}, wantAnnotations: annotations,
			wantTagUpdates: 1,
		},
		{
			name:    "rejects appending tags to an existing label minimega would fetch, before reconnecting",
			running: true,
			labels:  map[string]string{"url": "http://x"},
			opts: []vm.UpdateOption{
				vm.UpdateWithInterface(0, "EXP_1"), vm.UpdateWithTags(map[string]string{"zone": "a"}, true),
			},
			wantErr: mm.ErrUnsafeArg,
		},
		{
			name:    "rejects connecting a stopped VM's interface, and changes nothing else",
			opts:    []vm.UpdateOption{vm.UpdateWithInterface(0, "EXP_1"), vm.UpdateWithCPU(4)},
			wantErr: vm.ErrStoppedInterfaceUpdate,
		},
		{
			name:    "rejects disconnecting a stopped VM's interface",
			opts:    []vm.UpdateOption{vm.UpdateWithInterface(0, "")},
			wantErr: vm.ErrStoppedInterfaceUpdate,
		},
		{
			name:    "rejects a VLAN minimega would split into other arguments",
			running: true,
			opts:    []vm.UpdateOption{vm.UpdateWithInterface(0, "EXP_1 #")},
			wantErr: mm.ErrUnsafeArg,
		},
		{
			name:    "rejects a hardware change while the experiment runs",
			running: true,
			opts:    []vm.UpdateOption{vm.UpdateWithCPU(4), vm.UpdateWithAnnotations(replaced)},
			wantErr: errAny,
		},
		{
			name:    "rejects an update that changes nothing while the experiment runs",
			running: true,
			wantErr: errAny,
		},
		{
			name:    "rejects an empty experiment name",
			opts:    []vm.UpdateOption{vm.UpdateExperiment(""), vm.UpdateWithCPU(4)},
			wantErr: errAny,
		},
		{
			name:    "rejects a VM the topology does not have",
			opts:    []vm.UpdateOption{vm.UpdateVM("missing"), vm.UpdateWithCPU(4)},
			wantErr: errAny,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := vmNode("router")
			node["labels"], node["annotations"] = labels, annotations
			if test.labels != nil {
				node["labels"] = test.labels
			}

			started := ""
			if test.running {
				started = startTime
			}

			stored := useExperiment(t, started, node)
			if test.schedules != nil {
				stored.experiment.Spec["schedules"] = test.schedules
			}

			fake := useMM(t, &fakeMM{})

			opts := append([]vm.UpdateOption{vm.UpdateExperiment(testExp), vm.UpdateVM("router")}, test.opts...)
			if err := vm.Update(opts...); !errMatches(err, test.wantErr) {
				t.Errorf("Update() error = %v, want %v", err, test.wantErr)
			}

			if fake.connects != test.wantConnects {
				t.Errorf("Update() minimega interface connects = %d, want %d", fake.connects, test.wantConnects)
			}

			if fake.tagUpdates != test.wantTagUpdates {
				t.Errorf("Update() minimega tag updates = %d, want %d", fake.tagUpdates, test.wantTagUpdates)
			}

			if test.wantErr != nil {
				if len(stored.saved) != 0 {
					t.Errorf("Update() experiment saves = %d, want 0", len(stored.saved))
				}

				return
			}

			if len(stored.saved) != 1 {
				t.Fatalf("Update() experiment saves = %d, want 1", len(stored.saved))
			}

			spec := savedSpec(t, stored.saved[0])
			saved := spec.Topology().FindNodeByName("router")

			if got := saved.Hardware().VCPU(); got != test.wantCPUs {
				t.Errorf("Update() saved VCPU() = %d, want %d", got, test.wantCPUs)
			}

			if diff := cmp.Diff(test.wantLabels, saved.Labels()); diff != "" {
				t.Errorf("Update() saved Labels() mismatch (-want +got):\n%s", diff)
			}

			// nil and empty both mean no annotations
			if diff := cmp.Diff(test.wantAnnotations, saved.Annotations(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Update() saved Annotations() mismatch (-want +got):\n%s", diff)
			}

			wantMem := test.wantMem
			if wantMem == 0 {
				wantMem = 512
			}

			if got := saved.Hardware().Memory(); got != wantMem {
				t.Errorf("Update() saved Memory() = %d, want %d", got, wantMem)
			}

			// nil and false both mean not set
			if got := saved.General().DoNotBoot(); (got != nil && *got) != test.wantDNB {
				t.Errorf("Update() saved DoNotBoot() = %t, want %t", got != nil && *got, test.wantDNB)
			}

			if got := saved.General().Snapshot(); (got != nil && *got) != test.wantSnapshot {
				t.Errorf("Update() saved Snapshot() = %t, want %t", got != nil && *got, test.wantSnapshot)
			}

			if diff := cmp.Diff(test.wantSchedules, spec.Schedules(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Update() saved Schedules() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Update refuses a disk or inject partition for a topology node with no drives, and saves nothing.
func TestUpdateNodeWithoutDrives(t *testing.T) {
	for name, opt := range map[string]vm.UpdateOption{
		"disk":      vm.UpdateWithDisk("/images/new.qc2"),
		"partition": vm.UpdateWithPartition(2),
	} {
		t.Run(name, func(t *testing.T) {
			node := vmNode("router")
			node["hardware"].(map[string]any)["drives"] = []map[string]any{}

			stored := useExperiment(t, "", node)
			useMM(t, &fakeMM{})

			err := vm.Update(vm.UpdateExperiment(testExp), vm.UpdateVM("router"), opt)
			if err == nil {
				t.Errorf("Update(%s) of a node with no drives error = nil, want an error", name)
			}

			if len(stored.saved) != 0 {
				t.Errorf("Update(%s) experiment saves = %d, want 0", name, len(stored.saved))
			}
		})
	}
}

// The base image of a VM is its first drive's image, and a node with no drives gives an error.
func TestVMImage(t *testing.T) {
	bare := vmNode("bare")
	bare["hardware"].(map[string]any)["drives"] = []map[string]any{}

	useExperiment(t, "", vmNode("router"), bare)

	if got, err := vm.VMImage(testExp, "router"); err != nil || got != "/images/router.qc2" {
		t.Errorf("VMImage(%q, router) = %q, %v, want /images/router.qc2, nil", testExp, got, err)
	}

	if got, err := vm.VMImage(testExp, "bare"); err == nil {
		t.Errorf("VMImage(%q, bare) = %q, nil, want an error for a node with no drives", testExp, got)
	}
}

// Update saves the inject partition it is given, 0 too, which turns off disk
// injection, and keeps the partition (1 when unset) without the option.
func TestUpdatePartition(t *testing.T) {
	one, two, zero := 1, 2, 0

	for name, test := range map[string]struct {
		opts []vm.UpdateOption
		want *int
	}{
		"partition 2":  {opts: []vm.UpdateOption{vm.UpdateWithPartition(2)}, want: &two},
		"partition 0":  {opts: []vm.UpdateOption{vm.UpdateWithPartition(0)}, want: &zero},
		"no partition": {opts: []vm.UpdateOption{vm.UpdateWithCPU(4)}, want: &one},
	} {
		t.Run(name, func(t *testing.T) {
			stored := useExperiment(t, "", vmNode("router"))
			useMM(t, &fakeMM{})

			opts := append([]vm.UpdateOption{vm.UpdateExperiment(testExp), vm.UpdateVM("router")}, test.opts...)
			if err := vm.Update(opts...); err != nil {
				t.Fatalf("Update(%s) error = %v", name, err)
			}

			if len(stored.saved) != 1 {
				t.Fatalf("Update(%s) experiment saves = %d, want 1", name, len(stored.saved))
			}

			got := savedSpec(t, stored.saved[0]).Topology().FindNodeByName("router").Hardware().Drives()[0].InjectPartition()
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Update(%s) saved InjectPartition() mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}
