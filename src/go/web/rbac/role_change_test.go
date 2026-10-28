package rbac

import (
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/mapstructure"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

// ValidatePolicies refuses a policy without resources or verbs, a resource
// that matches no known resource, an unknown verb, and a bad resource name.
func TestValidatePolicies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		policies []*v1.PolicySpec
		wantErr  bool
	}{
		"no policies":             {policies: nil},
		"a known resource":        {policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "list", "get")}},
		"wildcards":               {policies: []*v1.PolicySpec{{Resources: []string{"*", "*/*"}, Verbs: []string{"*"}}}},
		"a resource pattern":      {policies: []*v1.PolicySpec{policy("vms/*", nil, "update")}},
		"a resource without verb": {policies: []*v1.PolicySpec{policy("vlans/blacklist", nil, "create")}},
		"negated names":           {policies: []*v1.PolicySpec{policy("vms", []string{"*", "!exp1/*"}, "get")}},
		"a missing policy":        {policies: []*v1.PolicySpec{nil}, wantErr: true},
		"no resources":            {policies: []*v1.PolicySpec{{Verbs: []string{"get"}}}, wantErr: true},
		"an empty resource":       {policies: []*v1.PolicySpec{policy("", nil, "get")}, wantErr: true},
		"a bad resource pattern":  {policies: []*v1.PolicySpec{policy("vms[", nil, "get")}, wantErr: true},
		"an unknown resource":     {policies: []*v1.PolicySpec{policy("rockets", nil, "get")}, wantErr: true},
		"an unknown sub-resource": {policies: []*v1.PolicySpec{policy("vms/fly", nil, "get")}, wantErr: true},
		"no verbs":                {policies: []*v1.PolicySpec{policy("vms", nil)}, wantErr: true},
		"an unknown verb":         {policies: []*v1.PolicySpec{policy("vms", nil, "launch")}, wantErr: true},
		"an empty name":           {policies: []*v1.PolicySpec{policy("vms", []string{""}, "get")}, wantErr: true},
		"a bare negation":         {policies: []*v1.PolicySpec{policy("vms", []string{"!"}, "get")}, wantErr: true},
		"a bad name pattern":      {policies: []*v1.PolicySpec{policy("vms", []string{"exp[1"}, "get")}, wantErr: true},
		"a bad second policy": {
			policies: []*v1.PolicySpec{policy("vms", nil, "get"), policy("vms", nil, "launch")}, wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidatePolicies(tc.policies)
			if tc.wantErr != errors.Is(err, ErrPolicyInvalid) || tc.wantErr != (err != nil) {
				t.Errorf("ValidatePolicies(%s) = %v, want an error wrapping ErrPolicyInvalid: %t", name, err, tc.wantErr)
			}
		})
	}
}

// builtinRoleSpecs decodes the built-in role configs.
func builtinRoleSpecs(t *testing.T) map[string]*v1.RoleSpec {
	t.Helper()

	configs, err := config.BuiltinRoleConfigs()
	if err != nil {
		t.Fatalf("BuiltinRoleConfigs() error = %v", err)
	}

	specs := make(map[string]*v1.RoleSpec, len(configs))

	for _, c := range configs {
		var spec v1.RoleSpec
		if err := mapstructure.Decode(c.Spec, &spec); err != nil {
			t.Fatalf("decoding built-in role %s: %v", c.Metadata.Name, err)
		}

		specs[c.Metadata.Name] = &spec
	}

	return specs
}

// Every policy of a built-in role passes ValidatePolicies, so the known
// resources and verbs cover what phenix ships.
func TestBuiltinRolePoliciesAreValid(t *testing.T) {
	t.Parallel()

	for name, spec := range builtinRoleSpecs(t) {
		if err := ValidatePolicies(spec.Policies); err != nil {
			t.Errorf("ValidatePolicies(built-in role %s) = %v, want nil", name, err)
		}
	}
}

