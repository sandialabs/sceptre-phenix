package rbac

import (
	"testing"

	v1 "phenix/types/version/v1"
)

func testRoleSpec() *v1.RoleSpec {
	return &v1.RoleSpec{
		Name: "test",
		Policies: []*v1.PolicySpec{
			{Resources: []string{"vms"}, ResourceNames: []string{"exp_*", "!exp_secret"}, Verbs: []string{"list"}},
			{Resources: []string{"experiments/*"}, ResourceNames: []string{"*"}, Verbs: []string{"*"}},
		},
	}
}

// Every way of getting a role builds its policy index once, so the copies
// handed to request contexts and broker clients share it rather than each
// Allowed call building its own.
//
//nolint:paralleltest // replaces the store
func TestRolesCarryAPolicyIndex(t *testing.T) {
	useStore(t,
		[]*v1.RoleSpec{testRoleSpec(), {Name: "disabled"}},
		[]*v1.UserSpec{{Username: "with-role", Role: testRoleSpec()}, {Username: "without-role"}},
	)

	roles := make(map[string]Role)

	stored, err := GetRoles()
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range stored {
		roles["stored "+r.Spec.Name] = *r
	}

	fromConfig, err := RoleFromConfig("test")
	if err != nil {
		t.Fatal(err)
	}

	roles["from config"] = *fromConfig

	for _, name := range []string{"with-role", "without-role"} {
		user, err := GetUser(name)
		if err != nil {
			t.Fatal(err)
		}

		if roles["user "+name], err = user.Role(); err != nil {
			t.Fatal(err)
		}
	}

	if len(roles) != 5 {
		t.Fatalf("got roles %v, want 5", roles)
	}

	for name, r := range roles {
		if r.mappedPolicies == nil {
			t.Errorf("%s has no policy index", name)
		}
	}
}

// A role allows nothing without a spec, or with a policy whose resource
// pattern is invalid.
func TestRoleDeniesEverything(t *testing.T) {
	t.Parallel()

	invalid := testRoleSpec()
	invalid.Policies = append(invalid.Policies, &v1.PolicySpec{Resources: []string{"[bad"}, Verbs: []string{"*"}})

	for name, r := range map[string]Role{
		"no role":                    {},
		"invalid pattern":            *newRole(invalid, nil),
		"invalid pattern, unindexed": {Spec: invalid},
	} {
		if r.Allowed("vms", "list", "exp_a") || r.Allowed("experiments/start", "update", "exp") {
			t.Errorf("%s allowed a request", name)
		}
	}
}

// A policy added to a role takes effect at once. Copies made before keep
// their own index, so AddPolicy does not change what they allow.
func TestAddPolicy(t *testing.T) {
	t.Parallel()

	r := newRole(&v1.RoleSpec{Name: "t"}, nil)
	earlier := *r

	r.AddPolicy([]string{"users"}, []string{"bob"}, []string{"get"})

	if !r.Allowed("users", "get", "bob") {
		t.Error("added policy is not honored")
	}

	if r.Allowed("users", "get", "alice") {
		t.Error("added policy allowed another name")
	}

	if earlier.Allowed("users", "get", "bob") {
		t.Error("AddPolicy changed a copy made before it")
	}
}
