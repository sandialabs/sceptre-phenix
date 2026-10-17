package rbac

import (
	"reflect"
	"strings"
	"testing"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// useStore gives the test a store of its own holding the given roles and
// users.
func useStore(t *testing.T, roles []*v1.RoleSpec, users []*v1.UserSpec) {
	t.Helper()

	storetest.Use(t)

	create := func(name string, spec any) {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatal(err)
		}

		c.Spec = structs.MapDefaultCase(spec, structs.CASESNAKE)

		if err := store.Create(c); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	for _, role := range roles {
		create("role/"+strings.ReplaceAll(strings.ToLower(role.Name), " ", "-"), role)
	}

	for _, user := range users {
		create("user/"+user.Username, user)
	}
}

// storedPolicies reads back the policies of every stored role and user role.
func storedPolicies(t *testing.T) map[string][]*v1.PolicySpec {
	t.Helper()

	roles, err := GetRoles()
	if err != nil {
		t.Fatal(err)
	}

	users, err := GetUsers()
	if err != nil {
		t.Fatal(err)
	}

	policies := make(map[string][]*v1.PolicySpec)

	for _, role := range roles {
		policies["role "+role.Spec.Name] = role.Spec.Policies
	}

	for _, user := range users {
		if user.Spec.Role != nil {
			policies["user "+user.Username()] = user.Spec.Role.Policies
		}
	}

	return policies
}

func policy(resource string, names []string, verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{resource}, ResourceNames: names, Verbs: verbs}
}

func role(name string, policies ...*v1.PolicySpec) *v1.RoleSpec {
	return &v1.RoleSpec{Name: name, Policies: policies}
}

// checkMigration runs the migrations twice and checks after each pass that the
// stored roles and user roles are exactly want.
func checkMigration(t *testing.T, want map[string][]*v1.PolicySpec, migrations ...func() error) {
	t.Helper()

	for _, pass := range []string{"first", "repeated"} {
		for _, migrate := range migrations {
			if err := migrate(); err != nil {
				t.Fatalf("%s migration: %v", pass, err)
			}
		}

		got := storedPolicies(t)

		for name, policies := range want {
			if !reflect.DeepEqual(got[name], policies) {
				t.Errorf("%s migration: %s policies = %s, want %s", pass, name, describe(got[name]), describe(policies))
			}
		}

		if len(got) != len(want) {
			t.Errorf("%s migration: %d roles and user roles stored, want %d", pass, len(got), len(want))
		}
	}
}

// The experiment files migrations let Experiment Admin/User roles and their
// users upload files, and only Admins delete, keeping a user's experiment scope.
//
//nolint:paralleltest // replaces the store
func TestEnsureExperimentFilesPermissions(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		vms         = policy("vms", []string{"vm-a"}, "get")
		scopedAB    = policy("experiments", []string{"exp-a", "exp-b"}, "get")
		scopedA     = policy("experiments", []string{"exp-a"}, "get")
	)

	user := func(name string, role *v1.RoleSpec) *v1.UserSpec {
		return &v1.UserSpec{Username: name, Role: role}
	}

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentAdminRole, experiments),
			// a files policy the role already has gains the verb
			role(experimentUserRole, experiments, policy(experimentFilesResource, nil, "get")),
			role("Experiment Viewer", experiments),
			role("VM Admin", vms),
		},
		[]*v1.UserSpec{
			user("admin", role(experimentAdminRole, experiments)),
			user("scoped-admin", role(experimentAdminRole,
				scopedA, policy(experimentFilesResource, []string{"exp-a"}, "create"))),
			// assigned before every unnamed policy was scoped
			user("unscoped-admin", role(experimentAdminRole,
				scopedA, policy(experimentFilesResource, nil, "create", "delete"))),
			user("scoped-user", role(experimentUserRole,
				scopedAB, policy(experimentFilesResource, []string{"exp-a"}, "get"))),
			// only an experiments policy scopes the new permission
			user("vm-user", role(experimentUserRole, vms)),
			user("viewer", role("Experiment Viewer", experiments)),
			user("no-role", nil),
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment Admin": {
			experiments, policy(experimentFilesResource, nil, "create", "delete"),
		},
		"role Experiment User": {
			experiments, policy(experimentFilesResource, nil, "get", "create"),
		},
		"role Experiment Viewer": {experiments},
		"role VM Admin":          {vms},
		"user admin": {
			experiments, policy(experimentFilesResource, nil, "create", "delete"),
		},
		"user scoped-admin": {
			scopedA, policy(experimentFilesResource, []string{"exp-a"}, "create", "delete"),
		},
		"user unscoped-admin": {
			scopedA, policy(experimentFilesResource, []string{"exp-a"}, "create", "delete"),
		},
		"user scoped-user": {
			scopedAB, policy(experimentFilesResource, []string{"exp-a", "exp-b"}, "get", "create"),
		},
		"user vm-user": {vms, policy(experimentFilesResource, nil, "create")},
		"user viewer":  {experiments},
	}

	checkMigration(t, want, EnsureExperimentFilesCreatePermission, EnsureExperimentFilesDeletePermission)
}