// Ungranted lists what the policies grant beyond the role, reading granted
// resources and names as patterns, and an unnamed policy as one for "*".
func TestUngranted(t *testing.T) {
	t.Parallel()

	scoped := &v1.RoleSpec{Policies: []*v1.PolicySpec{
		policy("vms", []string{"exp1/*", "exp1_?"}, "list", "get"),
		policy("experiments", []string{"*", "!secret"}, "get"),
		policy("disks", []string{"[a*]"}, "list"),
		{Resources: []string{"*/*"}, ResourceNames: []string{"*"}, Verbs: []string{"*"}},
	}}

	tests := map[string]struct {
		role     *v1.RoleSpec
		policies []*v1.PolicySpec
		want     []string
	}{
		"the same pattern":       {role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "list")}},
		"a name the pattern has": {role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1/vm1"}, "get")}},
		"a pattern of one name":  {role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1_a"}, "get")}},
		"a wider name pattern": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1_*"}, "get")},
			want: []string{"get on vms for exp1_*"},
		},
		"an unnamed policy": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms", nil, "list")},
			want: []string{"list on vms for *"},
		},
		"another verb": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "delete")},
			want: []string{"delete on vms", "delete on vms for exp1/*"},
		},
		"every verb": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "*")},
			want: []string{"* on vms", "* on vms for exp1/*"},
		},
		"a name the role negates": {
			role: scoped, policies: []*v1.PolicySpec{policy("experiments", []string{"public", "secret"}, "get")},
			want: []string{"get on experiments for secret"},
		},
		"a pattern beside a negation": {
			role: scoped, policies: []*v1.PolicySpec{policy("experiments", []string{"*"}, "get")},
			want: []string{"get on experiments for *"},
		},
		"negated names need nothing": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*", "!exp2/*"}, "list")},
		},
		"a class that matches a star": {
			role: scoped, policies: []*v1.PolicySpec{policy("disks", []string{"*"}, "list")},
			want: []string{"list on disks for *"},
		},
		"a sub-resource of a two-segment pattern": {
			role: scoped, policies: []*v1.PolicySpec{policy("vms/*", []string{"x"}, "update")},
		},
		"a one-segment pattern": {
			role: scoped, policies: []*v1.PolicySpec{policy("*", []string{"x"}, "update")},
			want: []string{"update on *", "update on * for x"},
		},
		"no role": {
			role: nil, policies: []*v1.PolicySpec{policy("vms", []string{"a"}, "get")},
			want: []string{"get on vms", "get on vms for a"},
		},
		"an admin": {
			role: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
				Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"},
			}}},
			policies: []*v1.PolicySpec{
				{Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"}},
				policy("vms", nil, "list"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			role := Role{Spec: tc.role}

			if diff := cmp.Diff(tc.want, role.Ungranted(tc.policies)); diff != "" {
				t.Errorf("Ungranted() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// coverTokens are the parts of the patterns of TestCoversIsSound.
var coverTokens = []string{"a", "b", "/", "*", "?", "[ab]", `\*`, "[^a]", `\/`, "[.-0]"} //nolint:gochecknoglobals // test data

// coverName returns a name that the pattern of tokens may match: each token
// gives a part that it matches, or a part near one.
func coverName(t *rapid.T, tokens []string) string {
	var b strings.Builder

	for i, token := range tokens {
		switch token {
		case "*":
			b.WriteString(rapid.StringOfN(rapid.SampledFrom([]rune("ab*?/")), 0, 3, -1).Draw(t, "star"+strconv.Itoa(i)))
		case "?", "[^a]", "[.-0]":
			b.WriteString(rapid.SampledFrom([]string{"a", "b", "*", "/", "."}).Draw(t, "one"+strconv.Itoa(i)))
		case `\/`:
			b.WriteString("/")
		case "[ab]":
			b.WriteString(rapid.SampledFrom([]string{"a", "b"}).Draw(t, "class"+strconv.Itoa(i)))
		case `\*`:
			b.WriteString("*")
		default:
			b.WriteString(token)
		}
	}

	return b.String()
}

// covers is never true for patterns p and q when a name that q matches does
// not match p.
func TestCoversIsSound(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		pTokens := rapid.SliceOfN(rapid.SampledFrom(coverTokens), 0, 3).Draw(t, "p")
		qTokens := rapid.SliceOfN(rapid.SampledFrom(coverTokens), 0, 4).Draw(t, "q")
		p, q := strings.Join(pTokens, ""), strings.Join(qTokens, "")

		name := coverName(t, qTokens)

		inQ, _ := filepath.Match(q, name)
		inP, _ := filepath.Match(p, name)

		if covers(p, q) && inQ && !inP {
			t.Fatalf("covers(%q, %q) = true, but %q matches only %q", p, q, name, q)
		}
	})
}

// covers holds for equal patterns, a "*" segment over a segment that cannot
// match "/" (classes and escapes included), and a pattern that matches a name.
func TestCovers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		p, q string
		want bool
	}{
		{"*", "exp1", true},
		{"exp1_?", "exp1_a", true},
		{"*", "exp1_*", true},
		{"*/*", "exp1/*", true},
		{"*", "exp1/*", false},
		{"*/*", "*", false},
		{"exp1_?", "exp1_*", false},
		{"[a*]", "*", false},
		{"*", "[ab]", true},
		{"*", "[^a]", false},
		{"*", "[.-0]", false},
		{`*`, `\*`, true},
		{"a*", `a\*`, true},
		{"b*", `a\*`, false},
		{"a[b]", "a[b]", true},
		{"*/b", "*/*", false},
		{"*", "vm[12]", true},
		{"*/*", "exp1/vm[12]", true},
		{"exp1/*", "exp1/vm[12]", true},
		{"exp2/*", "exp1/vm[12]", false},
		{"*", `a\/b`, false},
		{"*/*", `a\/b`, true},
		{"vm?", "vm[12]", false},
		{"*", "vm[", false},
	} {
		if got := covers(tc.p, tc.q); got != tc.want {
			t.Errorf("covers(%q, %q) = %t, want %t", tc.p, tc.q, got, tc.want)
		}
	}
}

