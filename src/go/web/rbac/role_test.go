// Mirrors src/js/test/rbac.test.js so client and server RBAC matching stay aligned.
package rbac

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// newTestRole returns the role shared with src/js/test/rbac.test.js.
func newTestRole(t *testing.T) Role {
	t.Helper()

	return Role{
		Spec: &v1.RoleSpec{
			Name: "Test Role",
			Policies: []*v1.PolicySpec{
				{Resources: []string{"experiments"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"get"}},
				{Resources: []string{"experiments/start"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"update"}},
				{Resources: []string{"experiments"}, ResourceNames: []string{"exp1"}, Verbs: []string{"delete"}},
				{Resources: []string{"*"}, ResourceNames: []string{"vm1"}, Verbs: []string{"patch"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"*"}, Verbs: []string{"delete"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"expA/*"}, Verbs: []string{"update"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"*/vm1"}, Verbs: []string{"create"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"*/*"}, Verbs: []string{"get"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"**"}, Verbs: []string{"list"}},
				{Resources: []string{"vms"}, ResourceNames: []string{"{expA,expB}/*"}, Verbs: []string{"post"}},
				{Resources: []string{"vms/start"}, ResourceNames: []string{"expA/*", "!expA/secret"}, Verbs: []string{"update"}},
				{Resources: []string{"**"}, ResourceNames: []string{"*"}, Verbs: []string{"watch"}},
				{Resources: []string{"things"}, ResourceNames: []string{"*", "!thing1"}, Verbs: []string{"*"}},
				{Resources: []string{"items"}, ResourceNames: []string{"item*"}, Verbs: []string{"*"}},
				{Resources: []string{"notes"}, ResourceNames: []string{"note!"}, Verbs: []string{"get"}},
			},
		},
	}
}

// TestRoleAllowed checks matching for indexed and unindexed (literal) roles;
// Allowed must not index a literal, since roles are shared.
func TestRoleAllowed(t *testing.T) {
	literal := newTestRole(t)
	roles := map[string]*Role{"indexed": newRole(literal.Spec, nil), "unindexed": &literal}

	tests := []struct {
		name     string
		resource string
		verb     string
		names    []string
		want     bool
	}{
		// get any experiment
		{"get expA", "experiments", "get", []string{"expA"}, true},
		{"get expB", "experiments", "get", []string{"expB"}, true},
		// update only experiments/start
		{"update experiments/start", "experiments/start", "update", nil, true},
		{"update experiments", "experiments", "update", nil, false},
		{"update experiments/stop", "experiments/stop", "update", nil, false},
		{"update experiments/start expA", "experiments/start", "update", []string{"expA"}, true},
		// only delete exp1
		{"delete exp1", "experiments", "delete", []string{"exp1"}, true},
		{"delete expB", "experiments", "delete", []string{"expB"}, false},
		{"delete experiments/stop exp1", "experiments/stop", "delete", []string{"exp1"}, false},
		// resource single wildcard doesn't apply
		{"patch vms vm1", "vms", "patch", []string{"vm1"}, true},
		{"patch vms/start vm1", "vms/start", "patch", []string{"vm1"}, false},
		// resource name restriction
		{"patch vms vmB", "vms", "patch", []string{"vmB"}, false},
		{"patch experiments expA", "experiments", "patch", []string{"expA"}, false},
		// resourceName single wildcard does not cross namespace boundaries
		{"delete vm1", "vms", "delete", []string{"vm1"}, true},
		{"delete expA/vm1", "vms", "delete", []string{"expA/vm1"}, false},
		// resourceName can allow one namespace
		{"update expA/vm1", "vms", "update", []string{"expA/vm1"}, true},
		{"update expB/vm1", "vms", "update", []string{"expB/vm1"}, false},
		// resourceName can allow one name across namespaces
		{"create expA/vm1", "vms", "create", []string{"expA/vm1"}, true},
		{"create expB/vm1", "vms", "create", []string{"expB/vm1"}, true},
		{"create expA/vm2", "vms", "create", []string{"expA/vm2"}, false},
		// resourceName namespace wildcard requires a namespace
		{"get expA/vm1", "vms", "get", []string{"expA/vm1"}, true},
		{"get vm1", "vms", "get", []string{"vm1"}, false},
		// resourceName globstar does not cross namespace boundaries
		{"list vm1", "vms", "list", []string{"vm1"}, true},
		{"list expA/vm1", "vms", "list", []string{"expA/vm1"}, false},
		// resourceName braces are not expanded
		{"post expA/vm1", "vms", "post", []string{"expA/vm1"}, false},
		{"post literal braces", "vms", "post", []string{"{expA,expB}/vm1"}, true},
		// resourceName negation within a namespace
		{"start expA/vm1", "vms/start", "update", []string{"expA/vm1"}, true},
		{"start expA/secret", "vms/start", "update", []string{"expA/secret"}, false},
		{"start expB/vm1", "vms/start", "update", []string{"expB/vm1"}, false},
		// any allowed resourceName grants access
		{"update one allowed name", "vms", "update", []string{"expB/vm1", "expA/vm1"}, true},
		{"update no allowed names", "vms", "update", []string{"expB/vm1", "expC/vm1"}, false},
		// resource globstar does not cross subresource boundaries
		{"watch vms", "vms", "watch", []string{"vm1"}, true},
		{"watch vms/start", "vms/start", "watch", []string{"vm1"}, false},
		// resourceName wildcard matches leading dots
		{"get .thing", "things", "get", []string{".thing"}, true},
		// resourceName negation
		{"delete thing", "things", "delete", []string{"thing"}, true},
		{"delete thing1", "things", "delete", []string{"thing1"}, false},
		{"delete thing2", "things", "delete", []string{"thing2"}, true},
		// resourceName mid-wildcard
		{"delete item", "items", "delete", []string{"item"}, true},
		{"delete item1", "items", "delete", []string{"item1"}, true},
		{"delete non-item", "items", "delete", []string{"thing"}, false},
		// only a leading "!" negates
		{"get note!", "notes", "get", []string{"note!"}, true},
		{"get note", "notes", "get", []string{"note"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for kind, role := range roles {
				if got := role.Allowed(tt.resource, tt.verb, tt.names...); got != tt.want {
					t.Errorf("Allowed(%q, %q, %q) of the %s role = %v, want %v", tt.resource, tt.verb, tt.names, kind, got, tt.want)
				}
			}
		})
	}

	if literal.mappedPolicies != nil {
		t.Error("Allowed() stored a policy index on an unindexed role, want none")
	}
}