// The jobs migration lets Experiment Admin roles and their users act on others'
// jobs of their experiments.
//
//nolint:paralleltest // replaces the store
func TestEnsureJobsPermission(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		scopedA     = policy("experiments", []string{"exp-a"}, "get")
		jobVerbs    = []string{"list", "get", "update", "delete"}
	)

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentAdminRole, experiments),
			role(experimentUserRole, experiments),
		},
		[]*v1.UserSpec{
			{Username: "scoped-admin", Role: role(experimentAdminRole, scopedA)},
			// assigned before every unnamed policy was scoped
			{Username: "unscoped-admin", Role: role(experimentAdminRole, scopedA, policy(jobsResource, nil, jobVerbs...))},
			{Username: "user", Role: role(experimentUserRole, scopedA)},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment Admin": {experiments, policy(jobsResource, nil, jobVerbs...)},
		"role Experiment User":  {experiments},
		"user scoped-admin":     {scopedA, policy(jobsResource, []string{"exp-a"}, jobVerbs...)},
		"user unscoped-admin":   {scopedA, policy(jobsResource, []string{"exp-a"}, jobVerbs...)},
		"user user":             {scopedA},
	}

	checkMigration(t, want, EnsureJobsPermission)
}

// The subnet capture migration lets roles that may start subnet captures stop
// them on the same names.
//
//nolint:paralleltest // replaces the store
func TestEnsureCaptureSubnetStopPermission(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		start       = policy(captureSubnetResource, nil, "create")
		startA      = &v1.PolicySpec{
			Resources: []string{captureSubnetResource, "vms/captures"}, ResourceNames: []string{"exp-a"},
			Verbs: []string{"get", "create"},
		}
		startStop = policy(captureSubnetResource, nil, "create", "delete")
		anything  = policy(captureSubnetResource, nil, "*")
		// starts them through a pattern, as Role.Allowed matches it
		startAll  = policy("exp/*", nil, "create", "get")
		startSome = &v1.PolicySpec{
			Resources: []string{"exp/capture*"}, ResourceNames: []string{"exp-b"}, Verbs: []string{"create"},
		}
		stopAll   = policy("*/*", nil, "delete")
		otherExps = policy("experiments/*", nil, "create")
	)

	useStore(t,
		[]*v1.RoleSpec{
			role("Capturer", experiments, start),
			role("Stopper", startStop),
			role("Anything", anything),
			role("Viewer", experiments),
			role("Patterned", startAll),
			role("Patterned Stopper", startAll, stopAll),
			role("Other", otherExps),
		},
		[]*v1.UserSpec{
			{Username: "scoped", Role: role("Capturer", startA)},
			{Username: "viewer", Role: role("Viewer", experiments)},
			{Username: "patterned", Role: role("Patterned", startSome)},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Capturer": {experiments, start, policy(captureSubnetResource, nil, "delete")},
		"role Stopper":  {startStop},
		"role Anything": {anything},
		"role Viewer":   {experiments},
		"user scoped":   {startA, policy(captureSubnetResource, []string{"exp-a"}, "delete")},
		"user viewer":   {experiments},

		"role Patterned":         {startAll, policy(captureSubnetResource, nil, "delete")},
		"role Patterned Stopper": {startAll, stopAll},
		"role Other":             {otherExps},
		"user patterned":         {startSome, policy(captureSubnetResource, []string{"exp-b"}, "delete")},
	}

	checkMigration(t, want, EnsureCaptureSubnetStopPermission)
}