// own is the users policy that assignment adds for the user.
func own(username string, verbs ...string) *v1.PolicySpec {
	return policy("users", []string{username}, verbs...)
}

// UserRoleCopy gives the updated role's unnamed policies the names the user
// was assigned, keeps named policies and the user's own users policy.
func TestUserRoleCopy(t *testing.T) {
	t.Parallel()

	old := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
		policy("vms", []string{"shared/*"}, "list"),
		policy("experiments", nil, "get"),
		policy("vms", nil, "list"),
	}}

	updated := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
		policy("experiments", nil, "get", "list"),
		policy("disks", []string{"*"}, "list"),
	}}

	tests := map[string]struct {
		old, current *v1.RoleSpec
		want         *v1.RoleSpec
	}{
		"a copy of the role": {
			old: old,
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("vms", []string{"shared/*"}, "list"),
				policy("experiments", []string{"exp1", "exp2"}, "get"),
				policy("vms", []string{"exp1", "exp2"}, "list"),
				own("alice", "get", "patch"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"exp1", "exp2"}, "get", "list"),
				policy("disks", []string{"*"}, "list"),
				own("alice", "get", "patch"),
			}},
		},
		"a stale copy in another order": {
			old: old,
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				own("alice", "get"),
				policy("vms", []string{"shared/*"}, "list"),
				policy("vms", []string{"exp3"}, "list"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", nil, "get", "list"),
				policy("disks", []string{"*"}, "list"),
				own("alice", "get"),
			}},
		},
		"a stale copy with a narrowest set": {
			old: old,
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("vms", []string{"exp1/*", "exp2/*"}, "list"),
				policy("disks", []string{"exp1/vm1"}, "get"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"exp1/vm1"}, "get", "list"),
				policy("disks", []string{"*"}, "list"),
			}},
		},
		"a copy whose verbs changed": {
			old: old,
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"exp4"}, "get", "update"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"exp4"}, "get", "list"),
				policy("disks", []string{"*"}, "list"),
			}},
		},
		"an old role with only named policies": {
			old: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{policy("vms", []string{"shared/*"}, "list")}},
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("vms", []string{"shared/*"}, "list"), own("alice", "get"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", nil, "get", "list"),
				policy("disks", []string{"*"}, "list"),
				own("alice", "get"),
			}},
		},
		"another user's users policy": {
			old: old,
			current: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"*"}, "get"), own("bob", "get"),
			}},
			want: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
				policy("experiments", []string{"*"}, "get", "list"),
				policy("disks", []string{"*"}, "list"),
			}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := UserRoleCopy(updated, tc.old, tc.current, "alice")
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("UserRoleCopy() mismatch (-want +got):\n%s", diff)
			}

			got.Policies[0].ResourceNames = append(got.Policies[0].ResourceNames, "changed")
			if slices.Contains(tc.current.Policies[0].ResourceNames, "changed") ||
				slices.Contains(updated.Policies[1].ResourceNames, "changed") {
				t.Error("UserRoleCopy() shares a list with its input")
			}
		})
	}
}

