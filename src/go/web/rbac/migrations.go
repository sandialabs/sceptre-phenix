package rbac

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/activeshadow/structs"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

const (
	builderResource           = "builder"
	experimentAdminRole       = "Experiment Admin"
	experimentFilesCreateVerb = "create"
	experimentFilesResource   = "experiments/files"
	experimentUserRole        = "Experiment User"
	experimentViewerRole      = "Experiment Viewer"
	forwardsResource          = "vms/forwards"
	mountResource             = "vms/mount"
	scorchResource            = "scorch"
	createVerb                = "create"
	deleteVerb                = "delete"
	getVerb                   = "get"
	postVerb                  = "post"
	putVerb                   = "put"
	tunnelerResource          = "tunneler"
	vmAdminRole               = "VM Admin"
	vmViewerRole              = "VM Viewer"

	// servicePermissionsAnnotation marks a role config whose Builder, Scorch,
	// and Tunneler permissions have been migrated.
	servicePermissionsAnnotation = "phenix.rbac/service-permissions"
)

type servicePermission struct {
	resource string
	verb     string
}

// legacyServicePermissions lists the permissions each built-in role gets on
// upgrade; the default role configs grant the same. Viewer roles can open the
// Builder, and roles that can control VMs can also control Scorch runs, write to
// Scorch terminals, and use Tunneler port forwards.
var legacyServicePermissions = map[string][]servicePermission{ //nolint:gochecknoglobals // migration data
	experimentAdminRole: {
		{resource: builderResource, verb: getVerb},
		{resource: builderResource, verb: postVerb},
		{resource: builderResource, verb: putVerb},
		{resource: scorchResource, verb: getVerb},
		{resource: scorchResource, verb: postVerb},
		{resource: scorchResource, verb: deleteVerb},
		{resource: tunnelerResource, verb: getVerb},
	},
	experimentUserRole: {
		{resource: builderResource, verb: getVerb},
		{resource: builderResource, verb: postVerb},
		{resource: scorchResource, verb: getVerb},
		{resource: scorchResource, verb: postVerb},
		{resource: scorchResource, verb: deleteVerb},
		{resource: tunnelerResource, verb: getVerb},
		{resource: forwardsResource, verb: createVerb},
		{resource: forwardsResource, verb: deleteVerb},
	},
	experimentViewerRole: {
		{resource: builderResource, verb: getVerb},
		{resource: scorchResource, verb: getVerb},
		{resource: tunnelerResource, verb: getVerb},
	},
	vmAdminRole: {
		{resource: scorchResource, verb: getVerb},
		{resource: scorchResource, verb: postVerb},
		{resource: scorchResource, verb: deleteVerb},
		{resource: tunnelerResource, verb: getVerb},
	},
	vmViewerRole: {
		{resource: builderResource, verb: getVerb},
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

// EnsureServicePermissions preserves the Builder, Scorch, and Tunneler access
// built-in roles, and the users assigned to them, had before those services
// were protected by RBAC. Each role config is migrated once and then annotated,
// so an administrator can later remove these permissions without a restart
// granting them again.
func EnsureServicePermissions() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	var (
		pending      []*Role
		pendingNames = make(map[string]struct{})
	)

	for _, role := range roles {
		if _, ok := legacyServicePermissions[role.Spec.Name]; !ok {
			continue
		}

		if role.config.HasAnnotation(servicePermissionsAnnotation) {
			continue
		}

		pending = append(pending, role)
		pendingNames[role.Spec.Name] = struct{}{}
	}

	if len(pending) == 0 {
		return nil
	}

	users, err := GetUsers()
	if err != nil {
		return fmt.Errorf("getting users: %w", err)
	}

	// Users hold their own copy of their role, so migrate the copies of the
	// roles migrated in this pass.
	for _, user := range users {
		if user.Spec.Role == nil {
			continue
		}

		if _, ok := pendingNames[user.Spec.Role.Name]; !ok {
			continue
		}

		changed := ensureServicePermissions(user.Spec.Role)
		if fixExperimentViewerMount(user.Spec.Role, true) {
			changed = true
		}

		if !changed {
			continue
		}

		user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

		if err := user.Save(); err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	// Annotate roles only after their users are migrated, so an interrupted
	// migration runs again on the next start.
	for _, role := range pending {
		ensureServicePermissions(role.Spec)
		fixExperimentViewerMount(role.Spec, false)

		if role.config.Metadata.Annotations == nil {
			role.config.Metadata.Annotations = make(store.Annotations)
		}

		role.config.Metadata.Annotations[servicePermissionsAnnotation] = "true"

		if err := role.Save(); err != nil {
			return fmt.Errorf("saving role %s: %w", role.Spec.Name, err)
		}
	}

	return nil
}

// fixExperimentViewerMount repairs the Experiment Viewer vms/mount policy.
// Older default configs listed it after a policy with resourceNames, and role
// assignment only scopes the policies before the first named one, so the
// policy was never scoped and never matched a VM. In a role config it moves
// the policy ahead of the first named policy; in a user's copy of the role it
// copies the scope of the user's VM policy.
func fixExperimentViewerMount(role *v1.RoleSpec, userCopy bool) bool {
	if role.Name != experimentViewerRole {
		return false
	}

	idx := slices.IndexFunc(role.Policies, func(p *v1.PolicySpec) bool {
		return slices.Equal(p.Resources, []string{mountResource}) && p.ResourceNames == nil
	})
	if idx < 0 {
		return false
	}

	if userCopy {
		names := scopeFor(role, mountResource)
		if names == nil {
			return false
		}

		role.Policies[idx].ResourceNames = names

		return true
	}

	named := slices.IndexFunc(role.Policies, func(p *v1.PolicySpec) bool { return p.ResourceNames != nil })
	if named < 0 || named > idx {
		return false
	}

	policy := role.Policies[idx]
	role.Policies = slices.Delete(role.Policies, idx, idx+1)
	role.Policies = slices.Insert(role.Policies, named, policy)

	return true
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

// ensurePermission grants permission unless the role already allows it,
// including through wildcard resources or verbs.
func ensurePermission(role *v1.RoleSpec, permission servicePermission) bool {
	if (Role{Spec: role}).Allowed(permission.resource, permission.verb) { //nolint:exhaustruct // partial initialization
		return false
	}

	// Extend a policy for only this resource, so other resources in a shared
	// policy do not gain the verb.
	for _, policy := range role.Policies {
		if slices.Equal(policy.Resources, []string{permission.resource}) {
			policy.Verbs = append(policy.Verbs, permission.verb)

			return true
		}
	}

	role.Policies = append(role.Policies, &v1.PolicySpec{
		Resources:     []string{permission.resource},
		ResourceNames: scopeFor(role, permission.resource),
		Verbs:         []string{permission.verb},
	})

	return true
}

// scopeFor returns the resource names of the first policy whose resources
// match resource, so a new vms/forwards policy for a user keeps the scope of
// the user's vms/* policy. Service resources such as scorch match no other
// policy and stay unscoped.
func scopeFor(role *v1.RoleSpec, resource string) []string {
	for _, policy := range role.Policies {
		for _, pattern := range policy.Resources {
			if matched, _ := filepath.Match(pattern, resource); matched {
				return slices.Clone(policy.ResourceNames)
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
