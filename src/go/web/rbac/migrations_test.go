package rbac

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

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
			t.Fatalf("NewConfig(%s) error = %v", name, err)
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
		t.Fatalf("GetRoles() error = %v", err)
	}

	users, err := GetUsers()
	if err != nil {
		t.Fatalf("GetUsers() error = %v", err)
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

// migrate runs the migrations and returns the policies stored after them.
func migrate(t *testing.T, migrations ...func() error) map[string][]*v1.PolicySpec {
	t.Helper()

	for _, m := range migrations {
		if err := m(); err != nil {
			t.Fatalf("migrating: %v", err)
		}
	}

	return storedPolicies(t)
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

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureExperimentFilesCreatePermission, EnsureExperimentFilesDeletePermission)); diff != "" {
			t.Errorf("EnsureExperimentFiles{Create,Delete}Permission() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}
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

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureJobsPermission)); diff != "" {
			t.Errorf("EnsureJobsPermission() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}
}

// The VLANs migration lets the experiment roles and their users list and get
// the VLANs of their experiments, and leaves other roles as they are.
//
//nolint:paralleltest // replaces the store
func TestEnsureVLANsPermission(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		scopedA     = policy("experiments", []string{"exp-a"}, "get")
		vlans       = policy(vlansResource, nil, "list", "get")
		vmViewer    = policy("vms", nil, "list")
	)

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentAdminRole, experiments),
			role(experimentUserRole, experiments),
			role(experimentViewerRole, experiments),
			role("VM Viewer", vmViewer),
		},
		[]*v1.UserSpec{
			{Username: "scoped-viewer", Role: role(experimentViewerRole, scopedA)},
			// a policy that lists VLANs already gets the missing verb and names
			{Username: "partial-user", Role: role(experimentUserRole, scopedA, policy(vlansResource, nil, "list"))},
			{Username: "vm-viewer", Role: role("VM Viewer", vmViewer)},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment Admin":  {experiments, vlans},
		"role Experiment User":   {experiments, vlans},
		"role Experiment Viewer": {experiments, vlans},
		"role VM Viewer":         {vmViewer},
		"user scoped-viewer":     {scopedA, policy(vlansResource, []string{"exp-a"}, "list", "get")},
		"user partial-user":      {scopedA, policy(vlansResource, []string{"exp-a"}, "list", "get")},
		"user vm-viewer":         {vmViewer},
	}

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureVLANsPermission)); diff != "" {
			t.Errorf("EnsureVLANsPermission() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}
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

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureCaptureSubnetStopPermission)); diff != "" {
			t.Errorf("EnsureCaptureSubnetStopPermission() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}
}

// The screenshot migration lets the roles and users that may create VM
// snapshots save VM screenshots on the same names.
//
//nolint:paralleltest // replaces the store
func TestEnsureVMScreenshotPermission(t *testing.T) {
	var (
		experiments = policy("experiments", nil, "list", "get")
		snapshots   = policy(snapshotsResource, nil, "list", "create", "update")
		scoped      = policy(snapshotsResource, []string{"exp-a/*"}, "create")
		listOnly    = policy(snapshotsResource, nil, "list")
		allVMs      = policy("vms/*", nil, "*")
		vmsCreate   = policy("vms/*", []string{"exp-b/*"}, "create")
		screenshot  = func(names []string) *v1.PolicySpec { return policy(screenshotResource, names, "create") }
	)

	useStore(t,
		[]*v1.RoleSpec{
			role(experimentUserRole, experiments, snapshots),
			role("VM Admin", experiments, allVMs),
			role("Lister", listOnly),
			role("Saver", snapshots, screenshot(nil)),
		},
		[]*v1.UserSpec{
			{Username: "scoped-user", Role: role(experimentUserRole, scoped)},
			{Username: "creator", Role: role("Creator", vmsCreate)},
			{Username: "viewer", Role: role("Viewer", experiments)},
		},
	)

	want := map[string][]*v1.PolicySpec{
		"role Experiment User": {experiments, snapshots, screenshot(nil)},
		"role VM Admin":        {experiments, allVMs},
		"role Lister":          {listOnly},
		"role Saver":           {snapshots, screenshot(nil)},
		"user scoped-user":     {scoped, screenshot([]string{"exp-a/*"})},
		"user creator":         {vmsCreate},
		"user viewer":          {experiments},
	}

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureVMScreenshotPermission)); diff != "" {
			t.Errorf("EnsureVMScreenshotPermission() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}
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

	for _, pass := range []string{"first", "repeated"} {
		if diff := cmp.Diff(want, migrate(t, EnsureExperimentViewerMountScope)); diff != "" {
			t.Errorf("EnsureExperimentViewerMountScope() %s pass: stored policies mismatch (-want +got):\n%s", pass, diff)
		}
	}

	user, err := GetUser("legacy")
	if err != nil {
		t.Fatalf("GetUser(legacy) error = %v", err)
	}

	r, err := user.Role()
	if err != nil {
		t.Fatalf("Role() of the migrated user error = %v", err)
	}

	if !r.Allowed(mountResource, "post", "exp-a/vm") {
		t.Errorf("Allowed(%s, post, exp-a/vm) of the migrated user = false, want true", mountResource)
	}

	if r.Allowed(mountResource, "post", "exp-b/vm") {
		t.Errorf("Allowed(%s, post, exp-b/vm) of the migrated user = true, want false", mountResource)
	}
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
			t.Fatalf("NewConfig(%s) error = %v", name, err)
		}

		c.Spec = spec

		if err := store.Create(c); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	unnamed := map[string]any{"resources": []any{"vms"}, "verbs": []any{"get"}}
	named := map[string]any{"resources": []any{"hosts"}, "resourceNames": []any{"*"}, "verbs": []any{"list"}}
	want := map[string]map[string]any{
		"role/legacy": {"roleName": "Legacy", "policies": []any{unnamed, named}},
		"user/legacy": {
			"username": "legacy",
			"password": "hash",
			"rbac":     map[string]any{"roleName": "Legacy", "policies": []any{unnamed, named}},
		},
	}

	for _, pass := range []string{"first", "repeated"} {
		if err := DropNullResourceNames(); err != nil {
			t.Fatalf("DropNullResourceNames() %s pass error = %v", pass, err)
		}

		got := make(map[string]map[string]any, len(stored))

		for name := range stored {
			c, _ := store.NewConfig(name)
			if err := store.Get(c); err != nil {
				t.Fatalf("getting %s: %v", name, err)
			}

			got[name] = c.Spec
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("DropNullResourceNames() %s pass: stored specs mismatch (-want +got):\n%s", pass, diff)
		}
	}

	c, _ := store.NewConfig("role/legacy")
	if err := store.Get(c); err != nil {
		t.Fatalf("getting role/legacy: %v", err)
	}

	if err := types.ValidateConfigSpec(*c); err != nil {
		t.Errorf("ValidateConfigSpec(migrated role/legacy) error = %v", err)
	}
}

