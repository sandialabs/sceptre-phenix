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
			patterns := []string{"*", "a", "b", "a*", "*/*", "a/*", "!a", "!b*", "!a/b", "!*"}
			names = rapid.SliceOfN(rapid.SampledFrom(patterns), 1, 3).Draw(t, "names")
		}

		verbs := rapid.SliceOfN(rapid.SampledFrom([]string{"get", "list", "*"}), 1, 2).Draw(t, "verbs")

		return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
	})
}

// roleModelAllowed reads the policies as written: a name matched by a pattern and
// by no "!" pattern; any invalid resource pattern allows nothing.
func roleModelAllowed(spec *v1.RoleSpec, resource, verb string, names []string) bool {
	match := func(name string) func(string) bool {
		return func(pattern string) bool {
			matched, _ := filepath.Match(pattern, name)

			return matched
		}
	}

	for _, p := range spec.Policies {
		for _, r := range p.Resources {
			if _, err := filepath.Match(r, resource); err != nil {
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

	queries := [][]string{nil, {"a"}, {"b"}, {"a/b"}, {"ab", "b/a"}, {"b", "a/b"}}

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