// Scoping a default role to a user's experiments scopes every unnamed policy,
// also those after a named one.
//
//nolint:paralleltest // replaces the store
func TestSetResourceNamesScopesEveryUnnamedPolicy(t *testing.T) {
	storetest.Use(t)

	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}

	for _, tc := range []struct {
		role    string
		allowed [][3]string // resource, verb, name
	}{
		{"Experiment Admin", [][3]string{
			{"experiments", "get", "exp-a"},
			{"experiments/files", "delete", "exp-a"},
			{"jobs", "list", "exp-a"},
			{"jobs", "update", "exp-a"},
		}},
		{"Experiment Viewer", [][3]string{
			{"experiments", "get", "exp-a"},
			{"vms/mount", "post", "exp-a/vm"},
		}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			role, err := RoleFromConfig(tc.role)
			if err != nil {
				t.Fatalf("RoleFromConfig(%s) error = %v", tc.role, err)
			}

			if err := role.SetResourceNames("exp-a", "exp-a/*"); err != nil {
				t.Fatalf("SetResourceNames(exp-a, exp-a/*) error = %v", err)
			}

			for _, a := range tc.allowed {
				if !role.Allowed(a[0], a[1], a[2]) {
					t.Errorf("Allowed(%s, %s, %s) after SetResourceNames(exp-a, exp-a/*) = false, want true", a[0], a[1], a[2])
				}

				other := strings.Replace(a[2], "exp-a", "exp-b", 1)
				if role.Allowed(a[0], a[1], other) {
					t.Errorf("Allowed(%s, %s, %s) after SetResourceNames(exp-a, exp-a/*) = true, want false", a[0], a[1], other)
				}
			}

			// a policy that names its resources keeps them
			if !role.Allowed("hosts", "list", "any-host") {
				t.Error("Allowed(hosts, list, any-host) after SetResourceNames(exp-a, exp-a/*) = false, want true")
			}
		})
	}

	if err := (&Role{Spec: &v1.RoleSpec{}}).SetResourceNames("["); !errors.Is(err, ErrResourceNameInvalid) {
		t.Errorf("SetResourceNames([) error = %v, want %v", err, ErrResourceNameInvalid)
	}
}

