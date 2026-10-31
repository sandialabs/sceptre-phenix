package rbac

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

	want := &v1.RoleSpec{Name: "Builder", Policies: builderRolePolicies()}
	if diff := cmp.Diff(want, role.Spec); diff != "" {
		t.Errorf("RoleFromConfig(builder) spec mismatch (-want +got):\n%s", diff)
	}

	if got := mustRole(t, "Builder").config.Metadata.Name; got != "builder" {
		t.Errorf("RoleFromConfig(Builder) config name = %q, want builder", got)
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
				t.Errorf("Builder role Allowed(configs, %s, %q) = false, want true", verb, names)
			}
		}

		// Accounts and roles are not the Builder's: their configs hold
		// password hashes and the permissions of every user.
		for _, name := range []string{"User/alice", "Role/builder", "Role/global-admin"} {
			if role.Allowed("configs", verb, name) {
				t.Errorf("Builder role Allowed(configs, %s, %s) = true, want false", verb, name)
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
			t.Errorf("Builder role Allowed(%s, %s, %q) = false, want true", check.resource, check.verb, names)
		}
	}

	// Nothing beyond the Builder.
	for _, check := range []struct{ resource, verb string }{
		{"vms", "list"}, {"users", "list"}, {"experiments", "delete"}, {"experiments/start", "update"}, {"roles", "list"},
		{"builder", "list"}, {"builder", "get"}, {"builder", "update"}, {"builder", "delete"},
	} {
		if role.Allowed(check.resource, check.verb) {
			t.Errorf("Builder role Allowed(%s, %s) = true, want false", check.resource, check.verb)
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
		t.Errorf("built-in roles Allowed(builder, use) = %q, want %q", users, want)
	}

	if mustRole(t, "global-viewer").Allowed(builderResource, builderUseVerb) {
		t.Error("Global Viewer role Allowed(builder, use) = true, want false")
	}

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Errorf("EnsureBuilderRolePermissions() on a new store wrote %q, want none", counting.writes)
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
			t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
		}
	}

	if want := []string{"create Role/builder"}; !slices.Equal(counting.writes, want) {
		t.Errorf("EnsureBuilderRolePermissions() twice wrote %q, want %q", counting.writes, want)
	}

	if diff := cmp.Diff(builderRolePolicies(), mustRole(t, "Builder").Spec.Policies); diff != "" {
		t.Errorf("Builder role policies after EnsureBuilderRolePermissions() mismatch (-want +got):\n%s", diff)
	}
}

// setBuilderPolicies replaces the policies of the stored Builder role, as an
// administrator may, and clears the counted writes.
func setBuilderPolicies(t *testing.T, counting *configWritesStore, policies []any) {
	t.Helper()

	builder, err := config.Get("role/builder")
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
		t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
	}

	wantWrites := []string{"update Role/builder", "update User/alice"}
	if diff := cmp.Diff(wantWrites, counting.writes, cmpopts.SortSlices(strings.Compare)); diff != "" {
		t.Errorf("EnsureBuilderRolePermissions() writes mismatch (-want +got):\n%s", diff)
	}

	stored, err := config.Get("role/builder")
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	added := &v1.PolicySpec{Resources: []string{"builder"}, ResourceNames: nil, Verbs: []string{"use"}}

	if policies, _ := stored.Spec["policies"].([]any); len(policies) != 3 {
		t.Errorf("stored Builder role has %d policies %v, want %v and the Builder policy", len(policies), policies, made)
	} else if diff := cmp.Diff(made, policies[:2]); diff != "" {
		t.Errorf("stored Builder role policies kept mismatch (-want +got):\n%s", diff)
	}

	if policies := mustRole(t, "builder").Spec.Policies; len(policies) != 3 {
		t.Errorf("RoleFromConfig(builder) has %d policies (%s), want 3", len(policies), policiesText(policies))
	} else if diff := cmp.Diff(added, policies[2]); diff != "" {
		t.Errorf("RoleFromConfig(builder) added policy mismatch (-want +got):\n%s", diff)
	}

	user, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}

	if policies := user.Spec.Role.Policies; len(policies) != 3 {
		t.Errorf("GetUser(alice) role has %d policies (%s), want 3", len(policies), policiesText(policies))
	} else if diff := cmp.Diff(added, policies[2]); diff != "" {
		t.Errorf("GetUser(alice) added policy mismatch (-want +got):\n%s", diff)
	}

	counting.writes = nil

	if err := EnsureBuilderRolePermissions(); err != nil {
		t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Errorf("EnsureBuilderRolePermissions() again wrote %q, want none", counting.writes)
	}
}

// TestEnsureBuilderRoleKeepsWildcards leaves a Builder role that allows the
// permission through a wildcard as it is.
func TestEnsureBuilderRoleKeepsWildcards(t *testing.T) {
	counting := useDefaultConfigs(t)

	for _, tc := range []struct {
		name   string
		policy map[string]any
	}{
		{name: "builder any verb", policy: map[string]any{"resources": []any{"builder"}, "verbs": []any{"*"}}},
		{name: "any resource any verb", policy: map[string]any{"resources": []any{"*"}, "verbs": []any{"*"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setBuilderPolicies(t, counting, []any{tc.policy})

			if err := EnsureBuilderRolePermissions(); err != nil {
				t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
			}

			if len(counting.writes) != 0 {
				t.Errorf("EnsureBuilderRolePermissions() with policy %v wrote %q, want none", tc.policy, counting.writes)
			}
		})
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
		t.Fatalf("EnsureBuilderRolePermissions() returned error: %v", err)
	}

	if stored := mustRole(t, "builder"); !stored.Allowed(builderResource, builderUseVerb) {
		t.Errorf("Allowed(builder, use) of the role written back = false, want true; policies %s", policiesText(stored.Spec.Policies))
	}
}

// policiesText returns policies as text for a failure message.
func policiesText(policies []*v1.PolicySpec) string {
	parts := make([]string, 0, len(policies))

	for _, policy := range policies {
		parts = append(parts, fmt.Sprintf("%v %v %v", policy.Resources, policy.ResourceNames, policy.Verbs))
	}

	return strings.Join(parts, "; ")
}
