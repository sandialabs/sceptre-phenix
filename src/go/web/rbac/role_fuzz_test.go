package rbac

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
)

// Fuzz inputs encode policies as lines of tab-separated fields (resources,
// resource names, verbs), with fuzzItemSep between the items of a field.
const (
	fuzzItemSep     = "\x1f"
	fuzzFieldSep    = "\t"
	fuzzPolicySep   = "\n"
	fuzzMaxInput    = 256
	fuzzMaxPolicies = 8
)

// fuzzItems splits a field into items; an empty field is nil.
func fuzzItems(s string) []string {
	if s == "" {
		return nil
	}

	return strings.Split(s, fuzzItemSep)
}

func decodeFuzzPolicies(s string) []*v1.PolicySpec {
	if s == "" {
		return nil
	}

	var policies []*v1.PolicySpec

	for line := range strings.SplitSeq(s, fuzzPolicySep) {
		fields := strings.SplitN(line, fuzzFieldSep, 3)
		fields = append(fields, "", "")

		policies = append(policies, &v1.PolicySpec{
			Resources:     fuzzItems(fields[0]),
			ResourceNames: fuzzItems(fields[1]),
			Verbs:         fuzzItems(fields[2]),
		})
	}

	return policies
}

func encodeFuzzPolicies(policies []*v1.PolicySpec) string {
	lines := make([]string, len(policies))
	for i, p := range policies {
		lines[i] = strings.Join([]string{
			strings.Join(p.Resources, fuzzItemSep),
			strings.Join(p.ResourceNames, fuzzItemSep),
			strings.Join(p.Verbs, fuzzItemSep),
		}, fuzzFieldSep)
	}

	return strings.Join(lines, fuzzPolicySep)
}

func cloneFuzzSpec(spec *v1.RoleSpec) *v1.RoleSpec {
	out := &v1.RoleSpec{Name: spec.Name, Policies: slices.Clone(spec.Policies)}
	for i, p := range spec.Policies {
		out.Policies[i] = &v1.PolicySpec{
			Resources:     slices.Clone(p.Resources),
			ResourceNames: slices.Clone(p.ResourceNames),
			Verbs:         slices.Clone(p.Verbs),
		}
	}

	return out
}