// SetResourceNames scopes a policy with an empty resourceNames list, as it
// scopes one without the field, and keeps the names of a named policy.
func TestSetResourceNamesScopesAnEmptyList(t *testing.T) {
	t.Parallel()

	r := newRole(&v1.RoleSpec{Name: "r", Policies: []*v1.PolicySpec{
		policy("experiments", nil, "get"),
		policy("vms/mount", []string{}, "post"),
		policy("hosts", []string{"*"}, "list"),
	}}, nil)

	if err := r.SetResourceNames("exp-a"); err != nil {
		t.Fatalf("SetResourceNames(exp-a) error = %v", err)
	}

	want := []*v1.PolicySpec{
		policy("experiments", []string{"exp-a"}, "get"),
		policy("vms/mount", []string{"exp-a"}, "post"),
		policy("hosts", []string{"*"}, "list"),
	}
	if diff := cmp.Diff(want, r.Spec.Policies); diff != "" {
		t.Errorf("SetResourceNames(exp-a) policies mismatch (-want +got):\n%s", diff)
	}

	if !r.Allowed("vms/mount", "post", "exp-a") {
		t.Error("Allowed(vms/mount, post, exp-a) after SetResourceNames(exp-a) = false, want true")
	}
}

// A name pattern that is bad only past where a probe name stops matching, such
// as "x*[", is refused, and the role stays as it was.
func TestResourceNamesRefuseAPartlyBadPattern(t *testing.T) {
	t.Parallel()

	spec := func() *v1.RoleSpec {
		return &v1.RoleSpec{Name: "r", Policies: []*v1.PolicySpec{
			{Resources: []string{"vms"}, ResourceNames: []string{"vm1"}, Verbs: []string{"get"}},
			{Resources: []string{"experiments"}, Verbs: []string{"get"}},
		}}
	}

	for name, call := range map[string]func(*Role) error{
		"SetResourceNames": func(r *Role) error { return r.SetResourceNames("x*[") },
		"AddResourceName":  func(r *Role) error { return r.AddResourceName("x*[") },
	} {
		r := newRole(spec(), nil)

		if err := call(r); !errors.Is(err, ErrResourceNameInvalid) {
			t.Errorf("%s(x*[) error = %v, want %v", name, err, ErrResourceNameInvalid)
		}

		if diff := cmp.Diff(spec(), r.Spec); diff != "" {
			t.Errorf("%s(x*[) changed the spec (-want +got):\n%s", name, diff)
		}
	}
}