// genChangePolicy draws a policy of the role_change tests, never on users.
func genChangePolicy() *rapid.Generator[*v1.PolicySpec] {
	return rapid.Custom(func(t *rapid.T) *v1.PolicySpec {
		resources := rapid.SliceOfN(rapid.SampledFrom([]string{"vms", "experiments", "disks", "vms/*"}), 1, 2).Draw(t, "resources")
		verbs := rapid.SliceOfN(rapid.SampledFrom([]string{"get", "list", "update"}), 1, 2).Draw(t, "verbs")

		var names []string
		if rapid.Bool().Draw(t, "named") {
			names = rapid.SliceOfN(rapid.SampledFrom([]string{"*", "a", "b/*", "!c"}), 1, 2).Draw(t, "names")
		}

		return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
	})
}

// A role update that changes nothing gives each user the copy it holds: the
// copy that assignment (SetResourceNames, then the own users policy) made.
func TestUserRoleCopyKeepsAnAssignment(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		policies := rapid.SliceOfN(genChangePolicy(), 0, 4).Draw(t, "policies")
		names := rapid.SliceOfN(rapid.SampledFrom([]string{"exp1", "exp2/*", "*"}), 0, 2).Draw(t, "user names")
		spec := &v1.RoleSpec{Name: "Lab", Policies: policies}

		assigned := newRole(cloneFuzzSpec(spec), nil)
		if err := assigned.SetResourceNames(names...); err != nil {
			t.Fatalf("SetResourceNames(%q) error = %v", names, err)
		}

		assigned.AddPolicy([]string{"users"}, []string{"alice"}, []string{"get", "patch"})

		got := UserRoleCopy(spec, spec, assigned.Spec, "alice")
		if diff := cmp.Diff(assigned.Spec, got); diff != "" {
			t.Fatalf("UserRoleCopy(role, role, assigned copy) mismatch (-want +got):\n%s", diff)
		}
	})
}

// CreateRole stores a role that passes the Role schema, refuses a taken name,
// and SetPolicies and Delete refuse a role changed since it was read.
//
//nolint:paralleltest // replaces the store
func TestRoleStoreChanges(t *testing.T) {
	storetest.UseBolt(t)

	created, err := CreateRole("lab", &v1.RoleSpec{Name: "Lab"})
	if err != nil {
		t.Fatalf("CreateRole(lab) error = %v", err)
	}

	if got := created.ConfigName(); got != "lab" {
		t.Errorf("CreateRole(lab).ConfigName() = %q, want lab", got)
	}

	if _, err := CreateRole("lab", &v1.RoleSpec{Name: "Lab 2"}); !errors.Is(err, store.ErrExist) {
		t.Errorf("CreateRole(lab) again error = %v, want %v", err, store.ErrExist)
	}

	first, _ := RoleFromConfig("lab")
	second, _ := RoleFromConfig("Lab")

	policies := []*v1.PolicySpec{policy("vms", nil, "list")}

	if err := first.SetPolicies(policies); err != nil {
		t.Fatalf("SetPolicies() error = %v", err)
	}

	if !first.Allowed("vms", "list") {
		t.Error("SetPolicies() did not index the new policies")
	}

	stored, _ := RoleFromConfig("lab")
	if diff := cmp.Diff(policies, stored.Spec.Policies); diff != "" {
		t.Errorf("stored policies mismatch (-want +got):\n%s", diff)
	}

	if err := second.SetPolicies(nil); !errors.Is(err, store.ErrConflict) {
		t.Errorf("SetPolicies() of a stale role error = %v, want %v", err, store.ErrConflict)
	}

	if err := second.Delete(); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Delete() of a stale role error = %v, want %v", err, store.ErrConflict)
	}

	if err := first.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := RoleFromConfig("lab"); !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("RoleFromConfig(lab) after Delete() error = %v, want %v", err, ErrRoleNotFound)
	}

	if got := (Role{}).ConfigName(); got != "" {
		t.Errorf("Role{}.ConfigName() = %q, want empty", got)
	}
}