// FuzzRoleAllowed checks that indexed and unindexed roles agree with
// roleModelAllowed, also after SetResourceNames (ops bit 0) and AddPolicy of
// the last policy (ops bit 1). A copy made before AddPolicy must keep
// answering for the policies it had.
func FuzzRoleAllowed(f *testing.F) {
	// newTestRole uses its *testing.T only to mark itself a helper.
	shared := encodeFuzzPolicies(newTestRole(new(testing.T)).Spec.Policies)

	for _, q := range [][]string{
		{"experiments", "get", "expA"}, {"experiments/start", "update", ""}, {"experiments", "delete", "exp1"},
		{"vms/start", "patch", "vm1"}, {"vms", "delete", "expA/vm1"}, {"vms", "update", "expA/vm1"},
		{"vms", "create", "expB/vm1"}, {"vms", "get", "vm1"}, {"vms", "list", "expA/vm1"},
		{"vms", "post", "{expA,expB}/vm1"}, {"vms/start", "update", "expA/secret"},
		{"vms", "update", "expB/vm1" + fuzzItemSep + "expA/vm1"}, {"vms/start", "watch", "vm1"},
		{"things", "get", ".thing"}, {"things", "delete", "thing1"}, {"items", "delete", "item1"},
		{"notes", "get", "note!"}, {"notes", "get", "note"},
	} {
		f.Add(shared, q[0], q[1], q[2], "", uint8(0))
	}

	index := encodeFuzzPolicies(testRoleSpec().Policies)
	invalid := index + fuzzPolicySep + "[bad" + fuzzFieldSep + fuzzFieldSep + "*"

	f.Add(index, "vms", "list", "exp_a", "", uint8(0))
	f.Add(index, "vms", "list", "exp_secret", "", uint8(0))
	f.Add(invalid, "vms", "list", "exp_a", "", uint8(0))
	f.Add(invalid, "experiments/start", "update", "exp", "", uint8(0))
	f.Add("users"+fuzzFieldSep+"bob"+fuzzFieldSep+"get", "users", "get", "bob", "", uint8(2))
	f.Add("experiments\tsecret\tget\nvms\t\tget", "vms", "get", "exp-a", "exp-a"+fuzzItemSep+"exp-a/*", uint8(1))
	f.Add("vms\t\tget", "vms", "get", "x", "[", uint8(3))

	f.Fuzz(func(t *testing.T, policies, resource, verb, names, scope string, ops uint8) {
		if len(policies)+len(resource)+len(verb)+len(names)+len(scope) > fuzzMaxInput {
			t.Skip("input too large")
		}

		decoded := decodeFuzzPolicies(policies)
		if len(decoded) > fuzzMaxPolicies {
			t.Skip("too many policies")
		}

		if probeDependent(decoded, resource) {
			t.Skip("resource pattern validity depends on the probe string")
		}

		var added *v1.PolicySpec
		if ops&2 != 0 && len(decoded) > 0 {
			added, decoded = decoded[len(decoded)-1], decoded[:len(decoded)-1]
		}

		spec := &v1.RoleSpec{Name: "fuzz", Policies: decoded}
		r := newRole(spec, nil)

		if ops&1 != 0 {
			checkSetResourceNames(t, r, fuzzItems(scope))
		}

		earlier := *r
		before := &v1.RoleSpec{Name: spec.Name, Policies: slices.Clone(spec.Policies)}

		if added != nil {
			r.AddPolicy(added.Resources, added.ResourceNames, added.Verbs)
		}

		for _, query := range [][]string{fuzzItems(names), nil} {
			want := roleModelAllowed(spec, resource, verb, query)

			for kind, role := range map[string]Role{"indexed": *r, "unindexed": {Spec: spec}} {
				if got := role.Allowed(resource, verb, query...); got != want {
					t.Fatalf("Allowed(%q, %q, %q) of the %s role %v = %t, want %t",
						resource, verb, query, kind, rolePolicies(spec), got, want)
				}
			}

			want = roleModelAllowed(before, resource, verb, query)
			if got := earlier.Allowed(resource, verb, query...); got != want {
				t.Fatalf("Allowed(%q, %q, %q) of a copy made before AddPolicy %v = %t, want %t",
					resource, verb, query, rolePolicies(before), got, want)
			}
		}
	})
}

// probeDependent reports whether a resource pattern is bad against only one of
// "useless" (the probe of mapPolicies) and resource (the probe of
// roleModelAllowed). For such a pattern, the two do not agree.
func probeDependent(policies []*v1.PolicySpec, resource string) bool {
	for _, p := range policies {
		for _, r := range p.Resources {
			_, errProbe := filepath.Match(r, "useless")
			_, errResource := filepath.Match(r, resource)

			if (errProbe == nil) != (errResource == nil) {
				return true
			}
		}
	}

	return false
}

// checkSetResourceNames calls r.SetResourceNames(names...) and checks that it
// refuses a bad pattern without a change, and otherwise scopes only the
// policies that have no resource names.
func checkSetResourceNames(t *testing.T, r *Role, names []string) {
	t.Helper()

	scope := slices.Clone(names)
	if len(scope) == 0 || len(scope) == 1 && scope[0] == "" {
		scope = []string{"*"}
	}

	var bad bool

	for _, n := range scope {
		// SetResourceNames probes with "useless", and filepath.Match finds a
		// bad pattern only when it parses that part of the pattern.
		if _, err := filepath.Match(n, "useless"); err != nil {
			bad = true
		}
	}

	want := cloneFuzzSpec(r.Spec)
	if !bad {
		for _, p := range want.Policies {
			if len(p.ResourceNames) == 0 {
				p.ResourceNames = slices.Clone(scope)
			}
		}
	}

	err := r.SetResourceNames(names...)
	if bad != errors.Is(err, ErrResourceNameInvalid) || !bad && err != nil {
		t.Fatalf("SetResourceNames(%q) error = %v, want a bad pattern error: %t", names, err, bad)
	}

	if diff := cmp.Diff(want, r.Spec); diff != "" {
		t.Fatalf("SetResourceNames(%q) spec mismatch (-want +got):\n%s", names, diff)
	}
}
