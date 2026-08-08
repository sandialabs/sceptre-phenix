package rbac

import (
	"fmt"
	"slices"

	"github.com/activeshadow/structs"

	v1 "phenix/types/version/v1"
)

const (
	verbCreate = "create"
	verbPatch  = "patch"

	resourceExperiments            = "experiments"
	resourceExperimentsFiles       = "experiments/files"
	resourceExperimentsReconfigure = "experiments/reconfigure"
	resourceExperimentsVLANs       = "experiments/vlans"

	roleExperimentAdmin = "Experiment Admin"
	roleExperimentUser  = "Experiment User"
)

// rolePermission is a resource/verb pair that must exist on the named roles.
type rolePermission struct {
	resource string
	verb     string
	roles    []string
}

// rolePermissionMigrations backfills permissions onto roles that were stored
// before the corresponding API route existed. Editing the default role configs
// under api/config/default only affects new installs, so every addition there
// needs a matching entry here.
//
//nolint:gochecknoglobals // migration table
var rolePermissionMigrations = []rolePermission{
	{
		resource: resourceExperimentsFiles,
		verb:     verbCreate,
		roles:    []string{roleExperimentAdmin, roleExperimentUser},
	},
	{
		resource: resourceExperimentsReconfigure,
		verb:     verbCreate,
		roles:    []string{roleExperimentAdmin},
	},
	{
		resource: resourceExperimentsVLANs,
		verb:     verbPatch,
		roles:    []string{roleExperimentAdmin},
	},
}

// EnsureRolePermissions applies rolePermissionMigrations to stored roles and users.
func EnsureRolePermissions() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if ensureRolePolicies(role.Spec, nil) {
			if err := role.Save(); err != nil {
				return fmt.Errorf("saving role %s: %w", role.Spec.Name, err)
			}
		}
	}

	users, err := GetUsers()
	if err != nil {
		return fmt.Errorf("getting users: %w", err)
	}

	for _, user := range users {
		if user.Spec.Role == nil {
			continue
		}

		if ensureRolePolicies(user.Spec.Role, experimentResourceNames(user.Spec.Role)) {
			user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

			if err := user.Save(); err != nil {
				return fmt.Errorf("saving user %s: %w", user.Username(), err)
			}
		}
	}

	return nil
}

// ensureRolePolicies applies every migration targeting the role, reporting whether it changed.
func ensureRolePolicies(role *v1.RoleSpec, names []string) bool {
	var changed bool

	for _, migration := range rolePermissionMigrations {
		if !slices.Contains(migration.roles, role.Name) {
			continue
		}

		if ensureRolePolicy(role, migration, names) {
			changed = true
		}
	}

	return changed
}

// ensureRolePolicy ensures the role allows the migration's verb on its resource for the given names.
func ensureRolePolicy(role *v1.RoleSpec, migration rolePermission, names []string) bool {
	for _, policy := range role.Policies {
		if !slices.Contains(policy.Resources, migration.resource) {
			continue
		}

		var changed bool
		if !slices.Contains(policy.Verbs, migration.verb) {
			policy.Verbs = append(policy.Verbs, migration.verb)
			changed = true
		}

		for _, name := range names {
			if !slices.Contains(policy.ResourceNames, name) {
				policy.ResourceNames = append(policy.ResourceNames, name)
				changed = true
			}
		}

		return changed
	}

	role.Policies = append(
		role.Policies,
		&v1.PolicySpec{
			Resources:     []string{migration.resource},
			ResourceNames: names,
			Verbs:         []string{migration.verb},
		},
	)

	return true
}

// experimentResourceNames returns resource names from the existing experiments policy.
func experimentResourceNames(role *v1.RoleSpec) []string {
	for _, policy := range role.Policies {
		if slices.Contains(policy.Resources, resourceExperiments) {
			return slices.Clone(policy.ResourceNames)
		}
	}

	return nil
}
