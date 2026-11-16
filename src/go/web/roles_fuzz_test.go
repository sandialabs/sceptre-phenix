package web

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// FuzzParseRoleRequests checks the POST /roles and PATCH /roles/{name} bodies
// against encoding/json and the Role schema: a body that encoding/json cannot
// parse is refused, an accepted body gives valid names and policies whose
// stored config passes the schema, and its re-encoding parses the same.
func FuzzParseRoleRequests(f *testing.F) {
	for _, seed := range []string{
		`{"name":"Lab Admin","policies":[{"resources":["vms"],"resourceNames":["exp1/*"],"verbs":["list"]}]}`,
		`{"name":"Night Shift","metadataName":"night","policies":[]}`,
		`{"name":"x","policies":[{"resources":["rockets"],"verbs":["get"]}]}`,
		`{"name":"x","policy":[]}`,
		`{"name":"Lab","policies":[{"resources":["*","*/*"],"resourceNames":["*","!a"],"verbs":["*"]}]}`,
		`{"name":"Lab","policies":[{"resources":["vms/*"],"resourceNames":["exp1/vm[12]"],"verbs":["get"]}]}`,
		`{"policies":[]}`,
		"{\"name\":\"Lab\u200b\",\"policies\":[]}",
		`{"policies":null}`,
		`{"name":"a/b","policies":[]} {}`,
		`{`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		var anything any
		parsable := json.Unmarshal([]byte(body), &anything) == nil

		configName, spec, err := parseCreateRole([]byte(body))
		checkRoleParse(t, body, parsable, err)

		if err == nil {
			checkCreatedRole(t, configName, spec)
		}

		name, policies, err := parseUpdateRole([]byte(body))
		checkRoleParse(t, body, parsable, err)

		if err == nil {
			checkUpdatedRole(t, name, policies)
		}
	})
}

// checkRoleParse fails the test for an error that is not a role request or
// policy error, or no error for a body that encoding/json cannot parse.
func checkRoleParse(t *testing.T, body string, parsable bool, err error) {
	t.Helper()

	if err != nil && !errors.Is(err, errRoleRequest) && !errors.Is(err, rbac.ErrPolicyInvalid) {
		t.Fatalf("parsing %q error = %v, want a role request or policy error", body, err)
	}

	if err == nil && !parsable {
		t.Fatalf("parsing %q error = nil, but encoding/json cannot parse it", body)
	}
}

// checkCreatedRole fails the test unless the role passes the checks and the
// Role schema, and its re-encoded request parses the same.
func checkCreatedRole(t *testing.T, configName string, spec *v1.RoleSpec) {
	t.Helper()

	if checkRoleName(spec.Name) != nil || !validRoleConfigName(configName) || !acceptablePolicies(spec.Policies) {
		t.Fatalf("parseCreateRole() = %q, %+v, which fails its own checks", configName, spec)
	}

	c := &store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: store.ConfigMetadata{Name: configName},
		Spec: structs.MapDefaultCase(spec, structs.CASESNAKE),
	}

	if err := config.Validate(c); err != nil {
		t.Fatalf("config.Validate(role %s) error = %v", configName, err)
	}

	policies := rolePolicyList(spec.Policies)

	again, err := json.Marshal(CreateRoleRequest{Name: spec.Name, MetadataName: configName, Policies: &policies})
	if err != nil {
		t.Fatal(err)
	}

	gotName, gotSpec, err := parseCreateRole(again)
	if err != nil || gotName != configName || !cmp.Equal(spec, gotSpec) {
		t.Fatalf("parseCreateRole(%s) = %q, %+v, %v, want %q, %+v", again, gotName, gotSpec, err, configName, spec)
	}
}

// checkUpdatedRole fails the test unless the policies pass the checks, and
// the re-encoded request parses the same.
func checkUpdatedRole(t *testing.T, name string, policies []*v1.PolicySpec) {
	t.Helper()

	if !acceptablePolicies(policies) {
		t.Fatalf("parseUpdateRole() policies = %+v, which fail their own checks", policies)
	}

	list := rolePolicyList(policies)

	again, err := json.Marshal(UpdateRoleRequest{Name: name, Policies: &list})
	if err != nil {
		t.Fatal(err)
	}

	gotName, got, err := parseUpdateRole(again)
	if err != nil || gotName != name || !cmp.Equal(policies, got) {
		t.Fatalf("parseUpdateRole(%s) = %q, %+v, %v, want %q, %+v", again, gotName, got, err, name, policies)
	}
}

// acceptablePolicies reports whether the policies are valid, within the
// limits, and grant no role change.
func acceptablePolicies(policies []*v1.PolicySpec) bool {
	if len(policies) > maxRolePolicies || rbac.ValidatePolicies(policies) != nil {
		return false
	}

	for _, p := range policies {
		if len(p.Resources) > maxPolicyItems || len(p.Verbs) > maxPolicyItems || len(p.ResourceNames) > maxPolicyItems {
			return false
		}
	}

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: policies}}

	return !role.Allowed(resourceRoles, "create") && !role.Allowed(resourceRoles, verbPatch) && !role.Allowed(resourceRoles, "delete")
}

// rolePolicyList is the policies as a request body lists them.
func rolePolicyList(specs []*v1.PolicySpec) []Policy {
	policies := make([]Policy, len(specs))
	for i, p := range specs {
		policies[i] = Policy{Resources: p.Resources, ResourceNames: p.ResourceNames, Verbs: p.Verbs}
	}

	return policies
}