// The Experiment Viewer migration scopes a user's unnamed vms/mount policy to
// the names of their VM policy.
//
//nolint:paralleltest // replaces the store
func TestEnsureExperimentViewerMountScope(t *testing.T) {
	var (
		scopedA   = []string{"exp-a", "exp-a/*"}
		resources = []string{"experiments", "experiments/*", "vms", "vms/*"}
		viewing   = func(names []string) *v1.PolicySpec {
			return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: []string{"list", "get"}}
		}
		hosts = policy("hosts", []string{"*"}, "list")
		mount = func(names []string) *v1.PolicySpec { return policy(mountResource, names, "post", "delete") }
	)

	useStore(t,
		[]*v1.RoleSpec{role(experimentViewerRole, viewing(nil), hosts, mount(nil))},
		[]*v1.UserSpec{
			{Username: "legacy", Role: role(experimentViewerRole, viewing(scopedA), hosts, mount(nil))},
			{Username: "scoped", Role: role(experimentViewerRole, viewing(scopedA), hosts, mount([]string{"exp-b/*"}))},
			{Username: "unnamed", Role: role(experimentViewerRole, viewing(nil), mount(nil))},
			{Username: "other", Role: role(experimentUserRole, viewing(scopedA), hosts, mount(nil))},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment Viewer": {viewing(nil), hosts, mount(nil)},
		"user legacy":            {viewing(scopedA), hosts, mount(scopedA)},
		"user scoped":            {viewing(scopedA), hosts, mount([]string{"exp-b/*"})},
		"user unnamed":           {viewing(nil), mount(nil)},
		"user other":             {viewing(scopedA), hosts, mount(nil)},
	}

	checkMigration(t, want, EnsureExperimentViewerMountScope)

	user, err := GetUser("legacy")
	if err != nil {
		t.Fatal(err)
	}

	if r, _ := user.Role(); !r.Allowed(mountResource, "post", "exp-a/vm") || r.Allowed(mountResource, "post", "exp-b/vm") {
		t.Error("the migrated user may not mount exactly the VMs of exp-a")
	}
}

func describe(policies []*v1.PolicySpec) string {
	out := make([]string, len(policies))
	for i, p := range policies {
		out[i] = strings.Join(p.Resources, ",") + " " + strings.Join(p.ResourceNames, ",") + " " + strings.Join(p.Verbs, ",")
	}

	return "[" + strings.Join(out, "; ") + "]"
}

// The null resource names migration removes stored resourceNames: null from
// role and user policies, keeping the rest of the config.
//
//nolint:paralleltest // replaces the store
func TestDropNullResourceNames(t *testing.T) {
	storetest.Use(t)

	policies := func() []any {
		return []any{
			map[string]any{"resources": []any{"vms"}, "resourceNames": nil, "verbs": []any{"get"}},
			map[string]any{"resources": []any{"hosts"}, "resourceNames": []any{"*"}, "verbs": []any{"list"}},
		}
	}

	stored := map[string]map[string]any{
		"role/legacy": {"roleName": "Legacy", "policies": policies()},
		"user/legacy": {
			"username": "legacy",
			"password": "hash",
			"rbac":     map[string]any{"roleName": "Legacy", "policies": policies()},
		},
	}

	for name, spec := range stored {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatal(err)
		}

		c.Spec = spec

		if err := store.Create(c); err != nil {
			t.Fatal(err)
		}
	}

	for _, pass := range []string{"first", "repeated"} {
		if err := DropNullResourceNames(); err != nil {
			t.Fatalf("%s migration: %v", pass, err)
		}

		for name := range stored {
			c, _ := store.NewConfig(name)
			if err := store.Get(c); err != nil {
				t.Fatal(err)
			}

			list := c.Spec["policies"]
			if c.Kind == "User" {
				if c.Spec["password"] != "hash" {
					t.Errorf("%s migration: %s password = %v, want it kept", pass, name, c.Spec["password"])
				}

				role, _ := c.Spec["rbac"].(map[string]any)
				list = role["policies"]
			}

			got, _ := list.([]any)
			if len(got) != 2 {
				t.Fatalf("%s migration: %s policies = %#v, want two", pass, name, list)
			}

			if p, _ := got[0].(map[string]any); p == nil {
				t.Errorf("%s migration: %s policy = %#v, want a map", pass, name, got[0])
			} else if names, set := p["resourceNames"]; set {
				t.Errorf("%s migration: %s unnamed policy resourceNames = %#v, want it left out", pass, name, names)
			}

			if p, _ := got[1].(map[string]any); !reflect.DeepEqual(p["resourceNames"], []any{"*"}) {
				t.Errorf("%s migration: %s named policy = %#v, want its names kept", pass, name, got[1])
			}
		}
	}

	c, _ := store.NewConfig("role/legacy")
	if err := store.Get(c); err != nil {
		t.Fatal(err)
	}

	if err := types.ValidateConfigSpec(*c); err != nil {
		t.Errorf("migrated role fails its schema: %v", err)
	}
}
