package rbac

import (
	"slices"
	"testing"

	"github.com/activeshadow/structs"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

// TestEnsureRolePoliciesAddsPolicy verifies every migration is applied to a bare target role.
func TestEnsureRolePoliciesAddsPolicy(t *testing.T) {
	for _, migration := range rolePermissionMigrations {
		for _, name := range migration.roles {
			t.Run(name+"/"+migration.resource+"/"+migration.verb, func(t *testing.T) {
				role := &v1.RoleSpec{
					Name: name,
					Policies: []*v1.PolicySpec{
						{Resources: []string{resourceExperiments}, Verbs: []string{"get"}},
					},
				}

				if !ensureRolePolicies(role, nil) {
					t.Fatal("expected role to change")
				}

				if !roleAllows(role, migration.resource, migration.verb) {
					t.Fatalf("expected %s to allow %s on %s", name, migration.verb, migration.resource)
				}
			})
		}
	}
}

// TestEnsureRolePoliciesSkipsUntargetedRoles verifies roles outside a migration are left alone.
func TestEnsureRolePoliciesSkipsUntargetedRoles(t *testing.T) {
	role := &v1.RoleSpec{
		Name:     "VM Viewer",
		Policies: []*v1.PolicySpec{{Resources: []string{"vms"}, Verbs: []string{"get"}}},
	}

	if ensureRolePolicies(role, nil) {
		t.Fatal("expected role to stay unchanged")
	}

	if len(role.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(role.Policies))
	}
}

// TestEnsureRolePolicyUpdatesExistingPolicy verifies partial policies gain the verb and names.
func TestEnsureRolePolicyUpdatesExistingPolicy(t *testing.T) {
	migration := rolePermission{
		resource: resourceExperimentsFiles,
		verb:     verbCreate,
		roles:    nil,
	}
	role := &v1.RoleSpec{
		Name: roleExperimentUser,
		Policies: []*v1.PolicySpec{
			{
				Resources:     []string{migration.resource},
				ResourceNames: []string{"exp-a"},
				Verbs:         []string{"get"},
			},
		},
	}

	if !ensureRolePolicy(role, migration, []string{"exp-a", "exp-b"}) {
		t.Fatal("expected role to change")
	}

	policy := role.Policies[0]
	if !slices.Contains(policy.ResourceNames, "exp-a") || !slices.Contains(policy.ResourceNames, "exp-b") {
		t.Fatal("expected resource names to include exp-a and exp-b")
	}

	if !slices.Contains(policy.Verbs, migration.verb) {
		t.Fatalf("expected verbs to include %s", migration.verb)
	}
}

// TestEnsureRolePoliciesIdempotent verifies a second migration pass is a no-op.
func TestEnsureRolePoliciesIdempotent(t *testing.T) {
	for _, migration := range rolePermissionMigrations {
		for _, name := range migration.roles {
			t.Run(name+"/"+migration.resource+"/"+migration.verb, func(t *testing.T) {
				role := &v1.RoleSpec{
					Name: name,
					Policies: []*v1.PolicySpec{{
						Resources:     []string{resourceExperiments},
						ResourceNames: []string{"exp-a"},
						Verbs:         []string{"get"},
					}},
				}

				names := experimentResourceNames(role)

				if !ensureRolePolicies(role, names) {
					t.Fatal("expected role to change")
				}

				before := len(role.Policies)

				if ensureRolePolicies(role, names) {
					t.Fatal("expected role to stay unchanged")
				}

				if len(role.Policies) != before {
					t.Fatalf("expected %d policies, got %d", before, len(role.Policies))
				}
			})
		}
	}
}

// TestExperimentResourceNamesPreservesUserScope verifies embedded user experiment scopes are reused.
func TestExperimentResourceNamesPreservesUserScope(t *testing.T) {
	role := &v1.RoleSpec{
		Name: roleExperimentUser,
		Policies: []*v1.PolicySpec{
			{
				Resources:     []string{resourceExperiments},
				ResourceNames: []string{"exp-a"},
				Verbs:         []string{"get"},
			},
			{Resources: []string{"hosts"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
		},
	}

	names := experimentResourceNames(role)
	if len(names) != 1 || names[0] != "exp-a" {
		t.Fatalf("expected exp-a scope, got %#v", names)
	}
}

// TestExperimentResourceNamesDefaultsNil verifies unscoped roles stay unscoped.
func TestExperimentResourceNamesDefaultsNil(t *testing.T) {
	role := &v1.RoleSpec{
		Name: roleExperimentAdmin,
		Policies: []*v1.PolicySpec{
			{Resources: []string{resourceExperiments}, Verbs: []string{"get"}},
		},
	}

	names := experimentResourceNames(role)
	if len(names) != 0 {
		t.Fatalf("expected nil scope, got %#v", names)
	}
}

// TestExperimentResourceNamesIgnoresOtherPolicyScopes verifies only experiments scope is copied.
func TestExperimentResourceNamesIgnoresOtherPolicyScopes(t *testing.T) {
	role := &v1.RoleSpec{
		Name: roleExperimentUser,
		Policies: []*v1.PolicySpec{
			{Resources: []string{"vms"}, ResourceNames: []string{"vm-a"}, Verbs: []string{"get"}},
			{Resources: []string{"hosts"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
		},
	}

	names := experimentResourceNames(role)
	if len(names) != 0 {
		t.Fatalf("expected nil scope, got %#v", names)
	}
}

// TestSyncUserSpecForMigration verifies migrated user RBAC is prepared for saving.
func TestSyncUserSpecForMigration(t *testing.T) {
	user := &User{
		Spec: &v1.UserSpec{
			Username: "user-a",
			Role: &v1.RoleSpec{
				Name: roleExperimentUser,
				Policies: []*v1.PolicySpec{{
					Resources:     []string{resourceExperiments},
					ResourceNames: []string{"exp-a"},
					Verbs:         []string{"get"},
				}},
			},
		},
		config: &store.Config{Spec: map[string]any{}},
	}

	if !ensureRolePolicies(user.Spec.Role, experimentResourceNames(user.Spec.Role)) {
		t.Fatal("expected user role to change")
	}

	user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)
	rbacSpec, ok := user.config.Spec["rbac"].(map[string]any)
	if !ok {
		t.Fatalf("expected rbac spec map, got %#v", user.config.Spec["rbac"])
	}

	policies, ok := rbacSpec["policies"].([]any)
	if !ok {
		t.Fatalf("expected policies slice, got %#v", rbacSpec["policies"])
	}

	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
}

// roleAllows reports whether the spec's policies permit the verb on the resource.
func roleAllows(spec *v1.RoleSpec, resource, verb string) bool {
	return (&Role{Spec: spec}).Allowed(resource, verb)
}