// ReplaceRole changes only the users whose copy has the role's name, and
// UsersWithRole finds those users.
//
//nolint:paralleltest // replaces the store
func TestReplaceRole(t *testing.T) {
	old := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{policy("vms", nil, "list")}}
	updated := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{policy("experiments", nil, "get")}}

	useStore(t, nil, []*v1.UserSpec{
		{Username: "alice", Role: &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{policy("vms", []string{"exp1"}, "list")}}},
		{Username: "bob", Role: &v1.RoleSpec{Name: "Other"}},
		{Username: "carol"},
	})

	users, err := UsersWithRole("Lab")
	if err != nil {
		t.Fatalf("UsersWithRole(Lab) error = %v", err)
	}

	if len(users) != 1 || users[0].Username() != "alice" {
		t.Fatalf("UsersWithRole(Lab) = %v, want only alice", users)
	}

	bob, _ := GetUser("bob")
	revision := bob.config.Metadata.Revision

	for _, u := range []*User{users[0], bob} {
		replaced, err := u.ReplaceRole(old, updated)
		if err != nil || replaced != (u.Username() == "alice") {
			t.Errorf("%s.ReplaceRole() = %t, %v, want %t, nil", u.Username(), replaced, err, u.Username() == "alice")
		}
	}

	alice, _ := GetUser("alice")
	want := []*v1.PolicySpec{policy("experiments", []string{"exp1"}, "get")}

	if diff := cmp.Diff(want, alice.Spec.Role.Policies); diff != "" {
		t.Errorf("alice's stored policies mismatch (-want +got):\n%s", diff)
	}

	if bob, _ = GetUser("bob"); bob.config.Metadata.Revision != revision {
		t.Errorf("bob's revision = %d, want %d: ReplaceRole wrote a user without the role", bob.config.Metadata.Revision, revision)
	}

	storetest.Use(t, userFailingStore{Store: storetest.NewBolt(t), list: map[string]bool{"User": true}})

	if _, err := UsersWithRole("Lab"); !errors.Is(err, errUserStore) {
		t.Errorf("UsersWithRole() of a failing store error = %v, want %v", err, errUserStore)
	}

	if _, err := alice.ReplaceRole(updated, updated); err == nil {
		t.Error("ReplaceRole() of a user not in the store error = nil, want one")
	}
}

// A global admin holds every name and resource pattern a role may grant,
// those with classes and escapes included.
func TestUngrantedOfAGlobalAdmin(t *testing.T) {
	t.Parallel()

	admin := Role{Spec: &v1.RoleSpec{Name: "Global Admin", Policies: []*v1.PolicySpec{{
		Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"},
	}}}}

	for _, p := range []*v1.PolicySpec{
		policy("vms", []string{"vm[12]"}, "get"),
		policy("vms", []string{`a\*`}, "get"),
		policy("vms", []string{"exp1/vm[12]"}, "get"),
		policy("vms", []string{"lab-*"}, "get"),
		policy("vm[s]", nil, "get"),
		policy("vms/*", []string{`exp1\/vm1`}, "*"),
	} {
		if got := admin.Ungranted([]*v1.PolicySpec{p}); len(got) != 0 {
			t.Errorf("a global admin's Ungranted(%v %v) = %v, want none", p.Resources, p.ResourceNames, got)
		}
	}

	if got := admin.Ungranted([]*v1.PolicySpec{policy("vms", []string{"exp/*/x"}, "get")}); len(got) == 0 {
		t.Error(`a global admin's Ungranted(vms "exp/*/x") = none, want "*/*" not to cover three segments`)
	}
}

