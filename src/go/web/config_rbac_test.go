package web

import (
	"strings"

	"phenix/web/rbac"
)

// topologyConfigs is a role that may act on Topology and Scenario configs only.
func topologyConfigs(verbs ...string) rbac.Role {
	roles := make([]rbac.Role, 0, len(verbs))
	for _, verb := range verbs {
		roles = append(roles, testRole("configs", verb, "Topology/*", "Scenario/*"))
	}

	return combinedRole(roles...)
}

// adminPolicies are the policies of a role that may do anything.
const adminPolicies = `[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]`

// configJSON is a valid User or Role config granting every permission.
func configJSON(kind, name string) string {
	spec := `{"roleName":"` + name + `","policies":` + adminPolicies + `}`
	if strings.EqualFold(kind, "User") {
		spec = `{"username":"` + name + `","first_name":"i","last_name":"i","password":"x","rbac":` + spec + `}`
	}

	return `{"apiVersion":"phenix.sandia.gov/v1","kind":"` + kind + `","metadata":{"name":"` + name + `"},"spec":` + spec + `}`
}
