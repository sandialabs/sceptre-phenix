package web

import (
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// testRole returns a role with one policy.
func testRole(resource, verb string, names ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{resource},
					ResourceNames: names,
					Verbs:         []string{verb},
				},
			},
		},
	}
}

// adminRole may do anything, as Global Admin may.
func adminRole() rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
		Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"},
	}}}}
}
