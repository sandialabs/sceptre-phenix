package rbac

import (
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// useStore gives the test a store of its own holding the given roles and
// users.
func useStore(t *testing.T, roles []*v1.RoleSpec, users []*v1.UserSpec) {
	t.Helper()

	storetest.UseBolt(t)

	create := func(name string, spec any) {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatalf("NewConfig(%s) error = %v", name, err)
		}

		c.Spec = structs.MapDefaultCase(spec, structs.CASESNAKE)

		if err := store.Create(c); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	for _, role := range roles {
		create("role/"+strings.ReplaceAll(strings.ToLower(role.Name), " ", "-"), role)
	}

	for _, user := range users {
		create("user/"+user.Username, user)
	}
}

func policy(resource string, names []string, verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{resource}, ResourceNames: names, Verbs: verbs}
}

// TestEnsureExperimentFilesCreatePolicyAddsPolicy verifies missing upload permission is added.
func TestEnsureExperimentFilesCreatePolicyAddsPolicy(t *testing.T) {
	role := &v1.RoleSpec{Name: "Experiment Admin", Policies: []*v1.PolicySpec{{Resources: []string{"experiments"}, Verbs: []string{"get"}}}}

	if !ensurePolicy(role, experimentFilesResource, []string{experimentFilesCreateVerb}, nil) {
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
		Name: "Experiment User",
		Policies: []*v1.PolicySpec{
			{Resources: []string{experimentFilesResource}, ResourceNames: []string{"exp-a"}, Verbs: []string{"get"}},
		},
	}

	if !ensurePolicy(role, experimentFilesResource, []string{experimentFilesCreateVerb}, []string{"exp-a", "exp-b"}) {
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
		Name: "Experiment User",
		Policies: []*v1.PolicySpec{
			{Resources: []string{experimentFilesResource}, ResourceNames: []string{"exp-a"}, Verbs: []string{experimentFilesCreateVerb}},
		},
	}

	if ensurePolicy(role, experimentFilesResource, []string{experimentFilesCreateVerb}, []string{"exp-a"}) {
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
		Name: "Experiment User",
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
	role := &v1.RoleSpec{Name: "Experiment Admin", Policies: []*v1.PolicySpec{{Resources: []string{"experiments"}, Verbs: []string{"get"}}}}

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
				Name:     "Experiment User",
				Policies: []*v1.PolicySpec{{Resources: []string{"experiments"}, ResourceNames: []string{"exp-a"}, Verbs: []string{"get"}}},
			},
		},
		config: &store.Config{Spec: map[string]any{}},
	}

	names := experimentResourceNames(user.Spec.Role)
	if !ensurePolicy(user.Spec.Role, experimentFilesResource, []string{experimentFilesCreateVerb}, names) {
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

// The null resource names migration removes stored resourceNames: null from
// role and user policies, keeping the rest of the config.
//
//nolint:paralleltest // replaces the store
func TestDropNullResourceNames(t *testing.T) {
	storetest.UseBolt(t)

	policies := func() []any {
		return []any{
			map[string]any{"resources": []any{"vms"}, "resourceNames": nil, "verbs": []any{"get"}},
			map[string]any{"resources": []any{"hosts"}, "resourceNames": []any{"*"}, "verbs": []any{"list"}},
		}
	}

	stored := map[string]map[string]any{
		"role/legacy": {"roleName": "Legacy", "policies": policies()},
		"user/legacy": {
			"username": "legacy",
			"password": "hash",
			"rbac":     map[string]any{"roleName": "Legacy", "policies": policies()},
		},
	}

	for name, spec := range stored {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatalf("NewConfig(%s) error = %v", name, err)
		}

		c.Spec = spec

		if err := store.Create(c); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	unnamed := map[string]any{"resources": []any{"vms"}, "verbs": []any{"get"}}
	named := map[string]any{"resources": []any{"hosts"}, "resourceNames": []any{"*"}, "verbs": []any{"list"}}
	want := map[string]map[string]any{
		"role/legacy": {"roleName": "Legacy", "policies": []any{unnamed, named}},
		"user/legacy": {
			"username": "legacy",
			"password": "hash",
			"rbac":     map[string]any{"roleName": "Legacy", "policies": []any{unnamed, named}},
		},
	}

	for _, pass := range []string{"first", "repeated"} {
		if err := DropNullResourceNames(); err != nil {
			t.Fatalf("DropNullResourceNames() %s pass error = %v", pass, err)
		}

		got := make(map[string]map[string]any, len(stored))

		for name := range stored {
			c, _ := store.NewConfig(name)
			if err := store.Get(c); err != nil {
				t.Fatalf("getting %s: %v", name, err)
			}

			got[name] = c.Spec
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("DropNullResourceNames() %s pass: stored specs mismatch (-want +got):\n%s", pass, diff)
		}
	}

	c, _ := store.NewConfig("role/legacy")
	if err := store.Get(c); err != nil {
		t.Fatalf("getting role/legacy: %v", err)
	}

	if err := types.ValidateConfigSpec(*c); err != nil {
		t.Errorf("ValidateConfigSpec(migrated role/legacy) error = %v", err)
	}
}
