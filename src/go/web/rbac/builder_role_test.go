package rbac

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
)

// configWritesStore counts the config writes made through it.
type configWritesStore struct {
	store.Store

	writes []string
}

func (s *configWritesStore) Create(c *store.Config) error {
	s.writes = append(s.writes, "create "+c.FullName())

	return s.Store.Create(c) //nolint:wrapcheck // the wrapped store's answer
}

func (s *configWritesStore) Update(c *store.Config) error {
	s.writes = append(s.writes, "update "+c.FullName())

	return s.Store.Update(c) //nolint:wrapcheck // the wrapped store's answer
}

func (s *configWritesStore) Delete(c *store.Config) error {
	s.writes = append(s.writes, "delete "+c.FullName())

	return s.Store.Delete(c) //nolint:wrapcheck // the wrapped store's answer
}

// useDefaultConfigs makes the config store a new BoltDB holding the default
// configs, and returns it with its writes counted from then on.
func useDefaultConfigs(t *testing.T) *configWritesStore {
	t.Helper()

	storetest.UseBolt(t)

	base := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // the test's own base directory

	t.Cleanup(func() { common.PhenixBase = base }) //nolint:reassign // restore the base directory

	if err := config.Init(); err != nil {
		t.Fatalf("config.Init returned error: %v", err)
	}

	counting := &configWritesStore{Store: store.DefaultStore, writes: nil}
	store.DefaultStore = counting //nolint:reassign // count the test's writes

	return counting
}

// builderRolePolicies is every permission the Builder needs: the policies
// of the built-in Builder role.
func builderRolePolicies() []*v1.PolicySpec {
	return []*v1.PolicySpec{
		{
			Resources:     []string{"configs"},
			ResourceNames: []string{"Topology/*", "Scenario/*", "Experiment/*"},
			Verbs:         []string{"list", "get", "create", "update", "delete"},
		},
		{Resources: []string{"builder"}, ResourceNames: nil, Verbs: []string{"use"}},
		{Resources: []string{"schemas"}, ResourceNames: []string{"*"}, Verbs: []string{"get"}},
		{Resources: []string{"topologies", "scenarios"}, ResourceNames: []string{"*"}, Verbs: []string{"list", "get"}},
		{Resources: []string{"experiments"}, ResourceNames: []string{"*"}, Verbs: []string{"list", "get", "create", "update"}},
		{Resources: []string{"disks"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
	}
}

// mustRole returns the stored role with the given name or role name.
func mustRole(t *testing.T, name string) *Role {
	t.Helper()

	role, err := RoleFromConfig(name)
	if err != nil {
		t.Fatalf("RoleFromConfig(%s) returned error: %v", name, err)
	}

	return role
}

// TestBuiltinBuilderRole asserts the built-in Builder role holds every
// permission the Builder checks and nothing more, and the migration writes nothing.
func TestBuiltinBuilderRole(t *testing.T) {
	counting := useDefaultConfigs(t)

	role := mustRole(t, "builder")

	if role.Spec.Name != "Builder" || !reflect.DeepEqual(role.Spec.Policies, builderRolePolicies()) {
		t.Fatalf("the built-in Builder role is %q with %s", role.Spec.Name, policiesText(role.Spec.Policies))
	}

	if mustRole(t, "Builder").config.Metadata.Name != "builder" {
		t.Fatal("the role is not found by its role name")
	}

	// The configs the Builder reads and publishes, by full name and by verb
	// alone, as a Builder route checks first.
	for _, verb := range []string{"list", "get", "create", "update", "delete"} {
		for _, name := range []string{"Topology/site", "Scenario/site", "Experiment/site", ""} {
			var names []string
			if name != "" {
				names = []string{name}
			}

			if !role.Allowed("configs", verb, names...) {
				t.Errorf("the Builder role does not allow configs %s %v", verb, names)
			}
		}

		// Accounts and roles are not the Builder's: their configs hold
		// password hashes and the permissions of every user.
		for _, name := range []string{"User/alice", "Role/builder", "Role/global-admin"} {
			if role.Allowed("configs", verb, name) {
				t.Errorf("the Builder role allows configs %s %s", verb, name)
			}
		}
	}

	for _, check := range []struct{ resource, verb, name string }{
		{"builder", "use", ""},
		{"schemas", "get", "Topology"},
		{"topologies", "list", "site"},
		{"topologies", "get", "site"},
		{"scenarios", "list", "site"},
		{"experiments", "list", "site"},
		{"experiments", "get", "site"},
		{"experiments", "create", "site"},
		{"experiments", "update", "site"},
		{"disks", "list", ""},
	} {
		var names []string
		if check.name != "" {
			names = []string{check.name}
		}

		if !role.Allowed(check.resource, check.verb, names...) {
			t.Errorf("the Builder role does not allow %s %s %v", check.resource, check.verb, names)
		}
	}

	// Nothing beyond the Builder.
	for _, check := range []struct{ resource, verb string }{
		{"vms", "list"}, {"users", "list"}, {"experiments", "delete"}, {"experiments/start", "update"}, {"roles", "list"},
		{"builder", "list"}, {"builder", "get"}, {"builder", "update"}, {"builder", "delete"},
	} {
		if role.Allowed(check.resource, check.verb) {
			t.Errorf("the Builder role allows %s %s", check.resource, check.verb)
		}
	}

	roles, err := GetRoles()
	if err != nil {
		t.Fatalf("GetRoles returned error: %v", err)
	}

	// Global Viewer lists and gets every resource, but the Builder is not
	// one it may use.
	var users []string

	for _, role := range roles {
		if role.Allowed(builderResource, builderUseVerb) {
			users = append(users, role.Spec.Name)
		}
	}

	slices.Sort(users)

	if want := []string{"Builder", "Global Admin"}; !slices.Equal(users, want) {
		t.Errorf("the built-in roles that may use the Builder are %q, want %q", users, want)
	}

	if mustRole(t, "global-viewer").Allowed(builderResource, builderUseVerb) {
		t.Error("the Global Viewer role may use the Builder")
	}

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Fatalf("the migration wrote %q to a new store", counting.writes)
	}
}

// TestEnsureBuilderRoleCreatesMissingRole creates the built-in Builder role
// at start in a store that holds none.
func TestEnsureBuilderRoleCreatesMissingRole(t *testing.T) {
	counting := useDefaultConfigs(t)

	if err := config.Delete("role/builder"); err != nil {
		t.Fatalf("deleting the Builder role: %v", err)
	}

	counting.writes = nil

	for range 2 {
		if err := EnsureBuilderRolePermissions(); err != nil {
			t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
		}
	}

	if want := []string{"create Role/builder"}; !slices.Equal(counting.writes, want) {
		t.Fatalf("the migration wrote %q, want %q", counting.writes, want)
	}

	if role := mustRole(t, "Builder"); !reflect.DeepEqual(role.Spec.Policies, builderRolePolicies()) {
		t.Fatalf("the Builder role made at start has %s", policiesText(role.Spec.Policies))
	}
}

// setBuilderPolicies replaces the policies of the stored Builder role, as an
// administrator may, and clears the counted writes.
func setBuilderPolicies(t *testing.T, counting *configWritesStore, policies []any) {
	t.Helper()

	builder, err := config.Get("role/builder", false)
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	builder.Spec = map[string]any{"roleName": "Builder", "policies": policies}

	if err := store.Update(builder); err != nil {
		t.Fatalf("replacing the Builder role: %v", err)
	}

	counting.writes = nil
}

// TestEnsureBuilderRolePermissions adds the Builder policy to a Builder role
// an administrator made, and to its user, keeping the rest, once.
func TestEnsureBuilderRolePermissions(t *testing.T) {
	counting := useDefaultConfigs(t)
	made := []any{
		map[string]any{"resources": []any{"configs"}, "resourceNames": []any{"*", "*/*"}, "verbs": []any{"list", "get"}},
		map[string]any{"resources": []any{"disks"}, "verbs": []any{"list"}},
	}

	setBuilderPolicies(t, counting, made)

	alice, err := NewUser("alice", "Testpass1!", "alice", "Tester")
	if err != nil {
		t.Fatalf("NewUser returned error: %v", err)
	}

	if err := alice.SetRole(mustRole(t, "builder")); err != nil {
		t.Fatalf("assigning alice: %v", err)
	}

	counting.writes = nil

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
	}

	if want := []string{"update Role/builder", "update User/alice"}; !sameWrites(counting.writes, want) {
		t.Fatalf("the migration wrote %q, want %q", counting.writes, want)
	}

	stored, err := config.Get("role/builder", false)
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	added := &v1.PolicySpec{Resources: []string{"builder"}, ResourceNames: nil, Verbs: []string{"use"}}

	if policies, _ := stored.Spec["policies"].([]any); len(policies) != 3 || !reflect.DeepEqual(policies[:2], made) ||
		!reflect.DeepEqual(mustRole(t, "builder").Spec.Policies[2], added) {
		t.Fatalf("the Builder role's policies are %v, want %v and the Builder policy", policies, made)
	}

	user, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}

	if policies := user.Spec.Role.Policies; len(policies) != 3 || !reflect.DeepEqual(policies[2], added) {
		t.Errorf("alice after the migration: %s", policiesText(policies))
	}

	counting.writes = nil

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Fatalf("running the migration again wrote %q", counting.writes)
	}
}

