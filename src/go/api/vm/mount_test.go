package vm_test

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/vm"
	"phenix/store"
	"phenix/util/mm"
)

// c2MM is a test double that records ExecC2Command invocations instead of
// talking to a real minimega instance.
type c2MM struct {
	mm.MM

	calls int
}

func (m *c2MM) ExecC2Command(_ ...mm.C2Option) (string, error) {
	m.calls++

	return "", nil
}

// getDelayedTestExperiment builds a store mock backing a minimal experiment
// with a single, user-delayed topology node, optionally with the
// phenix/auto-mount annotation set.
func getDelayedTestExperiment(t *testing.T, vmName string, autoMount any, setAnnotation bool) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	node := map[string]any{
		"type": "VirtualMachine",
		"general": map[string]any{
			"hostname":    vmName,
			"do_not_boot": false,
			"snapshot":    false,
		},
		"hardware": map[string]any{
			"vcpus":   2,
			"memory":  512,
			"os_type": "linux",
			"drives": []map[string]any{
				{"image": "test.qc2", "inject_partition": 1},
			},
		},
		"network": map[string]any{
			"interfaces": []map[string]any{
				{"name": "IF0", "vlan": "EXP_1", "address": "10.0.0.1"},
			},
		},
		"delay": map[string]any{"user": true},
	}

	if setAnnotation {
		node["annotations"] = map[string]any{"phenix/auto-mount": autoMount}
	}

	c := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "test-experiment",
		},
		Spec: map[string]any{
			"experimentName": "test-experiment",
			"topology": map[string]any{
				"nodes": []map[string]any{node},
			},
		},
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test
}

func TestTriggerAutoMountForDelayedStart(t *testing.T) {
	tests := []struct {
		name          string
		setAnnotation bool
		autoMount     any
		wantCalls     int
		wantErr       string
	}{
		{
			name:          "auto-mount enabled on user-delayed node",
			setAnnotation: true,
			autoMount:     true,
			wantCalls:     1,
		},
		{
			name:          "auto-mount disabled on user-delayed node",
			setAnnotation: true,
			autoMount:     false,
			wantCalls:     0,
		},
		{
			name:          "auto-mount annotation absent",
			setAnnotation: false,
			wantCalls:     0,
		},
		{
			name:          "invalid annotation value errors",
			setAnnotation: true,
			autoMount:     "true",
			wantCalls:     0,
			wantErr:       "auto-mounting delayed VM delayed-vm: phenix/auto-mount annotation for node delayed-vm must be a boolean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getDelayedTestExperiment(t, "delayed-vm", tt.autoMount, tt.setAnnotation)

			originalMM := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

			fake := &c2MM{}
			mm.DefaultMM = fake //nolint:reassign // install test double

			err := vm.TriggerAutoMountForDelayedStart(t.Context(), "test-experiment", "delayed-vm")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("TriggerAutoMountForDelayedStart() error = %v", err)
				}
			} else if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("TriggerAutoMountForDelayedStart() error = %v, want %q", err, tt.wantErr)
			}

			if fake.calls != tt.wantCalls {
				t.Fatalf("ExecC2Command calls = %d, want %d", fake.calls, tt.wantCalls)
			}
		})
	}
}
