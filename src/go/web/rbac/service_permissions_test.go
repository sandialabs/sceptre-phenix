package rbac

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// servicePermissionChecks lists every Builder, Scorch, and Tunneler permission
// checked by the web server.
var servicePermissionChecks = []servicePermission{ //nolint:gochecknoglobals // test data
	{resource: builderResource, verb: getVerb},
	{resource: builderResource, verb: postVerb},
	{resource: builderResource, verb: putVerb},
	{resource: scorchResource, verb: getVerb},
	{resource: scorchResource, verb: postVerb},
	{resource: scorchResource, verb: "delete"},
	{resource: tunnelerResource, verb: getVerb},
}

// loadDefaultRole decodes a built-in role config shipped with phenix.
func loadDefaultRole(t *testing.T, name string) *v1.RoleSpec {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "api", "config", "default", name+".yml"))
	if err != nil {
		t.Fatalf("reading default role %s: %v", name, err)
	}

	var c store.Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		t.Fatalf("parsing default role %s: %v", name, err)
	}

	var spec v1.RoleSpec
	if err := mapstructure.Decode(c.Spec, &spec); err != nil {
		t.Fatalf("decoding default role %s: %v", name, err)
	}

	return &spec
}

// TestDefaultRolesServicePermissions pins the service access each built-in role
// grants, including the write verbs that only Global Admin gets by default.
func TestDefaultRolesServicePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role    string
		allowed []servicePermission
	}{
		{role: "global-admin", allowed: servicePermissionChecks},
		{
			role: "global-viewer",
			allowed: []servicePermission{
				{resource: builderResource, verb: getVerb},
				{resource: scorchResource, verb: getVerb},
				{resource: tunnelerResource, verb: getVerb},
			},
		},
		{role: "experiment-admin", allowed: legacyServicePermissions[experimentAdminRole]},
		{role: "experiment-user", allowed: legacyServicePermissions[experimentUserRole]},
		{role: "experiment-viewer", allowed: legacyServicePermissions[experimentViewerRole]},
		{role: "vm-admin", allowed: legacyServicePermissions[vmAdminRole]},
		{role: "vm-viewer", allowed: nil},
		{role: "disabled", allowed: nil},
	}

	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			t.Parallel()

			role := Role{Spec: loadDefaultRole(t, test.role)}

			for _, check := range servicePermissionChecks {
				want := slices.Contains(test.allowed, check)
				if got := role.Allowed(check.resource, check.verb); got != want {
					t.Errorf("%s %s:%s: got %t, want %t", test.role, check.resource, check.verb, got, want)
				}
			}
		})
	}
}

// TestDefaultRolesIncludeServiceMigration verifies fresh installs and migrated
// installs end up with the same service permissions.
func TestDefaultRolesIncludeServiceMigration(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		experimentAdminRole:  "experiment-admin",
		experimentUserRole:   "experiment-user",
		experimentViewerRole: "experiment-viewer",
		vmAdminRole:          "vm-admin",
	}

	for name := range legacyServicePermissions {
		file, ok := files[name]
		if !ok {
			t.Fatalf("no default role config for migrated role %q", name)
		}

		spec := loadDefaultRole(t, file)
		if spec.Name != name {
			t.Fatalf("default role %s has name %q, want %q", file, spec.Name, name)
		}

		if ensureServicePermissions(spec) {
			t.Errorf("default role %s is missing migrated service permissions", file)
		}
	}
}

func TestEnsurePermissionExtendsExistingPolicy(t *testing.T) {
	t.Parallel()

	role := &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{{
			Resources:     []string{builderResource},
			ResourceNames: []string{"exp-a"},
			Verbs:         []string{getVerb},
		}},
	}

	if !ensurePermission(role, servicePermission{resource: builderResource, verb: postVerb}) {
		t.Fatal("expected role to change")
	}

	if len(role.Policies) != 1 {
		t.Fatalf("expected existing policy to be extended, got %d policies", len(role.Policies))
	}

	if !slices.Equal(role.Policies[0].Verbs, []string{getVerb, postVerb}) {
		t.Fatalf("unexpected verbs: %v", role.Policies[0].Verbs)
	}
}

// initTestStore points the default store at a throw-away BoltDB file.
func initTestStore(t *testing.T) {
	t.Helper()

	previous := store.DefaultStore

	endpoint := "bolt://" + filepath.Join(t.TempDir(), "phenix.bdb")
	if err := store.Init(store.Endpoint(endpoint)); err != nil {
		t.Fatalf("initializing test store: %v", err)
	}

	t.Cleanup(func() {
		store.DefaultStore = previous //nolint:reassign // restoring test store
	})
}

func createTestRole(t *testing.T, name string, spec map[string]any) {
	t.Helper()

	c := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     spec,
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("creating role %s: %v", name, err)
	}
}

func createTestUser(t *testing.T, name string, role *v1.RoleSpec) {
	t.Helper()

	spec := &v1.UserSpec{Username: name, Role: role}
	c := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "User",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     structs.MapDefaultCase(spec, structs.CASESNAKE),
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("creating user %s: %v", name, err)
	}
}