// Ungranted excuses what a held role, such as the target user's current
// role, already grants.
func TestUngrantedOfAHeldRole(t *testing.T) {
	t.Parallel()

	requester := Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{policy("users/roles", []string{"*"}, "patch")}}}
	current := Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "list")}}}
	assigned := []*v1.PolicySpec{policy("vms", []string{"exp1/*"}, "list")}

	if got := requester.Ungranted(assigned, current); len(got) != 0 {
		t.Errorf("Ungranted(the user's current policy, current role) = %v, want none", got)
	}

	want := []string{"list on vms", "list on vms for exp1/*"}
	if diff := cmp.Diff(want, requester.Ungranted(assigned)); diff != "" {
		t.Errorf("Ungranted(policy) without the held role mismatch (-want +got):\n%s", diff)
	}
}

// A retry after a user's copy failed to update reads the stored new role as
// old: the stale copy then keeps its names, and no name gets wider.
func TestUserRoleCopyRetryDoesNotWiden(t *testing.T) {
	t.Parallel()

	r1 := &v1.RoleSpec{Name: "R", Policies: []*v1.PolicySpec{policy("vms", []string{"*"}, "list"), policy("experiments", nil, "get")}}
	bob := &v1.RoleSpec{Name: "R", Policies: []*v1.PolicySpec{
		policy("vms", []string{"*"}, "list"), policy("experiments", []string{"exp1"}, "get"), own("bob", "get", "patch"),
	}}
	r2 := &v1.RoleSpec{Name: "R", Policies: []*v1.PolicySpec{policy("vms", nil, "list"), policy("experiments", nil, "get")}}

	first := UserRoleCopy(r2, r1, bob, "bob")
	wantFirst := &v1.RoleSpec{Name: "R", Policies: []*v1.PolicySpec{
		policy("vms", []string{"exp1"}, "list"), policy("experiments", []string{"exp1"}, "get"), own("bob", "get", "patch"),
	}}

	if diff := cmp.Diff(wantFirst, first); diff != "" {
		t.Errorf("UserRoleCopy(r2, r1, bob) mismatch (-want +got):\n%s", diff)
	}

	retry := UserRoleCopy(r2, r2, bob, "bob")
	wantRetry := &v1.RoleSpec{Name: "R", Policies: []*v1.PolicySpec{
		policy("vms", []string{"*"}, "list"), policy("experiments", []string{"exp1"}, "get"), own("bob", "get", "patch"),
	}}

	if diff := cmp.Diff(wantRetry, retry); diff != "" {
		t.Errorf("UserRoleCopy(r2, r2, stale bob) mismatch (-want +got):\n%s", diff)
	}
}

// For any copy, UserRoleCopy gives an unnamed policy of the update only a set
// of names that a policy of the copy already allows.
func TestUserRoleCopyNeverWidens(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		named := rapid.Custom(func(t *rapid.T) *v1.PolicySpec {
			p := genChangePolicy().Draw(t, "policy")
			if len(p.ResourceNames) == 0 {
				p.ResourceNames = []string{"exp1"}
			}

			return p
		})

		current := &v1.RoleSpec{Name: "Lab", Policies: rapid.SliceOfN(named, 0, 4).Draw(t, "current")}
		old := &v1.RoleSpec{Name: "Lab", Policies: rapid.SliceOfN(genChangePolicy(), 0, 4).Draw(t, "old")}
		updated := &v1.RoleSpec{Name: "Lab", Policies: rapid.SliceOfN(genChangePolicy(), 0, 4).Draw(t, "updated")}

		got := UserRoleCopy(updated, old, current, "alice")

		for i, p := range updated.Policies {
			names := got.Policies[i].ResourceNames
			if len(p.ResourceNames) > 0 || len(names) == 0 {
				continue
			}

			allowed := func(c *v1.PolicySpec) bool { return namesCover(c.ResourceNames, names) }
			if !slices.ContainsFunc(current.Policies, allowed) {
				t.Fatalf("UserRoleCopy() gives %v the names %q, which no policy of the copy allows", p.Resources, names)
			}
		}
	})
}