// genMigrationsRole draws a role of policies on the resources given, with
// names that are not negated.
func genMigrationsRole(resources ...string) *rapid.Generator[*v1.RoleSpec] {
	p := rapid.Custom(func(t *rapid.T) *v1.PolicySpec {
		var names []string
		if rapid.Bool().Draw(t, "named") {
			names = rapid.SliceOfN(rapid.SampledFrom([]string{"*", "a", "b", "a/*"}), 1, 2).Draw(t, "names")
		}

		return &v1.PolicySpec{
			Resources:     rapid.SliceOfN(rapid.SampledFrom(resources), 1, 2).Draw(t, "resources"),
			ResourceNames: names,
			Verbs:         rapid.SliceOfN(rapid.SampledFrom([]string{"get", "list", "create", "delete", "*"}), 1, 2).Draw(t, "verbs"),
		}
	})

	return rapid.Custom(func(t *rapid.T) *v1.RoleSpec {
		return &v1.RoleSpec{Name: experimentAdminRole, Policies: rapid.SliceOfN(p, 0, 4).Draw(t, "policies")}
	})
}

func migrationsClone(role *v1.RoleSpec) *v1.RoleSpec {
	out := &v1.RoleSpec{Name: role.Name, Policies: make([]*v1.PolicySpec, len(role.Policies))}

	for i, p := range role.Policies {
		out.Policies[i] = &v1.PolicySpec{
			Resources: slices.Clone(p.Resources), ResourceNames: slices.Clone(p.ResourceNames), Verbs: slices.Clone(p.Verbs),
		}
	}

	return out
}

