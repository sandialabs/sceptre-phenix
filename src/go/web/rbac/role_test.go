package rbac

import (
	"slices"
	"testing"

	v1 "phenix/types/version/v1"
)

// TestSetResourceNamesKeepsGoingPastPoliciesThatHaveTheirOwn verifies that a policy
// carrying its own resource names does not stop the policies after it from being
// given the caller's names. The shipped experiment-admin role has exactly this
// shape: a disks policy with resourceNames sits ahead of the experiments/files one.
func TestSetResourceNamesKeepsGoingPastPoliciesThatHaveTheirOwn(t *testing.T) {
	role := &Role{Spec: &v1.RoleSpec{
		Name: "Experiment Admin",
		Policies: []*v1.PolicySpec{
			{Resources: []string{"experiments", "experiments/*"}, Verbs: []string{"get"}},
			{Resources: []string{"disks"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
			{Resources: []string{experimentFilesResource}, Verbs: []string{experimentFilesCreateVerb}},
		},
	}}

	if err := role.SetResourceNames("exp-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(role.Spec.Policies[0].ResourceNames, "exp-a") {
		t.Fatal("expected the experiments policy to be given the resource name")
	}

	if !slices.Equal(role.Spec.Policies[1].ResourceNames, []string{"*"}) {
		t.Fatalf("expected the disks policy to keep its own names, got %#v",
			role.Spec.Policies[1].ResourceNames)
	}

	if !slices.Contains(role.Spec.Policies[2].ResourceNames, "exp-a") {
		t.Fatal("expected the policy after the disks one to be given the resource name")
	}

	if !role.Allowed(experimentFilesResource, experimentFilesCreateVerb, "exp-a") {
		t.Fatal("expected experiment file creation to be allowed for exp-a")
	}
}
