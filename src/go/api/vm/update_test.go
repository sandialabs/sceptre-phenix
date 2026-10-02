package vm_test

import (
	"reflect"
	"testing"

	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
)

// savedNode decodes the node router from a saved experiment spec.
func savedNode(t *testing.T, spec map[string]any) ifaces.NodeSpec { //nolint:ireturn // the topology's node type
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec:     spec,
	})
	if err != nil {
		t.Fatalf("decoding the saved experiment: %v", err)
	}

	return exp.Spec.Topology().FindNodeByName("router")
}

func TestUpdate(t *testing.T) {
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

		// the saved VM, unless Update fails
		wantCPUs        int
		wantLabels      map[string]string
		wantAnnotations map[string]any
		wantTagUpdates  int
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

			started := ""
			if test.running {
				started = startTime
			}

			stored := useExperiment(t, started, node)
			fake := useMM(t, &fakeMM{})

			opts := append([]vm.UpdateOption{vm.UpdateExperiment(testExp), vm.UpdateVM("router")}, test.opts...)
			checkErr(t, vm.Update(opts...), test.wantErr)

			if fake.tagUpdates != test.wantTagUpdates {
				t.Errorf("updated minimega's tags %d times, want %d", fake.tagUpdates, test.wantTagUpdates)
			}

			if test.wantErr != nil {
				if len(stored.saved) != 0 {
					t.Errorf("saved the experiment %d times, want never", len(stored.saved))
				}

				return
			}

			if len(stored.saved) != 1 {
				t.Fatalf("saved the experiment %d times, want once", len(stored.saved))
			}

			saved := savedNode(t, stored.saved[0])

			if got := saved.Hardware().VCPU(); got != test.wantCPUs {
				t.Errorf("saved %d CPUs, want %d", got, test.wantCPUs)
			}

			if got := saved.Labels(); !reflect.DeepEqual(got, test.wantLabels) {
				t.Errorf("saved labels %v, want %v", got, test.wantLabels)
			}

			// nil and empty both mean no annotations
			if got := saved.Annotations(); (len(got) > 0 || len(test.wantAnnotations) > 0) &&
				!reflect.DeepEqual(got, test.wantAnnotations) {
				t.Errorf("saved annotations %v, want %v", got, test.wantAnnotations)
			}
		})
	}
}
