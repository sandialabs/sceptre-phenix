// Package storetest gives a test a phenix store of its own. Only tests import
// it.
package storetest

import (
	"path/filepath"
	"testing"

	"phenix/store"
)

// NewBolt returns a new BoltDB in a temporary directory of the test.
func NewBolt(tb testing.TB) *store.BoltDB {
	tb.Helper()

	db := new(store.BoltDB)
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(tb.TempDir(), "phenix.bdb"))); err != nil {
		tb.Fatalf("initializing BoltDB returned error: %v", err)
	}

	return db
}

// Use makes s the phenix store until the test ends. Tests that call it must
// not run in parallel.
func Use(tb testing.TB, s store.Store) {
	tb.Helper()

	previous := store.DefaultStore
	store.DefaultStore = s //nolint:reassign // the test's own store

	tb.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store
}

// UseBolt makes a new BoltDB the phenix store until the test ends, and
// returns it.
func UseBolt(tb testing.TB) *store.BoltDB {
	tb.Helper()

	db := NewBolt(tb)
	Use(tb, db)

	return db
}
