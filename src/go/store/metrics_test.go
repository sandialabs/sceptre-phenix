package store

import (
	"errors"
	"path/filepath"
	"testing"

	"phenix/util/metrics/metricstest"
)

// Each store operation is timed by operation; a missing config is an answer,
// not a failure of the store.
func TestBoltRecordsOperationMetrics(t *testing.T) {
	b := newTestBolt(t, filepath.Join(t.TempDir(), "store.bdb"))

	tests := []struct {
		operation string
		run       func() error
		result    string
	}{
		{"create", func() error { return b.Create(newTestConfig(t, "topology/foo")) }, "ok"},
		{"get", func() error { return b.Get(newTestConfig(t, "topology/foo")) }, "ok"},
		{"get", func() error { return b.Get(newTestConfig(t, "topology/missing")) }, "ok"},
		{"list", func() error { _, err := b.List("Topology"); return err }, "ok"},
		{"update", func() error { return b.Update(newTestConfig(t, "topology/missing")) }, "ok"},
		{"delete", func() error { return b.Delete(newTestConfig(t, "topology/foo")) }, "ok"},
	}

	for _, tt := range tests {
		labels := map[string]string{"operation": tt.operation, "result": tt.result}
		before := metricstest.Value(t, "phenix_store_operation_duration_seconds", labels)

		if err := tt.run(); err != nil && !errors.Is(err, ErrNotExist) {
			t.Fatalf("%s error = %v", tt.operation, err)
		}

		if got := metricstest.Value(t, "phenix_store_operation_duration_seconds", labels) - before; got != 1 {
			t.Errorf("%s: phenix_store_operation_duration_seconds %v moved by %v, want 1", tt.operation, labels, got)
		}
	}

	// a store that cannot be opened fails
	broken := &BoltDB{path: filepath.Join(t.TempDir(), "missing", "store.bdb")}
	labels := map[string]string{"operation": "list", "result": "error"}
	before := metricstest.Value(t, "phenix_store_operation_duration_seconds", labels)

	if _, err := broken.List("Topology"); err == nil {
		t.Fatal("List() on a store in a missing directory error = nil, want an error")
	}

	if got := metricstest.Value(t, "phenix_store_operation_duration_seconds", labels) - before; got != 1 {
		t.Errorf("phenix_store_operation_duration_seconds %v moved by %v, want 1", labels, got)
	}
}
