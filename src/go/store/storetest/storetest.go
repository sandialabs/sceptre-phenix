// Package storetest gives a test a config store of its own. It is imported
// only by tests, as net/http/httptest is.
package storetest

import (
	"path/filepath"
	"testing"

	"phenix/store"
)

// Use makes a new, empty BoltDB file in the test's temporary directory the
// package store until the test ends. Tests using it must not run in parallel
// with others that use the package store.
func Use(tb testing.TB) {
	tb.Helper()

	s := store.NewBoltDB()
	if err := s.Init(store.Endpoint("bolt://" + filepath.Join(tb.TempDir(), "store.bdb"))); err != nil {
		tb.Fatalf("initializing store: %v", err)
	}

	original := store.DefaultStore
	store.DefaultStore = s //nolint:reassign // the test's store

	// BoltDB opens its file for each call, so there is nothing to close
	tb.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore the package store
}