func roleConfigSpec(spec *v1.RoleSpec) map[string]any {
	return structs.MapDefaultCase(spec, structs.CASESNAKE)
}

// withNullResourceNames writes unscoped policies as null, the way role saves
// stored them before empty resource names were omitted.
func withNullResourceNames(t *testing.T, spec map[string]any) map[string]any {
	t.Helper()

	policies, ok := spec["policies"].([]any)
	if !ok {
		t.Fatalf("unexpected policies type %T", spec["policies"])
	}

	for _, p := range policies {
		policy, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("unexpected policy type %T", p)
		}

		if _, set := policy["resourceNames"]; !set {
			policy["resourceNames"] = nil
		}
	}

	return spec
}

// legacyExperimentUser returns an Experiment User role as stored before
// service-level RBAC existed.
func legacyExperimentUser(names ...string) *v1.RoleSpec {
	return &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{{
			Resources:     []string{"experiments", "experiments/*"},
			ResourceNames: names,
			Verbs:         []string{"list", getVerb},
		}},
	}
}

func testRole(t *testing.T, name string) *Role {
	t.Helper()

	role, err := RoleFromConfig(name)
	if err != nil {
		t.Fatalf("getting role %s: %v", name, err)
	}

	return role
}

func testUserRole(t *testing.T, name string) Role {
	t.Helper()

	user, err := GetUser(name)
	if err != nil {
		t.Fatalf("getting user %s: %v", name, err)
	}

	role, err := user.Role()
	if err != nil {
		t.Fatalf("getting user %s role: %v", name, err)
	}

	return role
}

// removeResource drops every policy for a resource, as an administrator
// revoking access would.
func removeResource(spec *v1.RoleSpec, resource string) {
	spec.Policies = slices.DeleteFunc(spec.Policies, func(p *v1.PolicySpec) bool {
		return slices.Contains(p.Resources, resource)
	})
}

// TestEnsureServicePermissionsMigratesOnce verifies the startup migration
// updates legacy roles and their users once, then leaves later administrator
// changes alone.
func TestEnsureServicePermissionsMigratesOnce(t *testing.T) {
	initTestStore(t)

	custom := &v1.RoleSpec{
		Name: "Custom Role",
		Policies: []*v1.PolicySpec{{
			Resources: []string{"experiments"},
			Verbs:     []string{"list"},
		}},
	}

	createTestRole(t, "experiment-user", withNullResourceNames(t, roleConfigSpec(legacyExperimentUser())))
	createTestRole(t, "custom-role", roleConfigSpec(custom))
	createTestUser(t, "scoped-user", legacyExperimentUser("exp-a"))
	createTestUser(t, "custom-user", custom)

	if err := types.ValidateConfigSpec(*testRole(t, "experiment-user").config); err == nil {
		t.Fatal("legacy role with null resource names passed schema validation")
	}

	if err := EnsureServicePermissions(); err != nil {
		t.Fatalf("migrating service permissions: %v", err)
	}

	role := testRole(t, "experiment-user")
	if !role.config.HasAnnotation(servicePermissionsAnnotation) {
		t.Fatal("migrated role is missing the migration annotation")
	}

	// Administrators revoke access by editing the role, which validates it.
	if err := types.ValidateConfigSpec(*role.config); err != nil {
		t.Fatalf("migrated role fails schema validation: %v", err)
	}

	scoped := testUserRole(t, "scoped-user")

	for _, permission := range legacyServicePermissions[experimentUserRole] {
		if !role.Allowed(permission.resource, permission.verb) {
			t.Errorf("role missing %s:%s", permission.resource, permission.verb)
		}

		if !scoped.Allowed(permission.resource, permission.verb) {
			t.Errorf("user missing %s:%s", permission.resource, permission.verb)
		}
	}

	if !scoped.Allowed("experiments", getVerb, "exp-a") || scoped.Allowed("experiments", getVerb, "exp-b") {
		t.Error("migration changed the user's experiment scope")
	}

	customRole := testRole(t, "custom-role")
	if customRole.config.HasAnnotation(servicePermissionsAnnotation) {
		t.Error("custom role was annotated")
	}

	if testUserRole(t, "custom-user").Allowed(scorchResource, getVerb) {
		t.Error("custom role user was granted Scorch access")
	}

	// An administrator revokes Scorch access from the role and its user.
	removeResource(role.Spec, scorchResource)

	if err := role.Save(); err != nil {
		t.Fatalf("saving role: %v", err)
	}

	user, err := GetUser("scoped-user")
	if err != nil {
		t.Fatalf("getting user: %v", err)
	}

	removeResource(user.Spec.Role, scorchResource)

	if err := user.SetRole(&Role{Spec: user.Spec.Role}); err != nil {
		t.Fatalf("saving user: %v", err)
	}

	if err := EnsureServicePermissions(); err != nil {
		t.Fatalf("rerunning service permission migration: %v", err)
	}

	if testRole(t, "experiment-user").Allowed(scorchResource, getVerb) {
		t.Error("restart granted revoked Scorch access to the role")
	}

	if testUserRole(t, "scoped-user").Allowed(scorchResource, getVerb) {
		t.Error("restart granted revoked Scorch access to the user")
	}
}
