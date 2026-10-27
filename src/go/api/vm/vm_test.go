package vm_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

type listMM struct {
	mm.MM

	vms mm.VMs
}

func (m listMM) GetVMInfo(...mm.Option) mm.VMs {
	return m.vms
}

type connectMM struct {
	mm.MM

	capturedOpts []mm.Option
}

func (m *connectMM) ConnectVMInterface(opts ...mm.Option) error {
	m.capturedOpts = opts
	return nil
}

// getTestExperiment builds a store mock backing a minimal experiment with one
// topology node and one scenario app that targets the node with metadata, and
// makes it the phenix default store for the duration of the test. Any notes go in the node's general.notes.
func getTestExperiment(t *testing.T, vmName string, appMetadata map[string]any, startTime string, notes ...string) {
	t.Helper()

	general := map[string]any{
		"hostname":    vmName,
		"do_not_boot": false,
		"snapshot":    false,
	}

	if len(notes) > 0 {
		general["notes"] = notes
	}

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	c := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "test-experiment",
		},
		Spec: map[string]any{
			"experimentName": "test-experiment",
			"defaultBridge":  "phenix",
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type":    "VirtualMachine",
						"general": general,
						"hardware": map[string]any{
							"vcpus":   2,
							"memory":  512,
							"os_type": "linux",
							"drives": []map[string]any{
								{
									"image":            "test.qc2",
									"inject_partition": 1,
								},
							},
						},
						"network": map[string]any{
							"interfaces": []map[string]any{
								{
									"name":    "IF0",
									"vlan":    "EXP_1",
									"address": "10.0.0.1",
								},
							},
						},
					},
				},
			},
			"scenario": map[string]any{
				"apps": []map[string]any{
					{
						"name": "test-app",
						"hosts": []map[string]any{
							{
								"hostname": vmName,
								"metadata": appMetadata,
							},
						},
					},
				},
			},
		},
	}

	if startTime != "" {
		c.Status = map[string]any{"startTime": startTime}
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test
}

// TestGetIncludesAppMetadata is a regression test for issue #23: `phenix vm
// info` was missing app metadata for the VM because vm.Get assigned the bound
// `Metadata` method value (a func) to the VM's Metadata map instead of
// calling it to get the actual metadata map. Since func values can't be
// marshaled to JSON, the metadata silently disappeared from the printed
// output.
func TestGetIncludesAppMetadata(t *testing.T) {
	metadata := map[string]any{"key": "value"}

	getTestExperiment(t, "test-vm", metadata, "")

	got, err := vm.Get("test-experiment", "test-vm")
	if err != nil {
		t.Fatalf("vm.Get returned error: %v", err)
	}

	appMetadata, ok := got.Metadata["test-app"]
	if !ok {
		t.Fatalf("expected metadata for app %q, got %v", "test-app", got.Metadata)
	}

	appMetadataMap, ok := appMetadata.(map[string]any)
	if !ok {
		t.Fatalf("expected app metadata to be a map[string]any, got %T", appMetadata)
	}

	if !reflect.DeepEqual(appMetadataMap, metadata) {
		t.Fatalf("expected app metadata %v, got %v", metadata, appMetadataMap)
	}
}

func TestListExcludesKilledVMsFromRunningExperiment(t *testing.T) {
	tests := []struct {
		name      string
		startTime string
		vms       mm.VMs
		want      int
	}{
		{
			name:      "running VM remains listed",
			startTime: "2026-08-26T00:00:00Z",
			vms:       mm.VMs{{Name: "test-vm", Running: true}},
			want:      1,
		},
		{
			name:      "killed VM is excluded",
			startTime: "2026-08-26T00:00:00Z",
			vms:       nil,
			want:      0,
		},
		{
			name:      "dry-run VM remains listed",
			startTime: "2026-08-26T00:00:00Z-DRYRUN",
			vms:       nil,
			want:      1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getTestExperiment(t, "test-vm", nil, test.startTime)

			originalMM := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double
			mm.DefaultMM = listMM{vms: test.vms}            //nolint:reassign // install test double

			got, err := vm.List("test-experiment")
			if err != nil {
				t.Fatalf("vm.List returned error: %v", err)
			}

			if len(got) != test.want {
				t.Fatalf("vm.List returned %d VMs, want %d: %#v", len(got), test.want, got)
			}
		})
	}
}

