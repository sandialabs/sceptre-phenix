package rbac

import (
	"fmt"
	"slices"

	"github.com/activeshadow/structs"

	v1 "phenix/types/version/v1"
)

const experimentFilesResource = "experiments/files"
const experimentFilesCreateVerb = "create"
const experimentFilesDeleteVerb = "delete"
const experimentAdminRole = "Experiment Admin"
const experimentUserRole = "Experiment User"

// EnsureExperimentFilesCreatePermission lets existing Experiment Admin and
// Experiment User roles, and users with them, upload experiment files.
func EnsureExperimentFilesCreatePermission() error {
	return ensureExperimentFilesPermission(experimentFilesCreateVerb, experimentFilesRole)
}

// EnsureExperimentFilesDeletePermission lets existing Experiment Admin roles,
// and users with them, delete experiment files, as the default Experiment Admin
// role does. Experiment User and viewer roles are left without it.
func EnsureExperimentFilesDeletePermission() error {
	return ensureExperimentFilesPermission(experimentFilesDeleteVerb, experimentFilesDeleteRole)
}

// ensureExperimentFilesPermission adds verb on experiment files to the stored
// roles, and the roles of users, that want it. A user's new permission takes
// the resource names of the user's experiments policy.
func ensureExperimentFilesPermission(verb string, wants func(role string) bool) error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if wants(role.Spec.Name) && ensureExperimentFilesPolicy(role.Spec, verb, nil) {
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
		if user.Spec.Role == nil || !wants(user.Spec.Role.Name) {
			continue
		}

		if ensureExperimentFilesPolicy(user.Spec.Role, verb, experimentResourceNames(user.Spec.Role)) {
			user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

			if err := user.Save(); err != nil {
				return fmt.Errorf("saving user %s: %w", user.Username(), err)
			}
		}
	}

	return nil
}

// experimentFilesRole returns true for roles that should allow experiment file uploads.
func experimentFilesRole(name string) bool {
	return name == experimentAdminRole || name == experimentUserRole
}

// experimentFilesDeleteRole returns true for roles that should allow deleting
// experiment files.
func experimentFilesDeleteRole(name string) bool {
	return name == experimentAdminRole
}

// ensureExperimentFilesPolicy ensures the role can take verb on experiment
// files for the given names.
func ensureExperimentFilesPolicy(role *v1.RoleSpec, verb string, names []string) bool {
	for _, policy := range role.Policies {
		if !slices.Contains(policy.Resources, experimentFilesResource) {
			continue
		}

		var changed bool
		if !slices.Contains(policy.Verbs, verb) {
			policy.Verbs = append(policy.Verbs, verb)
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
		&v1.PolicySpec{Resources: []string{experimentFilesResource}, ResourceNames: names, Verbs: []string{verb}},
	)

	return true
}

// experimentResourceNames returns resource names from the existing experiments policy.
func experimentResourceNames(role *v1.RoleSpec) []string {
	for _, policy := range role.Policies {
		if slices.Contains(policy.Resources, "experiments") {
			return slices.Clone(policy.ResourceNames)
		}
	}

	return nil
}
