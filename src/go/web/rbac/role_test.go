// Mirrors src/js/test/rbac.test.js so client and server RBAC matching stay aligned.
package rbac

import (
	"testing"

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

// TestRoleAllowed verifies resource, verb, and resource name matching.
func TestRoleAllowed(t *testing.T) {
	role := newTestRole(t)

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
			if got := role.Allowed(tt.resource, tt.verb, tt.names...); got != tt.want {
				t.Errorf("Allowed(%q, %q, %q) = %v, want %v", tt.resource, tt.verb, tt.names, got, tt.want)
			}
		})
	}
}
