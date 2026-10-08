package rbac

import (
	"fmt"
	"slices"

	"github.com/activeshadow/structs"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

const experimentFilesResource = "experiments/files"
const experimentFilesCreateVerb = "create"
const experimentAdminRole = "Experiment Admin"
const experimentUserRole = "Experiment User"

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

// The built-in Builder role (api/config/default/builder.yml), and the one
// permission of it that [EnsureBuilderTemplatesPublishPermission] makes sure
// a role of that name holds.
const (
	builderRoleConfig           = "builder"
	builderRoleName             = "Builder"
	builderTemplatesResource    = "builder-templates"
	builderTemplatesPublishVerb = "publish"
)

// EnsureBuilderTemplatesPublishPermission makes sure the store holds the
// built-in Builder role, and that it lets its users publish Builder template
// library items to every user. It runs at every start, after the default
// configs are created.
//
// A store that has no role named builder and none whose role name is Builder
// gets the built-in one: the default configs are created only when a store is
// first initialized, so a store made before the role was shipped would never
// get it otherwise. A role of that name an administrator made, and every user
// assigned it, gain the builder-templates publish policy when they lack it;
// nothing else in them changes. On a store that holds the built-in role
// unchanged, nothing is written.
func EnsureBuilderTemplatesPublishPermission() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	// Users hold a copy of their role, by its role name.
	names := map[string]bool{builderRoleName: true}
	found := false

	for _, role := range roles {
		if role.config.Metadata.Name != builderRoleConfig && role.Spec.Name != builderRoleName {
			continue
		}

		found = true
		names[role.Spec.Name] = true

		if err := role.ensureBuilderTemplatesPublish(); err != nil {
			return err
		}
	}

	if !found {
		if _, err := config.CreateDefault("Role", builderRoleConfig); err != nil {
			return fmt.Errorf("creating the %s role: %w", builderRoleName, err)
		}
	}

	users, err := GetUsers()
	if err != nil {
		return fmt.Errorf("getting users: %w", err)
	}

	for _, user := range users {
		if user.Spec.Role == nil || !names[user.Spec.Role.Name] || builderTemplatesPublishAllowed(user.Spec.Role) {
			continue
		}

		err := user.update(func(spec *v1.UserSpec) {
			if spec.Role != nil && !builderTemplatesPublishAllowed(spec.Role) {
				spec.Role.Policies = append(spec.Role.Policies, builderTemplatesPublishPolicy())
			}
		})
		if err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	return nil
}

// ensureBuilderTemplatesPublish adds the builder-templates publish policy to
// the role, unless it allows that already. The policy is added to the
// stored spec as it is, so the role's other policies are written back
// exactly as they were.
func (r *Role) ensureBuilderTemplatesPublish() error {
	if builderTemplatesPublishAllowed(r.Spec) {
		return nil
	}

	r.Spec.Policies = append(r.Spec.Policies, builderTemplatesPublishPolicy())
	r.mappedPolicies = nil

	// The store decodes a list of policies as a list of values. A spec of
	// any other shape is written back from the policies as decoded instead.
	policies, ok := r.config.Spec["policies"].([]any)
	if r.config.Spec == nil || (!ok && r.config.Spec["policies"] != nil) {
		return r.Save()
	}

	r.config.Spec["policies"] = append(policies, map[string]any{
		"resources": []any{builderTemplatesResource},
		"verbs":     []any{builderTemplatesPublishVerb},
	})

	if err := store.Update(r.config); err != nil {
		return fmt.Errorf("saving role %s: %w", r.Spec.Name, err)
	}

	return nil
}

// builderTemplatesPublishAllowed reports whether the role allows publishing
// Builder template library items to every user, through any policy.
func builderTemplatesPublishAllowed(spec *v1.RoleSpec) bool {
	return Role{Spec: spec}.Allowed(builderTemplatesResource, builderTemplatesPublishVerb) //nolint:exhaustruct // a role to check
}

// builderTemplatesPublishPolicy returns the policy that allows publishing
// Builder template library items to every user. It lists no resource
// names: the permission is not about one item.
func builderTemplatesPublishPolicy() *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     []string{builderTemplatesResource},
		ResourceNames: nil,
		Verbs:         []string{builderTemplatesPublishVerb},
	}
}
