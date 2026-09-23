package rbac

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	{resource: scorchResource, verb: deleteVerb},
	{resource: tunnelerResource, verb: getVerb},
}

// loadDefaultRoleConfig reads a built-in role config shipped with phenix.
func loadDefaultRoleConfig(t *testing.T, name string) store.Config {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "api", "config", "default", name+".yml"))
	if err != nil {
		t.Fatalf("reading default role %s: %v", name, err)
	}

	var c store.Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		t.Fatalf("parsing default role %s: %v", name, err)
	}

	return c
}

// loadDefaultRole decodes a built-in role config shipped with phenix.
func loadDefaultRole(t *testing.T, name string) *v1.RoleSpec {
	t.Helper()

	c := loadDefaultRoleConfig(t, name)

	var spec v1.RoleSpec
	if err := mapstructure.Decode(c.Spec, &spec); err != nil {
		t.Fatalf("decoding default role %s: %v", name, err)
	}

	return &spec
}

// assignDefaultRole returns a built-in role as the Users page assigns it to a
// user with the given resource names.
func assignDefaultRole(t *testing.T, name string, names ...string) Role {
	t.Helper()

	role := &Role{Spec: loadDefaultRole(t, name)}

	// Like the Users page, ignore the error returned for roles whose policies
	// already name their resources.
	_ = role.SetResourceNames(names...)

	return *role
}

func permissions(verbs map[string][]string) []servicePermission {
	var perms []servicePermission

	for resource, vs := range verbs {
		for _, verb := range vs {
			perms = append(perms, servicePermission{resource: resource, verb: verb})
		}
	}

	return perms
}

// TestDefaultRolesServicePermissions pins the service access each built-in role
// grants.
func TestDefaultRolesServicePermissions(t *testing.T) {
	t.Parallel()

	var (
		all         = []string{getVerb, postVerb, putVerb}
		scorchAll   = []string{getVerb, postVerb, deleteVerb}
		readService = map[string][]string{
			builderResource:  {getVerb},
			scorchResource:   {getVerb},
			tunnelerResource: {getVerb},
		}
	)

	tests := []struct {
		role    string
		allowed []servicePermission
	}{
		{role: "global-admin", allowed: servicePermissionChecks},
		{role: "global-viewer", allowed: permissions(readService)},
		{
			role: "experiment-admin",
			allowed: permissions(map[string][]string{
				builderResource: all, scorchResource: scorchAll, tunnelerResource: {getVerb},
			}),
		},
		{
			role: "experiment-user",
			allowed: permissions(map[string][]string{
				builderResource:  {getVerb, postVerb},
				scorchResource:   scorchAll,
				tunnelerResource: {getVerb},
			}),
		},
		{role: "experiment-viewer", allowed: permissions(readService)},
		{
			role:    "vm-admin",
			allowed: permissions(map[string][]string{scorchResource: scorchAll, tunnelerResource: {getVerb}}),
		},
		{role: "vm-viewer", allowed: permissions(map[string][]string{builderResource: {getVerb}})},
		{
			role:    "scorch-viewer",
			allowed: permissions(map[string][]string{builderResource: {getVerb}, scorchResource: {getVerb}}),
		},
		{role: "scorch-admin", allowed: permissions(map[string][]string{scorchResource: scorchAll})},
		{role: "builder", allowed: permissions(map[string][]string{builderResource: all})},
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

// TestDefaultRolesValidate verifies every built-in role config passes the Role
// schema, as it must for administrators to edit it.
func TestDefaultRolesValidate(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("..", "..", "api", "config", "default", "*.yml"))
	if err != nil {
		t.Fatalf("listing default configs: %v", err)
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".yml")

		c := loadDefaultRoleConfig(t, name)
		if c.Kind != "Role" {
			continue
		}

		if err := types.ValidateConfigSpec(c); err != nil {
			t.Errorf("default role %s fails schema validation: %v", name, err)
		}
	}
}

