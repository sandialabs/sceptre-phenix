package rbac

import (
	"slices"
	"testing"

	"github.com/activeshadow/structs"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

// TestEnsureExperimentFilesCreatePolicyAddsPolicy verifies missing upload permission is added.
func TestEnsureExperimentFilesCreatePolicyAddsPolicy(t *testing.T) {
	role := &v1.RoleSpec{
		Name: experimentAdminRole,
		Policies: []*v1.PolicySpec{{
			Resources: []string{"experiments"},
			Verbs:     []string{"get"},
		}},
	}

	if !ensureExperimentFilesCreatePolicy(role, nil) {
		t.Fatal("expected role to change")
	}

	if len(role.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(role.Policies))
	}

	policy := role.Policies[1]
	if !slices.Contains(policy.Resources, experimentFilesResource) {
		t.Fatalf("expected resources to include %s", experimentFilesResource)
	}
	if len(policy.ResourceNames) != 0 {
		t.Fatalf("expected nil resource names, got %#v", policy.ResourceNames)
	}
	if !slices.Contains(policy.Verbs, experimentFilesCreateVerb) {
		t.Fatalf("expected verbs to include %s", experimentFilesCreateVerb)
	}
}

// TestEnsureExperimentFilesCreatePolicyUpdatesExistingPolicy verifies partial policies are updated.
func TestEnsureExperimentFilesCreatePolicyUpdatesExistingPolicy(t *testing.T) {
	role := &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{
			{Resources: []string{experimentFilesResource}, ResourceNames: []string{"exp-a"}, Verbs: []string{"get"}},
		},
	}

	if !ensureExperimentFilesCreatePolicy(role, []string{"exp-a", "exp-b"}) {
		t.Fatal("expected role to change")
	}

	policy := role.Policies[0]
	if !slices.Contains(policy.ResourceNames, "exp-a") || !slices.Contains(policy.ResourceNames, "exp-b") {
		t.Fatal("expected resource names to include exp-a and exp-b")
	}
	if !slices.Contains(policy.Verbs, experimentFilesCreateVerb) {
		t.Fatalf("expected verbs to include %s", experimentFilesCreateVerb)
	}
}

// TestEnsureExperimentFilesCreatePolicyIdempotent verifies repeated migration does not duplicate values.
func TestEnsureExperimentFilesCreatePolicyIdempotent(t *testing.T) {
	role := &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{
			{Resources: []string{experimentFilesResource}, ResourceNames: []string{"exp-a"}, Verbs: []string{experimentFilesCreateVerb}},
		},
	}

	if ensureExperimentFilesCreatePolicy(role, []string{"exp-a"}) {
		t.Fatal("expected role to stay unchanged")
	}

	policy := role.Policies[0]
	if len(policy.ResourceNames) != 1 {
		t.Fatalf("expected 1 resource name, got %d", len(policy.ResourceNames))
	}
	if len(policy.Verbs) != 1 {
		t.Fatalf("expected 1 verb, got %d", len(policy.Verbs))
	}
}

// TestExperimentResourceNamesPreservesUserScope verifies embedded user experiment scopes are reused.
func TestExperimentResourceNamesPreservesUserScope(t *testing.T) {
	role := &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{
			{Resources: []string{"experiments"}, ResourceNames: []string{"exp-a"}, Verbs: []string{"get"}},
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
		Name: experimentAdminRole,
		Policies: []*v1.PolicySpec{{
			Resources: []string{"experiments"},
			Verbs:     []string{"get"},
		}},
	}

	names := experimentResourceNames(role)
	if len(names) != 0 {
		t.Fatalf("expected nil scope, got %#v", names)
	}
}

// TestExperimentResourceNamesIgnoresOtherPolicyScopes verifies only experiments scope is copied.
func TestExperimentResourceNamesIgnoresOtherPolicyScopes(t *testing.T) {
	role := &v1.RoleSpec{
		Name: "Experiment User",
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

// TestSyncUserSpecForExperimentFilesMigration verifies migrated user RBAC is prepared for saving.
func TestSyncUserSpecForExperimentFilesMigration(t *testing.T) {
	user := &User{
		Spec: &v1.UserSpec{
			Username: "user-a",
			Role: &v1.RoleSpec{
				Name:     experimentUserRole,
				Policies: []*v1.PolicySpec{{Resources: []string{"experiments"}, ResourceNames: []string{"exp-a"}, Verbs: []string{"get"}}},
			},
		},
		config: &store.Config{Spec: map[string]any{}},
	}

	if !ensureExperimentFilesCreatePolicy(user.Spec.Role, experimentResourceNames(user.Spec.Role)) {
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

func TestEnsureServicePermissions(t *testing.T) {
	tests := []struct {
		name        string
		roleName    string
		permissions []servicePermission
	}{
		{
			name:     "experiment admin",
			roleName: experimentAdminRole,
			permissions: []servicePermission{
				{resource: builderResource, verb: getVerb},
				{resource: builderResource, verb: postVerb},
				{resource: builderResource, verb: putVerb},
				{resource: scorchResource, verb: getVerb},
				{resource: tunnelerResource, verb: getVerb},
			},
		},
		{
			name:     "experiment user",
			roleName: experimentUserRole,
			permissions: []servicePermission{
				{resource: builderResource, verb: getVerb},
				{resource: builderResource, verb: postVerb},
				{resource: scorchResource, verb: getVerb},
				{resource: tunnelerResource, verb: getVerb},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			role := &v1.RoleSpec{Name: test.roleName}

			if !ensureServicePermissions(role) {
				t.Fatal("expected role to change")
			}

			migrated := Role{Spec: role}
			for _, permission := range test.permissions {
				if !migrated.Allowed(permission.resource, permission.verb) {
					t.Fatalf("expected %s:%s permission", permission.resource, permission.verb)
				}
			}

			if ensureServicePermissions(role) {
				t.Fatal("expected repeated migration to be idempotent")
			}
		})
	}
}

func TestEnsureServicePermissionsLeavesCustomRolesUnchanged(t *testing.T) {
	role := &v1.RoleSpec{
		Name: "Custom Role",
		Policies: []*v1.PolicySpec{{
			Resources: []string{"experiments"},
			Verbs:     []string{"list"},
		}},
	}

	if ensureServicePermissions(role) {
		t.Fatal("expected custom role to stay unchanged")
	}

	if len(role.Policies) != 1 {
		t.Fatalf("expected one policy, got %d", len(role.Policies))
	}
}
