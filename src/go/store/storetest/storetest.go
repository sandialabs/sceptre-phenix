// Package storetest gives a test a phenix store of its own. It is imported
// only by tests, as net/http/httptest is.
package storetest

import (
	"path/filepath"
	"testing"

	"phenix/store"
)

// NewBolt returns a new, empty BoltDB in a temporary directory of the test.
func NewBolt(tb testing.TB) *store.BoltDB {
	tb.Helper()

	db := new(store.BoltDB)
	endpoint := "bolt://" + filepath.Join(tb.TempDir(), "store.bdb")

	if err := db.Init(store.Endpoint(endpoint)); err != nil {
		tb.Fatalf("BoltDB.Init(%s) error = %v", endpoint, err)
	}

	return db
}

// Use makes s, or a new BoltDB from NewBolt when s is not given, the phenix
// store until the test ends. Tests that call it must not run in parallel.
func Use(tb testing.TB, s ...store.Store) {
	tb.Helper()

	if len(s) > 1 {
		tb.Fatalf("storetest.Use got %d stores, want at most 1", len(s))
	}

	var db store.Store
	if len(s) == 1 {
		db = s[0]
	} else {
		db = NewBolt(tb)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	// BoltDB opens its file for each call, so there is nothing to close
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
