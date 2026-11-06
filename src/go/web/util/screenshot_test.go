package util

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/vm"
	"phenix/store"
)

func TestGetScreenshotSkipsFirecracker(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "screenshot-exp"},
		Spec: map[string]any{
			"experimentName": "screenshot-exp",
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type":    "VirtualMachine",
						"general": map[string]any{"hostname": "fc-vm", "vm_type": "firecracker"},
					},
				},
			},
		},
		Status: map[string]any{"startTime": "2026-11-05T00:00:00Z"},
	}

	m := store.NewMockStore(ctrl)

	// The type of every node is cached, so the store is read once for both calls.
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).Times(1)

	prev := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = prev }) //nolint:reassign // restore store

	// No minimega runs in this test, so a minimega call would fail with another error.
	for range 2 {
		_, err := GetScreenshot("screenshot-exp", "fc-vm", "215")
		if !errors.Is(err, vm.ErrNotSupported) {
			t.Fatalf("GetScreenshot(%q) = %v, want %v", "fc-vm", err, vm.ErrNotSupported)
		}
	}
}