// migrationsAllowed is what the role allows, by resource, verb and name ("" for none).
func migrationsAllowed(role *v1.RoleSpec) map[[3]string]bool {
	r := newRole(role, nil)
	allowed := make(map[[3]string]bool)

	for _, resource := range []string{
		experimentFilesResource, jobsResource, captureSubnetResource, mountResource, snapshotsResource, screenshotResource,
		"experiments",
	} {
		for _, verb := range []string{"get", "list", "create", "delete", "update"} {
			allowed[[3]string{resource, verb, ""}] = r.Allowed(resource, verb)

			for _, name := range []string{"a", "b", "a/x"} {
				allowed[[3]string{resource, verb, name}] = r.Allowed(resource, verb, name)
			}
		}
	}

	return allowed
}

// companion is a permission that a migration adds for each policy that
// grants another.
type companion struct {
	name                               string
	resource, verb, toResource, toVerb string
	add                                func(*v1.RoleSpec) bool
}

// migrationsAddedCompanions checks that c.add kept the policies of original,
// and added toVerb only for names some policy granted verb but not toVerb on.
func migrationsAddedCompanions(t *rapid.T, c companion, original, role *v1.RoleSpec) {
	n := len(original.Policies)
	if len(role.Policies) < n || !cmp.Equal(original.Policies, role.Policies[:n]) {
		t.Fatalf("%s(%v) changed the policies it had to %v", c.name, rolePolicies(original), rolePolicies(role))
	}

	allows := func(p *v1.PolicySpec, resource, verb string) bool {
		return coversResource(p, resource) && (slices.Contains(p.Verbs, verb) || slices.Contains(p.Verbs, "*"))
	}

	added := role.Policies[n:]

	for i, p := range added {
		want := &v1.PolicySpec{
			Resources: []string{c.toResource}, ResourceNames: p.ResourceNames, Verbs: []string{c.toVerb},
		}

		var (
			names = func(q *v1.PolicySpec) bool { return slices.Equal(q.ResourceNames, p.ResourceNames) }
			only  = func(q *v1.PolicySpec) bool {
				return names(q) && allows(q, c.resource, c.verb) && !allows(q, c.toResource, c.toVerb)
			}
			has = func(q *v1.PolicySpec) bool { return names(q) && allows(q, c.toResource, c.toVerb) }
		)

		if !cmp.Equal(p, want) || slices.ContainsFunc(added[:i], names) ||
			!slices.ContainsFunc(original.Policies, only) || slices.ContainsFunc(original.Policies, has) {
			t.Fatalf("%s(%v) added %v, want a %s only where a role could %s but not %s on its names",
				c.name, rolePolicies(original), rolePolicies(role), c.toVerb, c.verb, c.toVerb)
		}
	}
}

