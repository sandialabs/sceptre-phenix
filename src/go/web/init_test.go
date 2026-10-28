package web

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// storedSpecs returns the spec of every stored role and user, by kind and
// name.
func storedSpecs(t *testing.T) map[string]map[string]any {
	t.Helper()

	configs, err := store.List("Role", "User")
	if err != nil {
		t.Fatalf("store.List(Role, User) error = %v", err)
	}

	specs := make(map[string]map[string]any, len(configs))
	for _, c := range configs {
		specs[c.Kind+"/"+c.Metadata.Name] = c.Spec
	}

	return specs
}

// Init brings an old store's roles to the defaults: Global Admin gets the
// */* resource name, and the other roles their newer permissions. It runs at
// each server start, so a second run changes nothing.
//
//nolint:paralleltest // replaces the store
func TestInitIsIdempotent(t *testing.T) {
	useDefaultRoles(t)

	if err := Init(); err != nil {
		t.Fatalf("Init() on the default roles error = %v", err)
	}

	defaults := storedSpecs(t)

	useDefaultRoles(t)

	admin, err := rbac.RoleFromConfig("global-admin")
	if err != nil {
		t.Fatalf("rbac.RoleFromConfig(global-admin) error = %v", err)
	}

	// as stored before resource names could hold a slash
	admin.Spec.Policies[0].ResourceNames = []string{"*"}

	if err := admin.Save(); err != nil {
		t.Fatalf("saving the old global-admin role: %v", err)
	}

	expAdmin, err := rbac.RoleFromConfig("experiment-admin")
	if err != nil {
		t.Fatalf("rbac.RoleFromConfig(experiment-admin) error = %v", err)
	}

	// as stored before experiment files and jobs had permissions
	expAdmin.Spec.Policies = slices.DeleteFunc(expAdmin.Spec.Policies, func(p *v1.PolicySpec) bool {
		return slices.Contains(p.Resources, "experiments/files") || slices.Contains(p.Resources, "jobs")
	})

	if err := expAdmin.Save(); err != nil {
		t.Fatalf("saving the old experiment-admin role: %v", err)
	}

	if cmp.Equal(defaults, storedSpecs(t)) {
		t.Fatal("the old store holds the default roles, want older ones")
	}

	// a migration appends the policy it adds, and policy order does not matter
	anyOrder := cmpopts.SortSlices(func(a, b any) bool { return fmt.Sprint(a) < fmt.Sprint(b) })

	for run := 1; run <= 2; run++ {
		if err := Init(); err != nil {
			t.Fatalf("Init() run %d error = %v", run, err)
		}

		if diff := cmp.Diff(defaults, storedSpecs(t), anyOrder); diff != "" {
			t.Errorf("roles and users after Init() run %d on an old store mismatch (-want +got):\n%s", run, diff)
		}
	}
}

// Init fails on a store without the Global Admin role, as one that was
// never initialized.
//
//nolint:paralleltest // replaces the store
func TestInitWithoutGlobalAdmin(t *testing.T) {
	storetest.UseBolt(t)

	if err := Init(); !errors.Is(err, rbac.ErrRoleNotFound) {
		t.Errorf("Init() on an empty store error = %v, want %v", err, rbac.ErrRoleNotFound)
	}
}
