package store

import (
	"path/filepath"
	"testing"
)

// The BoltDB RecordStore is tested by the shared conformance suite in
// record_conformance_test.go.

func newTestBoltDB(t *testing.T) (Store, string) { //nolint:ireturn // mirrors the store factory
	t.Helper()

	path := filepath.Join(t.TempDir(), "phenix.bdb")

	s := NewBoltDB()
	if err := s.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing BoltDB store returned error: %v", err)
	}

	return s, path
}
