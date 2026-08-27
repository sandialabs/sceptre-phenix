package rbac

import (
	"fmt"
	"slices"

	"github.com/activeshadow/structs"

	v1 "phenix/types/version/v1"
)

const (
	builderResource           = "builder"
	experimentAdminRole       = "Experiment Admin"
	experimentFilesCreateVerb = "create"
	experimentFilesResource   = "experiments/files"
	experimentUserRole        = "Experiment User"
	scorchResource            = "scorch"
	getVerb                   = "get"
	postVerb                  = "post"
	putVerb                   = "put"
	tunnelerResource          = "tunneler"
)

type servicePermission struct {
	resource string
	verb     string
}

var legacyServicePermissions = map[string][]servicePermission{ //nolint:gochecknoglobals // migration data
	experimentAdminRole: {
		{resource: builderResource, verb: getVerb},
		{resource: builderResource, verb: postVerb},
		{resource: builderResource, verb: putVerb},
		{resource: scorchResource, verb: getVerb},
		{resource: tunnelerResource, verb: getVerb},
	},
	experimentUserRole: {
		{resource: builderResource, verb: getVerb},
		{resource: builderResource, verb: postVerb},
		{resource: scorchResource, verb: getVerb},
		{resource: tunnelerResource, verb: getVerb},
	},
}

// EnsureExperimentFilesCreatePermission updates existing roles and users for file uploads.
func EnsureExperimentFilesCreatePermission() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if experimentFilesRole(role.Spec.Name) && ensureExperimentFilesCreatePolicy(role.Spec, nil) {
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
		if user.Spec.Role == nil || !experimentFilesRole(user.Spec.Role.Name) {
			continue
		}

		if ensureExperimentFilesCreatePolicy(user.Spec.Role, experimentResourceNames(user.Spec.Role)) {
			user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

			if err := user.Save(); err != nil {
				return fmt.Errorf("saving user %s: %w", user.Username(), err)
			}
		}
	}

	return nil
}

// EnsureServicePermissions preserves access for built-in roles after adding service-level RBAC.
func EnsureServicePermissions() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if ensureServicePermissions(role.Spec) {
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
		if user.Spec.Role == nil || !ensureServicePermissions(user.Spec.Role) {
			continue
		}

		user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

		if err := user.Save(); err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	return nil
}

func ensureServicePermissions(role *v1.RoleSpec) bool {
	var changed bool

	for _, permission := range legacyServicePermissions[role.Name] {
		if ensurePermission(role, permission) {
			changed = true
		}
	}

	return changed
}

func ensurePermission(role *v1.RoleSpec, permission servicePermission) bool {
	for _, policy := range role.Policies {
		if !slices.Contains(policy.Resources, permission.resource) {
			continue
		}

		if slices.Contains(policy.Verbs, permission.verb) {
			return false
		}

		policy.Verbs = append(policy.Verbs, permission.verb)

		return true
	}

	role.Policies = append(role.Policies, &v1.PolicySpec{
		Resources:     []string{permission.resource},
		ResourceNames: nil,
		Verbs:         []string{permission.verb},
	})

	return true
}

// experimentFilesRole returns true for roles that should allow experiment file uploads.
func experimentFilesRole(name string) bool {
	return name == experimentAdminRole || name == experimentUserRole
}

// ensureExperimentFilesCreatePolicy ensures the role can create experiment files for the given names.
func ensureExperimentFilesCreatePolicy(role *v1.RoleSpec, names []string) bool {
	for _, policy := range role.Policies {
		if !slices.Contains(policy.Resources, experimentFilesResource) {
			continue
		}

		var changed bool
		if !slices.Contains(policy.Verbs, experimentFilesCreateVerb) {
			policy.Verbs = append(policy.Verbs, experimentFilesCreateVerb)
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
		&v1.PolicySpec{Resources: []string{experimentFilesResource}, ResourceNames: names, Verbs: []string{experimentFilesCreateVerb}},
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
