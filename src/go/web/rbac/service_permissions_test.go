package rbac

import (
	"reflect"
	"slices"
	"testing"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

const (
	terminalsResource = "scorch/terminals"
	expA              = "exp-a"
	expB              = "exp-b"
)

// scorchChecks are the Scorch permissions the web server checks, as resource
// and verb.
var scorchChecks = [][2]string{ //nolint:gochecknoglobals // test data
	{scorchResource, "get"}, {scorchResource, "post"}, {scorchResource, "delete"}, {terminalsResource, "write"},
}

// useDefaultRoles gives the test a store of its own holding the default
// configs, as a new install has them.
func useDefaultRoles(t *testing.T) {
	t.Helper()

	storetest.Use(t)

	if err := config.Init(); err != nil {
		t.Fatal(err)
	}
}

// assigned returns the stored role as the Users page assigns it to a user of
// experiment exp-a.
func assigned(t *testing.T, name string) *Role {
	t.Helper()

	role, err := RoleFromConfig(name)
	if err != nil {
		t.Fatal(err)
	}

	if err := role.SetResourceNames(expA, expA+"/*"); err != nil {
		t.Fatal(err)
	}

	return role
}

// Each built-in role, assigned to a user of exp-a, has exactly the Scorch
// permissions listed for it on exp-a; scorch/terminals write only Global Admin
// and Scorch Admin. A role scoped to the user's experiments has none of them
// on another experiment.
//
//nolint:paralleltest // replaces the store
func TestDefaultRolesScorchPermissions(t *testing.T) {
	useDefaultRoles(t)

	var (
		all     = [][2]string{{scorchResource, "get"}, {scorchResource, "post"}, {scorchResource, "delete"}}
		view    = [][2]string{{scorchResource, "get"}}
		allowed = map[string][][2]string{
			"global-admin":      scorchChecks,
			"global-viewer":     view,
			"experiment-admin":  all,
			"experiment-user":   all,
			"experiment-viewer": view,
			"vm-admin":          all,
			"vm-viewer":         nil,
			"scorch-viewer":     view,
			"scorch-admin":      scorchChecks,
			"disabled":          nil,
		}
		global = []string{"global-admin", "global-viewer"}
	)

	for name, want := range allowed {
		role := assigned(t, name)

		for _, check := range scorchChecks {
			if got, want := role.Allowed(check[0], check[1], expA), slices.Contains(want, check); got != want {
				t.Errorf("%s %s %s: %t, want %t", name, check[0], check[1], got, want)
			}

			if !slices.Contains(global, name) && role.Allowed(check[0], check[1], expB) {
				t.Errorf("%s may %s %s on another experiment", name, check[1], check[0])
			}
		}
	}
}

// The new and changed built-in roles, assigned to a user of exp-a, reach only
// what they should of exp-a.
//
//nolint:paralleltest // replaces the store
func TestAssignedDefaultRolesScope(t *testing.T) {
	useDefaultRoles(t)

	type check struct {
		resource, verb, name string
		want                 bool
	}

	for name, checks := range map[string][]check{
		"experiment-user": {
			{forwardsResource, "create", "exp-a/vm1", true},
			{forwardsResource, "delete", "exp-a/vm1", true},
			{forwardsResource, "create", "exp-b/vm1", false},
			{"vms/redeploy", "create", "exp-a/vm1", false},
		},
		"scorch-viewer": {
			{"experiments", "get", expA, true},
			{"experiments/apps", "get", expA, true},
			{"experiments/files", "get", expA, true},
			{"experiments/files", "create", expA, false},
			{"experiments/start", "update", expA, false},
			{"experiments/trigger", "create", expA, false},
			{"vms", "list", "exp-a/vm1", false},
		},
		"scorch-admin": {
			{"experiments/files", "list", expA, true},
			{"vms", "list", "exp-a/vm1", true},
			{"vms/screenshot", "get", "exp-a/vm1", true},
			{"vms/vnc", "get", "exp-a/vm1", false},
			{"vms", "update", "exp-a/vm1", false},
			{"experiments/trigger", "create", expA, false},
		},
	} {
		role := assigned(t, name)

		for _, c := range checks {
			if got := role.Allowed(c.resource, c.verb, c.name); got != c.want {
				t.Errorf("%s %s %s %s: %t, want %t", name, c.resource, c.verb, c.name, got, c.want)
			}
		}
	}
}

// Every built-in role config passes the Role schema, as it must for an
// administrator to edit it.
//
//nolint:paralleltest // replaces the store
func TestDefaultRolesValidate(t *testing.T) {
	useDefaultRoles(t)

	roles, err := store.List("Role")
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range roles {
		if err := types.ValidateConfigSpec(role); err != nil {
			t.Errorf("default role %s fails its schema: %v", role.Metadata.Name, err)
		}
	}
}

// A new install's built-in roles already have what the service permission
// migration gives, so it changes none of them.
//
//nolint:paralleltest // replaces the store
func TestDefaultRolesIncludeServiceGrants(t *testing.T) {
	useDefaultRoles(t)

	for name := range serviceGrants {
		role, err := RoleFromConfig(name)
		if err != nil {
			t.Fatal(err)
		}

		if grantService(role.Spec, false) {
			t.Errorf("default role %s lacks a permission the migration gives", name)
		}
	}
}

// The service permission migration gives the stored built-in roles, and the
// roles of users assigned them, their Scorch and port forward permissions
// once: a user's new policy takes the user's VM or experiment names, a policy
// of only that resource gains the verb, wildcards count, and custom roles are
// left alone. Permissions an administrator later removes stay removed.
//
//nolint:paralleltest // replaces the store
func TestEnsureServicePermissions(t *testing.T) {
	var (
		scopedA     = []string{expA, expA + "/*"}
		experiments = func(names []string) *v1.PolicySpec { return policy("experiments", names, "list", "get") }
		vms         = func(names []string) *v1.PolicySpec {
			return &v1.PolicySpec{Resources: []string{"vms", "vms/*"}, ResourceNames: names, Verbs: []string{"list", "get"}}
		}
		everything = &v1.PolicySpec{Resources: []string{"*", "*/*"}, ResourceNames: []string{"*"}, Verbs: []string{"get"}}
	)

	role := func(name string, policies ...*v1.PolicySpec) *v1.RoleSpec {
		return &v1.RoleSpec{Name: name, Policies: policies}
	}

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentUserRole, experiments(nil), vms(nil)),
			// a scorch policy the role already has gains the verbs
			role(experimentAdminRole, experiments(nil), policy(scorchResource, nil, "get")),
			// wildcards already give it scorch get
			role(experimentViewerRole, everything),
			role("Custom", experiments(nil)),
		},
		[]*v1.UserSpec{
			{Username: "user", Role: role(experimentUserRole, experiments(scopedA), vms([]string{"exp-a/*"}))},
			{Username: "custom", Role: role("Custom", experiments(scopedA))},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment User": {
			experiments(nil), vms(nil),
			policy(scorchResource, nil, "get", "post", "delete"),
			policy(forwardsResource, nil, "create", "delete"),
		},
		"role Experiment Admin":  {experiments(nil), policy(scorchResource, nil, "get", "post", "delete")},
		"role Experiment Viewer": {everything},
		"role Custom":            {experiments(nil)},
		"user user": {
			experiments(scopedA), vms([]string{"exp-a/*"}),
			policy(scorchResource, scopedA, "get", "post", "delete"),
			policy(forwardsResource, []string{"exp-a/*"}, "create", "delete"),
		},
		"user custom": {experiments(scopedA)},
	}

	for _, pass := range []string{"first", "repeated"} {
		if err := EnsureServicePermissions(); err != nil {
			t.Fatalf("%s migration: %v", pass, err)
		}

		got := storedPolicies(t)

		for name, policies := range want {
			if !reflect.DeepEqual(got[name], policies) {
				t.Errorf("%s migration: %s policies = %s, want %s", pass, name, describe(got[name]), describe(policies))
			}
		}
	}

	for name, annotated := range map[string]bool{experimentUserRole: true, experimentViewerRole: true, "Custom": false} {
		r, err := RoleFromConfig(name)
		if err != nil {
			t.Fatal(err)
		}

		if got := r.config.HasAnnotation(servicePermissionsAnnotation); got != annotated {
			t.Errorf("role %s annotated %t, want %t", name, got, annotated)
		}

		if err := types.ValidateConfigSpec(*r.config); err != nil {
			t.Errorf("role %s fails its schema: %v", name, err)
		}
	}

	// an administrator takes Scorch away from the role and its user
	user, err := GetUser("user")
	if err != nil {
		t.Fatal(err)
	}

	r, err := RoleFromConfig(experimentUserRole)
	if err != nil {
		t.Fatal(err)
	}

	for _, spec := range []*v1.RoleSpec{r.Spec, user.Spec.Role} {
		spec.Policies = slices.DeleteFunc(spec.Policies, func(p *v1.PolicySpec) bool {
			return slices.Contains(p.Resources, scorchResource)
		})
	}

	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	if err := user.SetRole(&Role{Spec: user.Spec.Role}); err != nil {
		t.Fatal(err)
	}

	if err := EnsureServicePermissions(); err != nil {
		t.Fatal(err)
	}

	for name, policies := range storedPolicies(t) {
		if (name == "role "+experimentUserRole || name == "user user") &&
			slices.ContainsFunc(policies, func(p *v1.PolicySpec) bool { return slices.Contains(p.Resources, scorchResource) }) {
			t.Errorf("a restart gave %s back the Scorch access an administrator removed", name)
		}
	}
}
