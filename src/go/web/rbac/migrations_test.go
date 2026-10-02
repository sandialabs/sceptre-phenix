package rbac

import (
	"reflect"
	"strings"
	"testing"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

// useStore gives the test a store of its own holding the given roles and
// users.
func useStore(t *testing.T, roles []*v1.RoleSpec, users []*v1.UserSpec) {
	t.Helper()

	storetest.Use(t)

	create := func(name string, spec any) {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatal(err)
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

// storedPolicies reads back the policies of every stored role and user role.
func storedPolicies(t *testing.T) map[string][]*v1.PolicySpec {
	t.Helper()

	roles, err := GetRoles()
	if err != nil {
		t.Fatal(err)
	}

	users, err := GetUsers()
	if err != nil {
		t.Fatal(err)
	}

	policies := make(map[string][]*v1.PolicySpec)

	for _, role := range roles {
		policies["role "+role.Spec.Name] = role.Spec.Policies
	}

	for _, user := range users {
		if user.Spec.Role != nil {
			policies["user "+user.Username()] = user.Spec.Role.Policies
		}
	}

	return policies
}

func policy(resource string, names []string, verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{resource}, ResourceNames: names, Verbs: verbs}
}

// The experiment files migrations let stored Experiment Admin and Experiment
// User roles, and users with those roles, upload experiment files, and only
// Experiment Admins delete them. A user's new permission keeps the user's
// experiment scope. Running them again changes nothing.
//
//nolint:paralleltest // replaces the store
func TestEnsureExperimentFilesPermissions(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		vms         = policy("vms", []string{"vm-a"}, "get")
		scopedAB    = policy("experiments", []string{"exp-a", "exp-b"}, "get")
		scopedA     = policy("experiments", []string{"exp-a"}, "get")
	)

	role := func(name string, policies ...*v1.PolicySpec) *v1.RoleSpec {
		return &v1.RoleSpec{Name: name, Policies: policies}
	}

	user := func(name string, role *v1.RoleSpec) *v1.UserSpec {
		return &v1.UserSpec{Username: name, Role: role}
	}

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentAdminRole, experiments),
			// a files policy the role already has gains the verb
			role(experimentUserRole, experiments, policy(experimentFilesResource, nil, "get")),
			role("Experiment Viewer", experiments),
			role("VM Admin", vms),
		},
		[]*v1.UserSpec{
			user("admin", role(experimentAdminRole, experiments)),
			user("scoped-admin", role(experimentAdminRole,
				scopedA, policy(experimentFilesResource, []string{"exp-a"}, "create"))),
			user("scoped-user", role(experimentUserRole,
				scopedAB, policy(experimentFilesResource, []string{"exp-a"}, "get"))),
			// only an experiments policy scopes the new permission
			user("vm-user", role(experimentUserRole, vms)),
			user("viewer", role("Experiment Viewer", experiments)),
			user("no-role", nil),
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment Admin": {
			experiments, policy(experimentFilesResource, nil, "create", "delete"),
		},
		"role Experiment User": {
			experiments, policy(experimentFilesResource, nil, "get", "create"),
		},
		"role Experiment Viewer": {experiments},
		"role VM Admin":          {vms},
		"user admin": {
			experiments, policy(experimentFilesResource, nil, "create", "delete"),
		},
		"user scoped-admin": {
			scopedA, policy(experimentFilesResource, []string{"exp-a"}, "create", "delete"),
		},
		"user scoped-user": {
			scopedAB, policy(experimentFilesResource, []string{"exp-a", "exp-b"}, "get", "create"),
		},
		"user vm-user": {vms, policy(experimentFilesResource, nil, "create")},
		"user viewer":  {experiments},
	}

	for _, pass := range []string{"first", "repeated"} {
		if err := EnsureExperimentFilesCreatePermission(); err != nil {
			t.Fatalf("%s create migration: %v", pass, err)
		}

		if err := EnsureExperimentFilesDeletePermission(); err != nil {
			t.Fatalf("%s delete migration: %v", pass, err)
		}

		got := storedPolicies(t)

		for name, policies := range want {
			if !reflect.DeepEqual(got[name], policies) {
				t.Errorf("%s migration: %s policies = %s, want %s", pass, name, describe(got[name]), describe(policies))
			}
		}

		if len(got) != len(want) {
			t.Errorf("%s migration: %d roles and user roles stored, want %d", pass, len(got), len(want))
		}
	}
}

func describe(policies []*v1.PolicySpec) string {
	out := make([]string, len(policies))
	for i, p := range policies {
		out[i] = strings.Join(p.Resources, ",") + " " + strings.Join(p.ResourceNames, ",") + " " + strings.Join(p.Verbs, ",")
	}

	return "[" + strings.Join(out, "; ") + "]"
}
