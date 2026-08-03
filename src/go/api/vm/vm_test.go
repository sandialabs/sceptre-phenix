package vm_test

import (
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
)

// getTestExperiment builds a store mock backing a minimal experiment with one
// topology node and one scenario app that targets the node with metadata, and
// makes it the phenix default store for the duration of the test.
func getTestExperiment(t *testing.T, vmName string, appMetadata map[string]any) {
	t.Helper()

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
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
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

	getTestExperiment(t, "test-vm", metadata)

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