// The role helpers of the migrations only ever add access, add what they are
// for, and change nothing when run again.
func TestMigrationHelpersProperties(t *testing.T) {
	t.Parallel()

	// migrate runs f on role, checks it added access only and that a second
	// run changes nothing, and returns what the role allows after it.
	migrate := func(t *rapid.T, role *v1.RoleSpec, name string, f func(*v1.RoleSpec) bool) map[[3]string]bool {
		before := migrationsAllowed(role)
		original := migrationsClone(role)
		changed := f(role)
		after := migrationsAllowed(role)

		for query, ok := range before {
			if ok && !after[query] {
				t.Fatalf("%s(%v) no longer allows %q", name, rolePolicies(original), query)
			}
		}

		if !changed && !cmp.Equal(original, role) {
			t.Fatalf("%s(%v) = false, but changed the role to %v", name, rolePolicies(original), rolePolicies(role))
		}

		again := migrationsClone(role)
		if f(again) || !cmp.Equal(again, role) {
			t.Fatalf("%s(%v) again changed it to %v", name, rolePolicies(role), rolePolicies(again))
		}

		return after
	}

	t.Run("ensurePolicy", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			role := genMigrationsRole(experimentFilesResource, jobsResource, captureSubnetResource, "experiments", "*").Draw(t, "role")
			resource := rapid.SampledFrom([]string{experimentFilesResource, jobsResource, captureSubnetResource}).Draw(t, "resource")
			verbs := rapid.SliceOfN(rapid.SampledFrom([]string{"list", "create", "delete"}), 1, 2).Draw(t, "verbs")
			names := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b"}), 0, 2).Draw(t, "names")

			after := migrate(t, role, "ensurePolicy", func(r *v1.RoleSpec) bool { return ensurePolicy(r, resource, verbs, names) })

			for _, verb := range verbs {
				for _, name := range append([]string{""}, names...) {
					if query := [3]string{resource, verb, name}; !after[query] && (name != "" || len(names) == 0) {
						t.Fatalf("ensurePolicy(%s, %q, %q) left the role without %q", resource, verbs, names, query)
					}
				}
			}
		})
	})

	for _, c := range []companion{
		{
			name: "addCaptureSubnetStop", resource: captureSubnetResource, verb: "create",
			toResource: captureSubnetResource, toVerb: "delete", add: addCaptureSubnetStop,
		},
		{
			name: "addScreenshotCreate", resource: snapshotsResource, verb: "create",
			toResource: screenshotResource, toVerb: "create", add: addScreenshotCreate,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			rapid.Check(t, func(t *rapid.T) {
				role := genMigrationsRole(c.resource, c.toResource, "exp/*", "vms/*", "*", "experiments").Draw(t, "role")
				original := migrationsClone(role)
				after := migrate(t, role, c.name, c.add)

				for _, name := range []string{"", "a", "b", "a/x"} {
					from, to := [3]string{c.resource, c.verb, name}, [3]string{c.toResource, c.toVerb, name}
					if after[from] && !after[to] {
						t.Fatalf("%s left a role allowing %q without %q", c.name, from, to)
					}
				}

				migrationsAddedCompanions(t, c, original, role)
			})
		})
	}

	t.Run("scopeUnnamed", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			role := genMigrationsRole(mountResource, mountResource, "vms/*", "experiments").Draw(t, "role")
			before := migrationsAllowed(role)
			after := migrate(t, role, "scopeUnnamed", func(r *v1.RoleSpec) bool { return scopeUnnamed(r, mountResource) })

			for query, ok := range before {
				if query[2] == "" && after[query] != ok {
					t.Fatalf("scopeUnnamed(%s) changed %q from %t", mountResource, query, ok)
				}
			}
		})
	})
}

