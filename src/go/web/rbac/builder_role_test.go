package rbac

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/api/config"
	"phenix/store"
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

func (s *configWritesStore) Patch(c *store.Config, data map[string]any) error {
	s.writes = append(s.writes, "patch "+c.FullName())

	return s.Store.Patch(c, data) //nolint:wrapcheck // the wrapped store's answer
}

func (s *configWritesStore) Delete(c *store.Config) error {
	s.writes = append(s.writes, "delete "+c.FullName())

	return s.Store.Delete(c) //nolint:wrapcheck // the wrapped store's answer
}

// useDefaultConfigs points the config store at a new BoltDB that holds the
// default configs, as a server start makes it, and returns the store with
// its writes counted from then on.
func useDefaultConfigs(t *testing.T) *configWritesStore {
	t.Helper()

	useBoltStore(t)

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
		{Resources: []string{"builder-drafts"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"list", "get", "update", "delete"}},
		{Resources: []string{"builder-templates"}, ResourceNames: nil, Verbs: []string{"publish"}},
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

// TestBuiltinBuilderRole asserts a new store holds the Builder role with
// every permission the Builder checks and none on the configs of accounts
// and roles, that no other built-in role but Global Admin may publish
// templates to every user, and that the start-up migration writes nothing
// to such a store.
func TestBuiltinBuilderRole(t *testing.T) {
	counting := useDefaultConfigs(t)

	role := mustRole(t, "builder")

	if role.Spec.Name != "Builder" || !reflect.DeepEqual(role.Spec.Policies, builderRolePolicies()) {
		t.Fatalf("the built-in Builder role is %q with %s", role.Spec.Name, policiesText(role.Spec.Policies))
	}

	if mustRole(t, "Builder").config.Metadata.Name != "builder" {
		t.Fatal("the role is not found by its role name")
	}

	// What the Builder's routes and the editor check, by resource name where
	// they name one: the configs it reads and publishes by their full names,
	// and the verbs alone, as a Builder route checks first.
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
		{"builder-drafts", "list", "bob/draft"},
		{"builder-drafts", "get", "bob/draft"},
		{"builder-drafts", "update", "bob/draft"},
		{"builder-drafts", "delete", "bob/draft"},
		{"builder-templates", "publish", ""},
		{"schemas", "get", "builder"},
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
	} {
		if role.Allowed(check.resource, check.verb) {
			t.Errorf("the Builder role allows %s %s", check.resource, check.verb)
		}
	}

	roles, err := GetRoles()
	if err != nil {
		t.Fatalf("GetRoles returned error: %v", err)
	}

	var publishers []string

	for _, role := range roles {
		if role.Allowed(builderTemplatesResource, builderTemplatesPublishVerb) {
			publishers = append(publishers, role.Spec.Name)
		}
	}

	slices.Sort(publishers)

	if want := []string{"Builder", "Global Admin"}; !slices.Equal(publishers, want) {
		t.Errorf("the built-in roles that may publish templates are %q, want %q", publishers, want)
	}

	if err := EnsureBuilderTemplatesPublishPermission(); err != nil {
		t.Fatalf("EnsureBuilderTemplatesPublishPermission returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Fatalf("the migration wrote %q to a new store", counting.writes)
	}
}

// TestEnsureBuilderRoleCreatesMissingRole asserts a store that was
// initialized before the Builder role was shipped, and so holds no role of
// that name, gets the built-in one at start.
func TestEnsureBuilderRoleCreatesMissingRole(t *testing.T) {
	counting := useDefaultConfigs(t)

	if err := config.Delete("role/builder"); err != nil {
		t.Fatalf("deleting the Builder role: %v", err)
	}

	counting.writes = nil

	for range 2 {
		if err := EnsureBuilderTemplatesPublishPermission(); err != nil {
			t.Fatalf("EnsureBuilderTemplatesPublishPermission returned error: %v", err)
		}
	}

	if want := []string{"create Role/builder"}; !slices.Equal(counting.writes, want) {
		t.Fatalf("the migration wrote %q, want %q", counting.writes, want)
	}

	if role := mustRole(t, "Builder"); !reflect.DeepEqual(role.Spec.Policies, builderRolePolicies()) {
		t.Fatalf("the Builder role made at start has %s", policiesText(role.Spec.Policies))
	}
}

// seedBuilderRoles makes the store hold what an administrator made before
// the Builder role was shipped: a Builder role of its own whose second policy
// has no resource names, a second role whose role name is Builder, and the
// role of a topology designer; alice and carol are assigned the Builder role
// and bob the designer's. It returns the policies of the first, as stored.
func seedBuilderRoles(t *testing.T) []any {
	t.Helper()

	made := []any{
		map[string]any{"resources": []any{"configs"}, "resourceNames": []any{"*", "*/*"}, "verbs": []any{"list", "get"}},
		map[string]any{"resources": []any{"disks"}, "verbs": []any{"list"}},
	}

	builder, err := config.Get("role/builder", false)
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	builder.Spec = map[string]any{"roleName": "Builder", "policies": made}

	if err := store.Update(builder); err != nil {
		t.Fatalf("replacing the Builder role: %v", err)
	}

	for name, spec := range map[string]map[string]any{
		"workshop": {"roleName": "Builder", "policies": []any{
			map[string]any{"resources": []any{"configs"}, "verbs": []any{"list"}},
		}},
		"designer": {"roleName": "Topology Designer", "policies": []any{
			map[string]any{"resources": []any{"configs"}, "verbs": []any{"list"}},
		}},
	} {
		if err := store.Create(&store.Config{
			Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: store.ConfigMetadata{Name: name}, Spec: spec,
		}); err != nil {
			t.Fatalf("creating role %s: %v", name, err)
		}
	}

	for user, role := range map[string]string{"alice": "builder", "bob": "designer", "carol": "Builder"} {
		created, err := NewUser(user, "Testpass1!", user, "Tester")
		if err != nil {
			t.Fatalf("NewUser(%s) returned error: %v", user, err)
		}

		if err := created.SetRole(mustRole(t, role)); err != nil {
			t.Fatalf("assigning %s: %v", user, err)
		}

		if err := created.AddToken("token-"+user, "signed in"); err != nil {
			t.Fatalf("AddToken returned error: %v", err)
		}
	}

	return made
}

// TestEnsureBuilderTemplatesPublishPermission asserts a Builder role an
// administrator made, by name or by role name, gains the permission to
// publish templates and keeps everything else, that so do the users
// assigned it, that other roles and users are left alone, and that running
// again writes nothing.
func TestEnsureBuilderTemplatesPublishPermission(t *testing.T) {
	counting := useDefaultConfigs(t)
	made := seedBuilderRoles(t)

	designer, err := config.Get("role/designer", false)
	if err != nil {
		t.Fatalf("getting the designer role: %v", err)
	}

	bob, err := GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser(bob) returned error: %v", err)
	}

	counting.writes = nil

	if err := EnsureBuilderTemplatesPublishPermission(); err != nil {
		t.Fatalf("EnsureBuilderTemplatesPublishPermission returned error: %v", err)
	}

	if want := []string{"update Role/builder", "update Role/workshop", "update User/alice", "update User/carol"}; !sameWrites(
		counting.writes, want,
	) {
		t.Fatalf("the migration wrote %q, want %q", counting.writes, want)
	}

	// The role keeps its policies as stored, and gains the one.
	stored, err := config.Get("role/builder", false)
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	policies, _ := stored.Spec["policies"].([]any)
	if len(policies) != 3 || !reflect.DeepEqual(policies[:2], made) {
		t.Fatalf("the Builder role's policies are %v, want %v and one more", policies, made)
	}

	publish := &v1.PolicySpec{Resources: []string{"builder-templates"}, ResourceNames: nil, Verbs: []string{"publish"}}

	for _, name := range []string{"builder", "workshop"} {
		role := mustRole(t, name)

		if !reflect.DeepEqual(role.Spec.Policies[len(role.Spec.Policies)-1], publish) {
			t.Errorf("role %s ends with %s, want the publish policy", name, policiesText(role.Spec.Policies))
		}
	}

	for _, name := range []string{"alice", "carol"} {
		user, err := GetUser(name)
		if err != nil {
			t.Fatalf("GetUser(%s) returned error: %v", name, err)
		}

		policies := user.Spec.Role.Policies

		if user.Spec.Role.Name != "Builder" || len(policies) != 3 || !reflect.DeepEqual(policies[2], publish) ||
			!slices.Equal(policies[0].Verbs, []string{"list", "get"}) || len(user.Spec.Tokens) != 1 {
			t.Errorf("user %s after the migration: %s, %d tokens", name, policiesText(policies), len(user.Spec.Tokens))
		}
	}

	// The designer's role and its user are left alone.
	if after, err := config.Get("role/designer", false); err != nil || !reflect.DeepEqual(after.Spec, designer.Spec) {
		t.Errorf("the designer role changed: %v", err)
	}

	if after, err := GetUser("bob"); err != nil || !reflect.DeepEqual(after.Spec, bob.Spec) {
		t.Errorf("bob changed: %v", err)
	}

	// Running again changes nothing.
	counting.writes = nil

	if err := EnsureBuilderTemplatesPublishPermission(); err != nil {
		t.Fatalf("EnsureBuilderTemplatesPublishPermission returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Fatalf("running the migration again wrote %q", counting.writes)
	}
}

// TestEnsureBuilderTemplatesPublishKeepsWildcards asserts a Builder role
// that allows publishing through a wildcard is left as it is.
func TestEnsureBuilderTemplatesPublishKeepsWildcards(t *testing.T) {
	counting := useDefaultConfigs(t)

	builder, err := config.Get("role/builder", false)
	if err != nil {
		t.Fatalf("getting the Builder role: %v", err)
	}

	builder.Spec = map[string]any{"roleName": "Builder", "policies": []any{
		map[string]any{"resources": []any{"builder-*"}, "verbs": []any{"*"}},
	}}

	if err := store.Update(builder); err != nil {
		t.Fatalf("replacing the Builder role: %v", err)
	}

	counting.writes = nil

	if err := EnsureBuilderTemplatesPublishPermission(); err != nil {
		t.Fatalf("EnsureBuilderTemplatesPublishPermission returned error: %v", err)
	}

	if len(counting.writes) != 0 {
		t.Fatalf("the migration wrote %q to a role that allows publishing", counting.writes)
	}
}

// TestEnsureBuilderTemplatesPublishOtherShape asserts a role whose stored
// policies are not in the shape the store decodes is written back from its
// decoded policies, with the permission added.
func TestEnsureBuilderTemplatesPublishOtherShape(t *testing.T) {
	useBoltStore(t)

	c := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: "builder"},
		Spec:     map[string]any{"roleName": "Builder", "policies": []any{}},
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("creating the role: %v", err)
	}

	role := &Role{
		Spec: &v1.RoleSpec{Name: "Builder", Policies: []*v1.PolicySpec{
			{Resources: []string{"configs"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
		}},
		config: c,
	}

	c.Spec["policies"] = []map[string]any{{"resources": []string{"configs"}, "resourceNames": []string{"*"}, "verbs": []string{"list"}}}

	if err := role.ensureBuilderTemplatesPublish(); err != nil {
		t.Fatalf("ensureBuilderTemplatesPublish returned error: %v", err)
	}

	stored := mustRole(t, "builder")

	if len(stored.Spec.Policies) != 2 || !stored.Allowed("configs", "list", "x") ||
		!stored.Allowed(builderTemplatesResource, builderTemplatesPublishVerb) {
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
