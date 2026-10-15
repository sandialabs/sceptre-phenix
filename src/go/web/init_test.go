package web

import (
	"testing"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
)

// The Scorch roles are created once on an install that has its other default
// configs, and a deleted one is not created again.
//
//nolint:paralleltest // replaces the store
func TestEnsureServiceRolesCreatesOnce(t *testing.T) {
	storetest.Use(t)

	if err := ensureServiceRoles(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"scorch-viewer", "scorch-admin"} {
		if _, err := rbac.RoleFromConfig(name); err != nil {
			t.Errorf("role %s was not created: %v", name, err)
		}
	}

	if _, err := rbac.RoleFromConfig("experiment-user"); err == nil {
		t.Error("another default role was created")
	}

	admin, _ := store.NewConfig("role/scorch-admin")
	if err := store.Delete(admin); err != nil {
		t.Fatal(err)
	}

	if err := ensureServiceRoles(); err != nil {
		t.Fatal(err)
	}

	if _, err := rbac.RoleFromConfig("scorch-admin"); err == nil {
		t.Error("a deleted Scorch role was created again")
	}
}