// Each migration returns an error naming its step when the store fails to
// list the roles or users, or to save one that the migration changed.
//
//nolint:paralleltest // replaces the store
func TestMigrationsStoreErrors(t *testing.T) {
	var (
		capturer    = role("Capturer", policy(captureSubnetResource, nil, "create"))
		snapshotter = role("Snapshotter", policy(snapshotsResource, nil, "create"))
		admin       = role(experimentAdminRole, policy("experiments", nil, "get"))
		viewer      = role(experimentViewerRole,
			policy("vms/*", []string{"exp-a/*"}, "list"), policy(mountResource, nil, "post"))
		failRoles  = userFailingStore{list: map[string]bool{"Role": true}}
		failUsers  = userFailingStore{list: map[string]bool{"User": true}}
		failUpdate = userFailingStore{update: true}
		nullNames  = map[string]any{
			"roleName": "Legacy",
			"policies": []any{map[string]any{"resources": []any{"vms"}, "resourceNames": nil, "verbs": []any{"get"}}},
		}
	)

	tests := map[string]struct {
		roles   []*v1.RoleSpec
		users   []*v1.UserSpec
		raw     map[string]any // a role/legacy spec stored as it is
		fail    userFailingStore
		migrate func() error
		want    string
	}{
		"capture stop: listing roles": {
			fail: failRoles, migrate: EnsureCaptureSubnetStopPermission, want: "getting roles: ",
		},
		"capture stop: saving a role": {
			roles: []*v1.RoleSpec{capturer}, fail: failUpdate, migrate: EnsureCaptureSubnetStopPermission,
			want: "saving role Capturer: ",
		},
		"capture stop: listing users": {
			roles: []*v1.RoleSpec{admin}, fail: failUsers, migrate: EnsureCaptureSubnetStopPermission,
			want: "getting users: ",
		},
		"capture stop: saving a user": {
			users: []*v1.UserSpec{{Username: "cap", Role: capturer}}, fail: failUpdate,
			migrate: EnsureCaptureSubnetStopPermission, want: "saving user cap: ",
		},
		"screenshot: listing roles": {
			fail: failRoles, migrate: EnsureVMScreenshotPermission, want: "getting roles: ",
		},
		"screenshot: saving a role": {
			roles: []*v1.RoleSpec{snapshotter}, fail: failUpdate, migrate: EnsureVMScreenshotPermission,
			want: "saving role Snapshotter: ",
		},
		"screenshot: listing users": {
			roles: []*v1.RoleSpec{admin}, fail: failUsers, migrate: EnsureVMScreenshotPermission,
			want: "getting users: ",
		},
		"screenshot: saving a user": {
			users: []*v1.UserSpec{{Username: "snap", Role: snapshotter}}, fail: failUpdate,
			migrate: EnsureVMScreenshotPermission, want: "saving user snap: ",
		},
		"viewer mount scope: listing users": {
			fail: failUsers, migrate: EnsureExperimentViewerMountScope, want: "getting users: ",
		},
		"viewer mount scope: saving a user": {
			users: []*v1.UserSpec{{Username: "legacy", Role: viewer}}, fail: failUpdate,
			migrate: EnsureExperimentViewerMountScope, want: "saving user legacy: ",
		},
		"null names: listing": {
			fail: failUsers, migrate: DropNullResourceNames, want: "getting roles and users: ",
		},
		"null names: saving": {
			raw: nullNames, fail: failUpdate, migrate: DropNullResourceNames, want: "updating Role/legacy: ",
		},
		"vlans: listing roles": {
			fail: failRoles, migrate: EnsureVLANsPermission, want: "getting roles: ",
		},
		"vlans: saving a role": {
			roles: []*v1.RoleSpec{admin}, fail: failUpdate, migrate: EnsureVLANsPermission,
			want: "saving role Experiment Admin: ",
		},
		"files: listing roles": {
			fail: failRoles, migrate: EnsureExperimentFilesCreatePermission, want: "getting roles: ",
		},
		"files: saving a role": {
			roles: []*v1.RoleSpec{admin}, fail: failUpdate, migrate: EnsureExperimentFilesCreatePermission,
			want: "saving role Experiment Admin: ",
		},
		"files: listing users": {
			roles: []*v1.RoleSpec{capturer}, fail: failUsers, migrate: EnsureExperimentFilesCreatePermission,
			want: "getting users: ",
		},
		"files: saving a user": {
			users: []*v1.UserSpec{{Username: "admin", Role: admin}}, fail: failUpdate,
			migrate: EnsureExperimentFilesCreatePermission, want: "saving user admin: ",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useStore(t, tc.roles, tc.users)

			if tc.raw != nil {
				userStoreRaw(t, "role/legacy", tc.raw)
			}

			tc.fail.Store = store.DefaultStore
			storetest.Use(t, tc.fail)

			err := tc.migrate()
			if !errors.Is(err, errUserStore) || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("migration error = %v, want one starting %q that wraps %v", err, tc.want, errUserStore)
			}
		})
	}
}
