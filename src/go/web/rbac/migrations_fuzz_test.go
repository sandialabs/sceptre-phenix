package rbac

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
)

// FuzzDropNullNames runs dropNullNames on any decoded JSON. It must remove
// each null resourceNames from a list of policies and nothing else, report
// a change only when it made one, and change nothing on a second pass.
func FuzzDropNullNames(f *testing.F) {
	for _, seed := range []string{
		`[{"resources":["vms"],"resourceNames":null,"verbs":["get"]},` +
			`{"resources":["hosts"],"resourceNames":["*"],"verbs":["list"]}]`,
		`[{"resources":["vms"],"verbs":["get"]}]`,
		`[null, 1, "x", [], {"resourceNames":[]}, {"resourceNames":null}]`,
		`{"policies":[{"resourceNames":null}]}`,
		`null`, `[]`, `"policies"`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip("input too large")
		}

		var before, got any
		if json.Unmarshal(data, &before) != nil || json.Unmarshal(data, &got) != nil {
			t.Skip("not JSON")
		}

		changed := dropNullNames(got)

		list, _ := got.([]any)
		beforeList, _ := before.([]any)

		var removed bool

		for i, p := range list {
			policy, _ := p.(map[string]any)
			if names, ok := policy["resourceNames"]; ok && names == nil {
				t.Fatalf("dropNullNames(%s) kept the null resourceNames of policy %d", data, i)
			}

			beforePolicy, _ := beforeList[i].(map[string]any)
			if names, ok := beforePolicy["resourceNames"]; ok && names == nil {
				removed = true
			}
		}

		if changed != removed {
			t.Fatalf("dropNullNames(%s) = %t, want %t", data, changed, removed)
		}

		after, _ := json.Marshal(got)
		if dropNullNames(got) {
			t.Fatalf("dropNullNames(%s) on a second pass = true, want false", data)
		}

		if again, _ := json.Marshal(got); string(again) != string(after) {
			t.Fatalf("dropNullNames(%s) on a second pass changed %s to %s", data, after, again)
		}

		// Putting back the null names must give the input again.
		for i, p := range list {
			beforePolicy, _ := beforeList[i].(map[string]any)
			if names, ok := beforePolicy["resourceNames"]; ok && names == nil {
				p.(map[string]any)["resourceNames"] = nil
			}
		}

		if diff := cmp.Diff(before, got); diff != "" {
			t.Fatalf("dropNullNames(%s) changed more than null resourceNames (-input +restored):\n%s", data, diff)
		}
	})
}

// FuzzEnsurePolicy checks that ensurePolicy only appends the missing verbs
// and names to the first policy that lists resource, or adds one policy when
// none does, reports a change only when it made one, and is idempotent.
func FuzzEnsurePolicy(f *testing.F) {
	for _, seed := range []struct {
		policies     []*v1.PolicySpec
		verbs, names []string
		resource     string
	}{
		{[]*v1.PolicySpec{policy("experiments", nil, "get")}, []string{experimentFilesCreateVerb}, nil, experimentFilesResource},
		{
			[]*v1.PolicySpec{policy(experimentFilesResource, []string{"exp-a"}, "get")},
			[]string{experimentFilesCreateVerb}, []string{"exp-a", "exp-b"}, experimentFilesResource,
		},
		{
			[]*v1.PolicySpec{policy(experimentFilesResource, []string{"exp-a"}, experimentFilesCreateVerb)},
			[]string{experimentFilesCreateVerb}, []string{"exp-a"}, experimentFilesResource,
		},
		{
			[]*v1.PolicySpec{policy("vms", nil, "get"), policy("vms", []string{"a"}, "list")},
			[]string{"get", "get", "update"}, []string{"b", "b"}, "vms",
		},
	} {
		f.Add(encodeFuzzPolicies(seed.policies), seed.resource,
			strings.Join(seed.verbs, fuzzItemSep), strings.Join(seed.names, fuzzItemSep))
	}

	f.Fuzz(func(t *testing.T, policies, resource, verbs, names string) {
		if len(policies)+len(resource)+len(verbs)+len(names) > fuzzMaxInput {
			t.Skip("input too large")
		}

		spec := &v1.RoleSpec{Name: "fuzz", Policies: decodeFuzzPolicies(policies)}
		before := cloneFuzzSpec(spec)
		verbList, nameList := fuzzItems(verbs), fuzzItems(names)

		changed := ensurePolicy(spec, resource, verbList, nameList)

		want := cloneFuzzSpec(before)

		target := slices.IndexFunc(want.Policies, func(p *v1.PolicySpec) bool { return slices.Contains(p.Resources, resource) })
		if target < 0 {
			want.Policies = append(want.Policies, &v1.PolicySpec{
				Resources: []string{resource}, ResourceNames: slices.Clone(nameList), Verbs: slices.Clone(verbList),
			})
		} else {
			p := want.Policies[target]
			p.Verbs = appendMissing(p.Verbs, verbList)
			p.ResourceNames = appendMissing(p.ResourceNames, nameList)
		}

		if diff := cmp.Diff(want, spec); diff != "" {
			t.Fatalf("ensurePolicy(%v, %q, %q, %q) spec mismatch (-want +got):\n%s",
				rolePolicies(before), resource, verbList, nameList, diff)
		}

		if wantChanged := !cmp.Equal(before, spec); changed != wantChanged {
			t.Fatalf("ensurePolicy(%v, %q, %q, %q) = %t, want %t",
				rolePolicies(before), resource, verbList, nameList, changed, wantChanged)
		}

		once := cloneFuzzSpec(spec)
		if ensurePolicy(spec, resource, verbList, nameList) {
			t.Fatalf("ensurePolicy(%v, %q, %q, %q) on a second pass = true, want false",
				rolePolicies(once), resource, verbList, nameList)
		}

		if diff := cmp.Diff(once, spec); diff != "" {
			t.Fatalf("ensurePolicy on a second pass changed the spec (-first +second):\n%s", diff)
		}
	})
}

// appendMissing appends each item of add that list, as it grows, does not hold.
func appendMissing(list, add []string) []string {
	for _, a := range add {
		if !slices.Contains(list, a) {
			list = append(list, a)
		}
	}

	return list
}