// TestEnsureBuilderRoleKeepsWildcards leaves a Builder role that allows the
// permission through a wildcard as it is.
func TestEnsureBuilderRoleKeepsWildcards(t *testing.T) {
	counting := useDefaultConfigs(t)

	for _, policy := range []map[string]any{
		{"resources": []any{"builder"}, "verbs": []any{"*"}},
		{"resources": []any{"*"}, "verbs": []any{"*"}},
	} {
		setBuilderPolicies(t, counting, []any{policy})

		if err := EnsureBuilderRolePermissions(); err != nil {
			t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
		}

		if len(counting.writes) != 0 {
			t.Fatalf("the migration wrote %q to a role with %v", counting.writes, policy)
		}
	}
}

// TestEnsureBuilderRoleWithoutSpec gives the Builder permission to a stored builder role that has no spec.
func TestEnsureBuilderRoleWithoutSpec(t *testing.T) {
	storetest.UseBolt(t)

	if err := store.Create(
		&store.Config{Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: store.ConfigMetadata{Name: "builder"}},
	); err != nil {
		t.Fatalf("creating the role: %v", err)
	}

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions returned error: %v", err)
	}

	if stored := mustRole(t, "builder"); !stored.Allowed(builderResource, builderUseVerb) {
		t.Fatalf("the role written back has %s", policiesText(stored.Spec.Policies))
	}
}

// sameWrites reports whether two lists of writes hold the same writes, in
// any order.
func sameWrites(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

// policiesText returns policies as text for a failure message.
func policiesText(policies []*v1.PolicySpec) string {
	parts := make([]string, 0, len(policies))

	for _, policy := range policies {
		parts = append(parts, fmt.Sprintf("%v %v %v", policy.Resources, policy.ResourceNames, policy.Verbs))
	}

	return strings.Join(parts, "; ")
}
