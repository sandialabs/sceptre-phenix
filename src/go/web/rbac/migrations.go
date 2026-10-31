package rbac

import (
	"fmt"
	"slices"
	"strings"

	"github.com/activeshadow/structs"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

const experimentFilesResource = "experiments/files"
const experimentFilesCreateVerb = "create"
const experimentAdminRole = "Experiment Admin"
const experimentUserRole = "Experiment User"

// EnsureExperimentFilesCreatePermission lets existing Experiment Admin and
// Experiment User roles, and users with them, upload experiment files.
func EnsureExperimentFilesCreatePermission() error {
	return ensurePermission(experimentFilesResource, []string{experimentFilesCreateVerb}, experimentFilesRole)
}

// DropNullResourceNames removes the resourceNames: null that roles and users
// saved before an empty list was left out, since the role schema rejects it.
func DropNullResourceNames() error {
	configs, err := store.List("Role", "User")
	if err != nil {
		return fmt.Errorf("getting roles and users: %w", err)
	}

	for i := range configs {
		c := &configs[i]

		policies := c.Spec["policies"]
		if strings.EqualFold(c.Kind, "User") {
			role, _ := c.Spec["rbac"].(map[string]any)
			policies = role["policies"]
		}

		if !dropNullNames(policies) {
			continue
		}

		if err := store.Update(c); err != nil {
			return fmt.Errorf("updating %s: %w", c.FullName(), err)
		}
	}

	return nil
}

// dropNullNames removes resourceNames from the stored policies that hold
// null there, and reports whether it removed any.
func dropNullNames(policies any) bool {
	list, _ := policies.([]any)

	var changed bool

	for _, p := range list {
		policy, _ := p.(map[string]any)
		if names, ok := policy["resourceNames"]; ok && names == nil {
			delete(policy, "resourceNames")

			changed = true
		}
	}

	return changed
}

// ensurePermission adds verbs on resource to the stored roles, and the roles
// of users, that want it. A user's new permission takes the resource names of
// the user's experiments policy.
func ensurePermission(resource string, verbs []string, wants func(role string) bool) error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if wants(role.Spec.Name) && ensurePolicy(role.Spec, resource, verbs, nil) {
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

		if ensurePolicy(user.Spec.Role, resource, verbs, experimentResourceNames(user.Spec.Role)) {
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

// ensurePolicy ensures the role can take verbs on resource for the given
// names.
func ensurePolicy(role *v1.RoleSpec, resource string, verbs, names []string) bool {
	for _, policy := range role.Policies {
		if !slices.Contains(policy.Resources, resource) {
			continue
		}

		var changed bool

		for _, verb := range verbs {
			if !slices.Contains(policy.Verbs, verb) {
				policy.Verbs = append(policy.Verbs, verb)
				changed = true
			}
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
		&v1.PolicySpec{Resources: []string{resource}, ResourceNames: names, Verbs: slices.Clone(verbs)},
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
