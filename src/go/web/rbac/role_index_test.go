package rbac

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// Every way of getting a role builds its policy index once, so copies share it.
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
		t.Fatalf("GetRoles() error = %v", err)
	}

	for _, r := range stored {
		roles["stored "+r.Spec.Name] = *r
	}

	fromConfig, err := RoleFromConfig("test")
	if err != nil {
		t.Fatalf("RoleFromConfig(test) error = %v", err)
	}

	roles["from config"] = *fromConfig

	for _, name := range []string{"with-role", "without-role"} {
		user, err := GetUser(name)
		if err != nil {
			t.Fatalf("GetUser(%s) error = %v", name, err)
		}

		if roles["user "+name], err = user.Role(); err != nil {
			t.Fatalf("Role() of user %s error = %v", name, err)
		}
	}

	want := []string{"from config", "stored disabled", "stored test", "user with-role", "user without-role"}
	if diff := cmp.Diff(want, slices.Sorted(maps.Keys(roles))); diff != "" {
		t.Errorf("roles gathered mismatch (-want +got):\n%s", diff)
	}

	for name, r := range roles {
		if r.mappedPolicies == nil {
			t.Errorf("mappedPolicies of the %s role = nil, want a policy index", name)
		}
	}
}

// A role allows nothing without a spec, or with a policy whose resource
// pattern is invalid, also one bad only past where a probe name stops
// matching, such as "v*[".
func TestRoleDeniesEverything(t *testing.T) {
	t.Parallel()

	invalid := testRoleSpec()
	invalid.Policies = append(invalid.Policies, &v1.PolicySpec{Resources: []string{"[bad"}, Verbs: []string{"*"}})

	partlyBad := &v1.RoleSpec{Name: "r", Policies: []*v1.PolicySpec{
		{Resources: []string{"v*[", "vms"}, ResourceNames: []string{"*"}, Verbs: []string{"*"}},
	}}

	for name, r := range map[string]Role{
		"no role":                       {},
		"invalid pattern":               *newRole(invalid, nil),
		"invalid pattern, unindexed":    {Spec: invalid},
		"partly bad pattern":            *newRole(partlyBad, nil),
		"partly bad pattern, unindexed": {Spec: partlyBad},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, q := range [][]string{
				{"vms", "list", "exp_a"}, {"experiments/start", "update", "exp"}, {"vms", "list"}, {"vms", "get", "vm1"},
			} {
				if r.Allowed(q[0], q[1], q[2:]...) {
					t.Errorf("Allowed(%q) = true, want false", q)
				}
			}
		})
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
		t.Error("Allowed(users, get, bob) after AddPolicy(users, bob, get) = false, want true")
	}

	if r.Allowed("users", "get", "alice") {
		t.Error("Allowed(users, get, alice) after AddPolicy(users, bob, get) = true, want false")
	}

	if earlier.Allowed("users", "get", "bob") {
		t.Error("Allowed(users, get, bob) of a copy made before AddPolicy = true, want false")
	}

	// a policy with a bad resource pattern makes the role allow nothing
	r.AddPolicy([]string{"[bad"}, nil, []string{"*"})

	if r.Allowed("users", "get", "bob") {
		t.Error("Allowed(users, get, bob) after AddPolicy([bad, *) = true, want false")
	}
}
