package rbac

import (
	"slices"
	"testing"
)

// KnownPermissions lists each resource of Resources once, in order, with
// exactly the verbs of Permissions on it, sorted, and each permission's
// resource is one of Resources.
func TestKnownPermissionsByResource(t *testing.T) {
	t.Parallel()

	known := KnownPermissions()

	if len(known) != len(Resources) {
		t.Fatalf("KnownPermissions() has %d resources, want %d", len(known), len(Resources))
	}

	count := 0

	for i, rv := range known {
		if rv.Resource != Resources[i] {
			t.Errorf("KnownPermissions()[%d].Resource = %q, want %q", i, rv.Resource, Resources[i])
		}

		if rv.Verbs == nil {
			t.Errorf("KnownPermissions()[%d].Verbs = nil, want a list", i)
		}

		if !slices.IsSorted(rv.Verbs) {
			t.Errorf("KnownPermissions() verbs of %s = %v, want them sorted", rv.Resource, rv.Verbs)
		}

		for _, verb := range rv.Verbs {
			if !slices.Contains(Permissions, Permission{Resource: rv.Resource, Verb: verb}) {
				t.Errorf("KnownPermissions() gives %s %s, which Permissions does not hold", rv.Resource, verb)
			}
		}

		count += len(rv.Verbs)
	}

	if count != len(Permissions) {
		t.Errorf("KnownPermissions() has %d verbs in all, want the %d of Permissions", count, len(Permissions))
	}
}