// AddResourceName adds a name once to each named policy and never to a
// nameless one, so repeated calls do not grow a role.
func TestAddResourceName(t *testing.T) {
	t.Parallel()

	named := func(names ...string) *v1.PolicySpec { return policy("vms", names, "get") }
	empty := func() *v1.PolicySpec { return policy("hosts", []string{}, "list") }

	tests := map[string]struct {
		policies []*v1.PolicySpec
		name     string
		wantErrs []error // one per call
		want     []*v1.PolicySpec
	}{
		"policy with many names gets the name once": {
			policies: []*v1.PolicySpec{named("a", "b", "c")},
			name:     "d",
			wantErrs: []error{nil},
			want:     []*v1.PolicySpec{named("a", "b", "c", "d")},
		},
		"policy that has the name is unchanged": {
			policies: []*v1.PolicySpec{named("a", "d", "c")},
			name:     "d",
			wantErrs: []error{ErrResourceNameExists},
			want:     []*v1.PolicySpec{named("a", "d", "c")},
		},
		"policy that lacks the name gets it when another has it": {
			policies: []*v1.PolicySpec{named("d"), named("a", "b")},
			name:     "d",
			wantErrs: []error{nil},
			want:     []*v1.PolicySpec{named("d"), named("a", "b", "d")},
		},
		"every named policy has the name": {
			policies: []*v1.PolicySpec{named("a", "d"), named("d"), named()},
			name:     "d",
			wantErrs: []error{ErrResourceNameExists},
			want:     []*v1.PolicySpec{named("a", "d"), named("d"), named()},
		},
		"policies with no names stay unchanged": {
			policies: []*v1.PolicySpec{named(), named("a"), empty()},
			name:     "d",
			wantErrs: []error{nil},
			want:     []*v1.PolicySpec{named(), named("a", "d"), empty()},
		},
		"role of only nameless policies is unchanged": {
			policies: []*v1.PolicySpec{named()},
			name:     "d",
			wantErrs: []error{nil},
			want:     []*v1.PolicySpec{named()},
		},
		"bad pattern in a role of only nameless policies": {
			policies: []*v1.PolicySpec{named()},
			name:     "x*[",
			wantErrs: []error{ErrResourceNameInvalid},
			want:     []*v1.PolicySpec{named()},
		},
		"global-admin shape never grows": {
			policies: []*v1.PolicySpec{named("*", "*/*"), named("*", "*/*")},
			name:     "*/*",
			wantErrs: []error{ErrResourceNameExists, ErrResourceNameExists, ErrResourceNameExists},
			want:     []*v1.PolicySpec{named("*", "*/*"), named("*", "*/*")},
		},
		"name added once, then exists": {
			policies: []*v1.PolicySpec{named("*")},
			name:     "*/*",
			wantErrs: []error{nil, ErrResourceNameExists, ErrResourceNameExists},
			want:     []*v1.PolicySpec{named("*", "*/*")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRole(&v1.RoleSpec{Name: "r", Policies: tc.policies}, nil)

			for i, wantErr := range tc.wantErrs {
				if err := r.AddResourceName(tc.name); !errors.Is(err, wantErr) {
					t.Errorf("call %d: AddResourceName(%s) error = %v, want %v", i+1, tc.name, err, wantErr)
				}
			}

			if diff := cmp.Diff(tc.want, r.Spec.Policies); diff != "" {
				t.Errorf("AddResourceName(%s) policies mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// For any role, AddResourceName gives each named policy the name once, leaves
// nameless policies alone, and changes nothing on a second call.
func TestAddResourceNameIsIdempotent(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		spec := &v1.RoleSpec{Name: "r", Policies: rapid.SliceOfN(genRolePolicy(), 0, 4).Draw(t, "policies")}
		name := rapid.SampledFrom([]string{"*", "*/*", "a", "c", "!a"}).Draw(t, "name")
		before := cloneFuzzSpec(spec)
		r := newRole(spec, nil)

		var changed, named bool

		for _, p := range before.Policies {
			named = named || len(p.ResourceNames) > 0
			changed = changed || len(p.ResourceNames) > 0 && !slices.Contains(p.ResourceNames, name)
		}

		var wantErr error
		if named && !changed {
			wantErr = ErrResourceNameExists
		}

		if err := r.AddResourceName(name); !errors.Is(err, wantErr) {
			t.Fatalf("AddResourceName(%s) of %v error = %v, want %v", name, rolePolicies(before), err, wantErr)
		}

		for i, p := range r.Spec.Policies {
			want := cloneFuzzSpec(before).Policies[i]
			if len(want.ResourceNames) > 0 && !slices.Contains(want.ResourceNames, name) {
				want.ResourceNames = append(want.ResourceNames, name)
			}

			if diff := cmp.Diff(want, p); diff != "" {
				t.Fatalf("AddResourceName(%s) of %v policy %d mismatch (-want +got):\n%s", name, rolePolicies(before), i, diff)
			}
		}

		if named {
			wantErr = ErrResourceNameExists
		}

		after := cloneFuzzSpec(r.Spec)
		if err := r.AddResourceName(name); !errors.Is(err, wantErr) {
			t.Fatalf("second AddResourceName(%s) error = %v, want %v", name, err, wantErr)
		}

		if diff := cmp.Diff(after, r.Spec); diff != "" {
			t.Fatalf("second AddResourceName(%s) changed the spec (-want +got):\n%s", name, diff)
		}
	})
}

// A saved policy that names no resources leaves resourceNames out rather than
// storing null, which the role schema rejects on the next edit.
//
//nolint:paralleltest // replaces the store
func TestSaveOmitsNullResourceNames(t *testing.T) {
	useStore(t, []*v1.RoleSpec{{
		Name:     "Unscoped",
		Policies: []*v1.PolicySpec{policy("vms/forwards", nil, "create")},
	}}, nil)

	role, err := RoleFromConfig("Unscoped")
	if err != nil {
		t.Fatalf("RoleFromConfig(Unscoped) error = %v", err)
	}

	if err := role.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	c, _ := store.NewConfig("role/unscoped")
	if err := store.Get(c); err != nil {
		t.Fatalf("getting role/unscoped: %v", err)
	}

	want := map[string]any{
		"roleName": "Unscoped",
		"policies": []any{map[string]any{"resources": []any{"vms/forwards"}, "verbs": []any{"create"}}},
	}

	if diff := cmp.Diff(want, c.Spec); diff != "" {
		t.Errorf("Save() stored spec mismatch (-want +got):\n%s", diff)
	}

	if err := types.ValidateConfigSpec(*c); err != nil {
		t.Errorf("ValidateConfigSpec(saved role/unscoped) error = %v", err)
	}
}

// The generated permission list holds checks whose resource or verb is a
// constant, as well as those written as literals.
func TestKnownPermissions(t *testing.T) {
	t.Parallel()

	for _, want := range []Permission{
		{"users", "get"}, {"users", "list"}, {"users/roles", "patch"}, {"users/tokens", "create"}, {"configs", "create"},
	} {
		if !slices.Contains(Permissions, want) {
			t.Errorf("Permissions does not include {%s %s}, want it to", want.Resource, want.Verb)
		}
	}
}

// genRolePolicy draws a policy of a few resources, verbs and names, some
// negated or wildcards, now and then with an invalid resource pattern.
func genRolePolicy() *rapid.Generator[*v1.PolicySpec] {
	return rapid.Custom(func(t *rapid.T) *v1.PolicySpec {
		resources := rapid.SliceOfN(rapid.SampledFrom([]string{"vms", "vms/*", "*", "experiments", "**"}), 1, 2).Draw(t, "resources")
		if rapid.IntRange(0, 9).Draw(t, "invalid") == 0 {
			resources = append(resources, "[bad")
		}

		var names []string
		if rapid.IntRange(0, 4).Draw(t, "unnamed") > 0 {
			patterns := []string{"*", "a", "b", "a*", "*/*", "a/*", "!a", "!b*", "!a/b", "!*", "a!", "!!a"}
			names = rapid.SliceOfN(rapid.SampledFrom(patterns), 1, 3).Draw(t, "names")
		}

		verbs := rapid.SliceOfN(rapid.SampledFrom([]string{"get", "list", "*"}), 1, 2).Draw(t, "verbs")

		return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
	})
}

// roleModelAllowed reads the policies as written: a name matched by a pattern and
// by no "!" pattern; any invalid resource pattern (see validPattern) allows nothing.
func roleModelAllowed(spec *v1.RoleSpec, resource, verb string, names []string) bool {
	match := func(name string) func(string) bool {
		return func(pattern string) bool {
			matched, _ := filepath.Match(pattern, name)

			return matched
		}
	}

	for _, p := range spec.Policies {
		for _, r := range p.Resources {
			if !validPattern(r) {
				return false
			}
		}
	}

	for _, p := range spec.Policies {
		if !slices.ContainsFunc(p.Resources, match(resource)) || !slices.Contains(p.Verbs, verb) && !slices.Contains(p.Verbs, "*") {
			continue
		}

		if len(names) == 0 {
			return true
		}

		for _, name := range names {
			var granted, denied bool

			for _, pattern := range p.ResourceNames {
				if negated, ok := strings.CutPrefix(pattern, "!"); ok {
					denied = denied || match(name)(negated)
				} else {
					granted = granted || match(name)(pattern)
				}
			}

			if granted && !denied {
				return true
			}
		}
	}

	return false
}

// rolePolicies is the spec's policies, to print.
func rolePolicies(spec *v1.RoleSpec) []string {
	out := make([]string, len(spec.Policies))
	for i, p := range spec.Policies {
		out[i] = fmt.Sprintf("%q %q %q", p.Resources, p.ResourceNames, p.Verbs)
	}

	return out
}

// Indexed or not, a role allows exactly what its policies say.
func TestRoleAllowedAgreesWithItsPolicies(t *testing.T) {
	t.Parallel()

	queries := [][]string{nil, {"a"}, {"b"}, {"a/b"}, {"ab", "b/a"}, {"b", "a/b"}, {"a!"}, {"!a"}}

	rapid.Check(t, func(t *rapid.T) {
		spec := &v1.RoleSpec{Name: "r", Policies: rapid.SliceOfN(genRolePolicy(), 0, 4).Draw(t, "policies")}
		roles := map[string]Role{"indexed": *newRole(spec, nil), "unindexed": {Spec: spec}}

		for _, resource := range []string{"vms", "vms/start", "experiments", "x"} {
			for _, verb := range []string{"get", "list", "update"} {
				for _, names := range queries {
					want := roleModelAllowed(spec, resource, verb, names)

					for kind, role := range roles {
						if got := role.Allowed(resource, verb, names...); got != want {
							t.Fatalf("Allowed(%q, %q, %q) of the %s role %v = %t, want %t",
								resource, verb, names, kind, rolePolicies(spec), got, want)
						}
					}
				}
			}
		}
	})
}

// GetRoles and RoleFromConfig fail when the store fails to list roles or a
// stored role does not decode, and RoleFromConfig fails for a missing role.
//
//nolint:paralleltest // replaces the store
func TestGetRolesErrors(t *testing.T) {
	t.Run("a failing store", func(t *testing.T) {
		storetest.Use(t, userFailingStore{Store: storetest.NewBolt(t), list: map[string]bool{"Role": true}})

		if roles, err := GetRoles(); roles != nil || !errors.Is(err, errUserStore) {
			t.Errorf("GetRoles() = %v, %v, want nil, %v", roles, err, errUserStore)
		}

		if role, err := RoleFromConfig("disabled"); role != nil || !errors.Is(err, errUserStore) {
			t.Errorf("RoleFromConfig(disabled) = %v, %v, want nil, %v", role, err, errUserStore)
		}
	})

	t.Run("a role that does not decode", func(t *testing.T) {
		storetest.UseBolt(t)
		userStoreRaw(t, "role/broken", map[string]any{"roleName": "Broken", "policies": "all of them"})

		if roles, err := GetRoles(); roles != nil || err == nil || !strings.HasPrefix(err.Error(), "decoding roles config") {
			t.Errorf("GetRoles() = %v, %v, want nil and a decoding error", roles, err)
		}

		for _, name := range []string{"broken", "Broken"} {
			if role, err := RoleFromConfig(name); role != nil || err == nil || !strings.HasPrefix(err.Error(), "decoding role") {
				t.Errorf("RoleFromConfig(%s) = %v, %v, want nil and a decoding error", name, role, err)
			}
		}
	})

	t.Run("a missing role", func(t *testing.T) {
		storetest.UseBolt(t)
		userStoreRaw(t, "role/present", map[string]any{"roleName": "Present"})

		tests := []string{"nobody", "", "present-not", "PRESENT"}

		for _, name := range tests {
			role, err := RoleFromConfig(name)
			if role != nil || !errors.Is(err, ErrRoleNotFound) {
				t.Errorf("RoleFromConfig(%q) = %v, %v, want nil, %v", name, role, err, ErrRoleNotFound)

				continue
			}

			if diff := cmp.Diff("could not find role in store", err.Error()); diff != "" {
				t.Errorf("RoleFromConfig(%q) error message mismatch (-want +got):\n%s", name, diff)
			}
		}
	})
}

// Saving a role that the store fails to update returns the store's error.
//
//nolint:paralleltest // replaces the store
func TestRoleSaveError(t *testing.T) {
	storetest.UseBolt(t)
	userStoreRaw(t, "role/saved", map[string]any{"roleName": "Saved", "policies": []any{}})

	role, err := RoleFromConfig("Saved")
	if err != nil {
		t.Fatalf("RoleFromConfig(Saved) error = %v", err)
	}

	storetest.Use(t, userFailingStore{Store: storetest.NewBolt(t), update: true})

	if err := role.Save(); !errors.Is(err, errUserStore) || !strings.HasPrefix(err.Error(), "updating role in store") {
		t.Errorf("Save() error = %v, want an update error that wraps %v", err, errUserStore)
	}
}

// SetResourceNames with no names, or one empty name, scopes unnamed policies
// to every resource, and a list of names is copied, not shared.
func TestSetResourceNamesDefaults(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		names []string
		want  []string
	}{
		"no names":       {names: nil, want: []string{"*"}},
		"one empty name": {names: []string{""}, want: []string{"*"}},
		"two names":      {names: []string{"a", "b/*"}, want: []string{"a", "b/*"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRole(&v1.RoleSpec{Name: "r", Policies: []*v1.PolicySpec{
				policy("experiments", nil, "get"), policy("vms", nil, "list"),
			}}, nil)

			if err := r.SetResourceNames(tc.names...); err != nil {
				t.Fatalf("SetResourceNames(%q) error = %v", tc.names, err)
			}

			for _, p := range r.Spec.Policies {
				if diff := cmp.Diff(tc.want, p.ResourceNames); diff != "" {
					t.Errorf("SetResourceNames(%q) names of %v mismatch (-want +got):\n%s", tc.names, p.Resources, diff)
				}
			}

			r.Spec.Policies[0].ResourceNames[0] = "changed"
			if r.Spec.Policies[1].ResourceNames[0] == "changed" {
				t.Errorf("SetResourceNames(%q) shares one list between policies", tc.names)
			}
		})
	}
}