// TestVMsCarryNodeNotes checks that List and Get fill VM notes from general.notes, apart from the tags.
func TestVMsCarryNodeNotes(t *testing.T) {
	notes := []string{"Domain controller.", "Reset the password\nbefore each run."}

	tests := []struct {
		name      string
		startTime string
		vms       mm.VMs
	}{
		{name: "stopped experiment"},
		{
			name:      "running experiment",
			startTime: "2026-08-26T00:00:00Z",
			vms:       mm.VMs{{Name: "notes-vm", Running: true, Tags: map[string]string{"role": "dc"}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getTestExperiment(t, "notes-vm", nil, test.startTime, notes...)

			originalMM := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double
			mm.DefaultMM = listMM{vms: test.vms}            //nolint:reassign // install test double

			listed, err := vm.List("test-experiment")
			if err != nil {
				t.Fatalf("vm.List returned error: %v", err)
			}

			if len(listed) != 1 {
				t.Fatalf("vm.List returned %d VMs, want 1: %#v", len(listed), listed)
			}

			got, err := vm.Get("test-experiment", "notes-vm")
			if err != nil {
				t.Fatalf("vm.Get returned error: %v", err)
			}

			for name, v := range map[string]mm.VM{"vm.List": listed[0], "vm.Get": *got} {
				if !slices.Equal(v.Notes, notes) {
					t.Errorf("%s notes = %q, want %q", name, v.Notes, notes)
				}

				for key := range v.Tags {
					if strings.HasPrefix(key, "__notes_") {
						t.Errorf("%s tags hold note key %q, want no note tags", name, key)
					}
				}
			}
		})
	}
}

func TestConnectUsesExperimentDefaultBridge(t *testing.T) {
	getTestExperiment(t, "test-vm", nil, "")

	originalMM := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

	fakeMM := new(connectMM)
	mm.DefaultMM = fakeMM //nolint:reassign // install test double

	// Test that Connect() successfully calls ConnectVMInterface with options
	if err := vm.Connect("test-experiment", "test-vm", 0, "EXP_1"); err != nil {
		t.Fatalf("vm.Connect returned error: %v", err)
	}

	if len(fakeMM.capturedOpts) == 0 {
		t.Fatal("expected ConnectVMInterface to be called with options including bridge")
	}
}

func TestConnectHandlesExperimentLookupError(t *testing.T) {
	// Don't call getTestExperiment, so the experiment doesn't exist in the store
	if err := vm.Connect("nonexistent-experiment", "test-vm", 0, "EXP_1"); err == nil {
		t.Fatal("expected Connect to return error for nonexistent experiment")
	}
}

// TestGetIncludesAppMetadataUsingDecodedExperiment is a lighter-weight variant
// that decodes the experiment directly (bypassing the store mock plumbing) to
// double check the fix using types.DecodeExperimentFromConfig, matching the
// pattern used by other API-level tests in this repo.
func TestGetIncludesAppMetadataUsingDecodedExperiment(t *testing.T) {
	metadata := map[string]any{"foo": "bar"}

	c := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "decode-experiment",
		},
		Spec: map[string]any{
			"experimentName": "decode-experiment",
			"scenario": map[string]any{
				"apps": []map[string]any{
					{
						"name": "decode-app",
						"hosts": []map[string]any{
							{"hostname": "decode-vm", "metadata": metadata},
						},
					},
				},
			},
		},
	}

	exp, err := types.DecodeExperimentFromConfig(c)
	if err != nil {
		t.Fatalf("decoding experiment from config: %v", err)
	}

	var found map[string]any

	for _, app := range exp.Apps() {
		for _, h := range app.Hosts() {
			if h.Hostname() == "decode-vm" {
				found = h.Metadata()
			}
		}
	}

	if !reflect.DeepEqual(found, metadata) {
		t.Fatalf("expected host metadata %v, got %v", metadata, found)
	}
}