// TestAssignedDefaultRolesScope checks the new and changed built-in roles after
// the Users page assigns them to a user of experiment exp-a.
func TestAssignedDefaultRolesScope(t *testing.T) {
	t.Parallel()

	type check struct {
		resource, verb, name string
		want                 bool
	}

	tests := map[string][]check{
		"experiment-user": {
			{"vms/forwards", "create", "exp-a/vm1", true},
			{"vms/forwards", "delete", "exp-a/vm1", true},
			{"vms/forwards", "create", "exp-b/vm1", false},
			{"experiments", "get", "exp-b", false},
		},
		"experiment-admin": {
			{"vms/forwards", "create", "exp-a/vm1", true},
			{"vms/forwards", "create", "exp-b/vm1", false},
		},
		"vm-admin": {
			{"vms/forwards", "create", "exp-a/vm1", true},
			{"vms/forwards", "create", "exp-b/vm1", false},
		},
		"scorch-viewer": {
			{"experiments", "get", "exp-a", true},
			{"experiments", "get", "exp-b", false},
			{"experiments/apps", "get", "exp-a", true},
			{"experiments/files", "get", "exp-a", true},
			{"experiments/files", "create", "exp-a", false},
			{"experiments/start", "update", "exp-a", false},
			{"vms", "list", "exp-a/vm1", false},
		},
		"scorch-admin": {
			{"experiments", "get", "exp-a", true},
			{"experiments", "get", "exp-b", false},
			{"experiments/files", "list", "exp-a", true},
			{"vms", "list", "exp-a/vm1", true},
			{"vms/screenshot", "get", "exp-a/vm1", true},
			{"vms/vnc", "get", "exp-a/vm1", false},
			{"vms", "update", "exp-a/vm1", false},
			{"experiments/trigger", "create", "exp-a", false},
		},
		// Builder ignores the user's experiments, and must never reach User or
		// Role configs.
		"builder": {
			{"configs", "list", "Topology/topo", true},
			{"configs", "create", "Topology/topo", true},
			{"configs", "update", "Scenario/scn", true},
			{"configs", "get", "Experiment/exp-b", true},
			{"configs", "create", "Image/img", true},
			{"configs", "delete", "Topology/topo", false},
			{"configs", "create", "User/admin", false},
			{"configs", "update", "User/admin", false},
			{"configs", "create", "Role/global-admin", false},
			{"configs", "get", "User/admin", false},
			{"experiments", "list", "exp-b", true},
			{"experiments", "update", "exp-b", true},
			{"experiments", "delete", "exp-b", false},
			{"experiments/start", "update", "exp-b", false},
			{"topologies", "list", "topo", true},
			{"scenarios", "list", "scn", true},
			{"disks", "list", "disk.qc2", true},
			{"disks", "get", "disk.qc2", true},
			{"disks", "delete", "disk.qc2", false},
			{"schemas", "get", "Topology", true},
			{"vms", "list", "exp-b/vm1", false},
			{"users", "list", "admin", false},
		},
	}

	for name, checks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			role := assignDefaultRole(t, name, "exp-a", "exp-a/*")

			for _, c := range checks {
				if got := role.Allowed(c.resource, c.verb, c.name); got != c.want {
					t.Errorf("%s %s:%s %s: got %t, want %t", name, c.resource, c.verb, c.name, got, c.want)
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
		vmViewerRole:         "vm-viewer",
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

// TestEnsurePermissionSkipsGrantedPermission verifies wildcard grants, such as
// Global Viewer's, are not duplicated.
func TestEnsurePermissionSkipsGrantedPermission(t *testing.T) {
	t.Parallel()

	role := &v1.RoleSpec{
		Name: "Global Viewer",
		Policies: []*v1.PolicySpec{{
			Resources:     []string{"*", "*/*"},
			ResourceNames: []string{"*", "*/*"},
			Verbs:         []string{"list", getVerb},
		}},
	}

	if ensurePermission(role, servicePermission{resource: builderResource, verb: getVerb}) {
		t.Fatal("granted permission was added again")
	}

	if len(role.Policies) != 1 {
		t.Fatalf("expected one policy, got %d", len(role.Policies))
	}
}

// TestEnsureServicePermissionsScopesForwards verifies a user's new port
// forward permissions keep the scope of the user's VM policy.
func TestEnsureServicePermissionsScopesForwards(t *testing.T) {
	t.Parallel()

	spec := &v1.RoleSpec{
		Name: experimentUserRole,
		Policies: []*v1.PolicySpec{
			{Resources: []string{"experiments", "experiments/*"}, ResourceNames: []string{"exp-a"}, Verbs: []string{"list", getVerb}},
			{Resources: []string{"vms", "vms/*"}, ResourceNames: []string{"exp-a/*"}, Verbs: []string{"list", getVerb, "patch"}},
			{Resources: []string{"hosts"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
		},
	}

	if !ensureServicePermissions(spec) {
		t.Fatal("expected role to change")
	}

	role := Role{Spec: spec}

	checks := []struct {
		verb, name string
		want       bool
	}{
		{createVerb, "exp-a/vm1", true},
		{deleteVerb, "exp-a/vm1", true},
		{createVerb, "exp-b/vm1", false},
	}

	for _, c := range checks {
		if got := role.Allowed(forwardsResource, c.verb, c.name); got != c.want {
			t.Errorf("vms/forwards:%s %s: got %t, want %t", c.verb, c.name, got, c.want)
		}
	}

	if role.Allowed("vms/redeploy", createVerb, "exp-a/vm1") {
		t.Error("forward permissions leaked to other VM resources")
	}

	if !role.Allowed(scorchResource, postVerb) {
		t.Error("missing Scorch control")
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
