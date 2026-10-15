package rbac

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/activeshadow/structs"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

const experimentFilesResource = "experiments/files"
const jobsResource = "jobs"
const experimentFilesCreateVerb = "create"
const experimentFilesDeleteVerb = "delete"
const experimentAdminRole = "Experiment Admin"
const experimentUserRole = "Experiment User"

// EnsureExperimentFilesCreatePermission lets existing Experiment Admin and
// Experiment User roles, and users with them, upload experiment files.
func EnsureExperimentFilesCreatePermission() error {
	return ensurePermission(experimentFilesResource, []string{experimentFilesCreateVerb}, experimentFilesRole)
}

// EnsureExperimentFilesDeletePermission lets existing Experiment Admin roles,
// and users with them, delete experiment files, as the default Experiment Admin
// role does. Experiment User and viewer roles are left without it.
func EnsureExperimentFilesDeletePermission() error {
	return ensurePermission(experimentFilesResource, []string{experimentFilesDeleteVerb}, experimentFilesDeleteRole)
}

// EnsureJobsPermission lets existing Experiment Admin roles, and users with
// them, see, cancel and dismiss the background jobs of their experiments that
// other users started, as the default Experiment Admin role does.
func EnsureJobsPermission() error {
	return ensurePermission(jobsResource, []string{"list", "get", "update", "delete"}, experimentFilesDeleteRole)
}

// captureSubnetResource is the resource of starting (create) and stopping
// (delete) the captures of a subnet.
const captureSubnetResource = "exp/captureSubnet"

// EnsureCaptureSubnetStopPermission lets the stored roles, and users' roles,
// that may start subnet captures stop them as well. Stopping once needed the
// same create permission as starting, and now needs delete.
func EnsureCaptureSubnetStopPermission() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	for _, role := range roles {
		if addCaptureSubnetStop(role.Spec) {
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
		if user.Spec.Role == nil || !addCaptureSubnetStop(user.Spec.Role) {
			continue
		}

		user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

		if err := user.Save(); err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	return nil
}

// addCaptureSubnetStop gives the role, for each policy that lets it start
// subnet captures but not stop them, a policy that lets it stop them on the
// same names. A policy covers subnet captures when one of its resource
// patterns matches captureSubnetResource, as Role.Allowed matches them, so
// "exp/*" counts. It reports whether it changed the role.
func addCaptureSubnetStop(role *v1.RoleSpec) bool {
	var added []*v1.PolicySpec

	for _, policy := range role.Policies {
		if !coversCaptureSubnet(policy) || !slices.Contains(policy.Verbs, "create") ||
			slices.Contains(policy.Verbs, "delete") || slices.Contains(policy.Verbs, "*") {
			continue
		}

		stops := func(p *v1.PolicySpec) bool {
			return coversCaptureSubnet(p) &&
				(slices.Contains(p.Verbs, "delete") || slices.Contains(p.Verbs, "*")) &&
				slices.Equal(p.ResourceNames, policy.ResourceNames)
		}

		if slices.ContainsFunc(role.Policies, stops) || slices.ContainsFunc(added, stops) {
			continue
		}

		added = append(added, &v1.PolicySpec{
			Resources:     []string{captureSubnetResource},
			ResourceNames: slices.Clone(policy.ResourceNames),
			Verbs:         []string{"delete"},
		})
	}

	role.Policies = append(role.Policies, added...)

	return len(added) > 0
}

// coversCaptureSubnet reports whether one of the policy's resource patterns
// matches captureSubnetResource.
func coversCaptureSubnet(policy *v1.PolicySpec) bool {
	return slices.ContainsFunc(policy.Resources, func(pattern string) bool {
		matched, _ := filepath.Match(pattern, captureSubnetResource)

		return matched
	})
}

const (
	experimentViewerRole = "Experiment Viewer"
	mountResource        = "vms/mount"
)

// EnsureExperimentViewerMountScope scopes the vms/mount policy of users who
// were given the Experiment Viewer role before every unnamed policy was scoped
// on assignment, and so cannot mount their VMs, to the user's VM names.
func EnsureExperimentViewerMountScope() error {
	users, err := GetUsers()
	if err != nil {
		return fmt.Errorf("getting users: %w", err)
	}

	for _, user := range users {
		if user.Spec.Role == nil || user.Spec.Role.Name != experimentViewerRole || !scopeUnnamed(user.Spec.Role, mountResource) {
			continue
		}

		user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

		if err := user.Save(); err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	return nil
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

		c.IfRevision = c.Metadata.Revision

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

// scopeUnnamed gives the role's policies on resource that name nothing the
// names of its first other policy covering resource, and reports whether it
// changed the role.
func scopeUnnamed(role *v1.RoleSpec, resource string) bool {
	covers := func(p *v1.PolicySpec) bool {
		return len(p.ResourceNames) > 0 && slices.ContainsFunc(p.Resources, func(pattern string) bool {
			matched, _ := filepath.Match(pattern, resource)

			return matched
		})
	}

	i := slices.IndexFunc(role.Policies, covers)
	if i < 0 {
		return false
	}

	var changed bool

	for _, policy := range role.Policies {
		if len(policy.ResourceNames) == 0 && slices.Equal(policy.Resources, []string{resource}) {
			policy.ResourceNames = slices.Clone(role.Policies[i].ResourceNames)
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

// experimentFilesDeleteRole returns true for roles that should allow deleting
// experiment files, and acting on other users' jobs of their experiments.
func experimentFilesDeleteRole(name string) bool {
	return name == experimentAdminRole
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

const (
	scorchResource   = "scorch"
	forwardsResource = "vms/forwards"
	vmAdminRole      = "VM Admin"

	// servicePermissionsAnnotation marks a built-in role config that has been
	// given its service permissions, so they are given once.
	servicePermissionsAnnotation = "phenix.rbac/service-permissions"
)

// serviceGrant is verbs on a resource that a built-in role gains.
type serviceGrant struct {
	resource string
	verbs    []string
}

// serviceGrants lists what each built-in role gains on upgrade, as its default
// config grants: Scorch access, and port forwards for Experiment User.
var serviceGrants = map[string][]serviceGrant{ //nolint:gochecknoglobals // migration data
	experimentAdminRole:  {{scorchResource, []string{"get", "post", "delete"}}},
	experimentUserRole:   {{scorchResource, []string{"get", "post", "delete"}}, {forwardsResource, []string{"create", "delete"}}},
	experimentViewerRole: {{scorchResource, []string{"get"}}},
	vmAdminRole:          {{scorchResource, []string{"get", "post", "delete"}}},
}

// EnsureServicePermissions gives the stored built-in roles, and the roles of
// users assigned them, the Scorch and port forward permissions their default
// configs grant. Each role config is migrated once and then annotated, so an
// administrator can remove these permissions without a restart granting them
// again.
func EnsureServicePermissions() error {
	roles, err := GetRoles()
	if err != nil {
		return fmt.Errorf("getting roles: %w", err)
	}

	var (
		pending      []*Role
		pendingNames = make(map[string]bool)
	)

	for _, role := range roles {
		if _, ok := serviceGrants[role.Spec.Name]; ok && !role.config.HasAnnotation(servicePermissionsAnnotation) {
			pending = append(pending, role)
			pendingNames[role.Spec.Name] = true
		}
	}

	if len(pending) == 0 {
		return nil
	}

	users, err := GetUsers()
	if err != nil {
		return fmt.Errorf("getting users: %w", err)
	}

	for _, user := range users {
		if user.Spec.Role == nil || !pendingNames[user.Spec.Role.Name] || !grantService(user.Spec.Role, true) {
			continue
		}

		user.config.Spec = structs.MapDefaultCase(user.Spec, structs.CASESNAKE)

		if err := user.Save(); err != nil {
			return fmt.Errorf("saving user %s: %w", user.Username(), err)
		}
	}

	// roles are annotated only after their users are migrated, so an
	// interrupted migration runs again on the next start
	for _, role := range pending {
		grantService(role.Spec, false)

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

// grantService gives the role each verb of its service grants that it does
// not already have, wildcards included, and reports whether it changed the
// role. A user's copy of the role scopes a new policy as the user's policy
// covering the resource, such as vms/*, or else as the user's experiments.
func grantService(role *v1.RoleSpec, userCopy bool) bool {
	var changed bool

	for _, grant := range serviceGrants[role.Name] {
		for _, verb := range grant.verbs {
			if (Role{Spec: role}).Allowed(grant.resource, verb) { //nolint:exhaustruct // a throwaway index
				continue
			}

			changed = true

			// a policy of only this resource gains the verb, so the other
			// resources of a shared policy do not
			i := slices.IndexFunc(role.Policies, func(p *v1.PolicySpec) bool {
				return slices.Equal(p.Resources, []string{grant.resource})
			})
			if i >= 0 {
				role.Policies[i].Verbs = append(role.Policies[i].Verbs, verb)

				continue
			}

			var names []string
			if userCopy {
				names = scopeOf(role, grant.resource)
			}

			role.Policies = append(role.Policies, &v1.PolicySpec{
				Resources: []string{grant.resource}, ResourceNames: names, Verbs: []string{verb},
			})
		}
	}

	return changed
}

// scopeOf returns the resource names of the role's first named policy whose
// resources cover resource, or else those of its experiments policy.
func scopeOf(role *v1.RoleSpec, resource string) []string {
	for _, policy := range role.Policies {
		covers := slices.ContainsFunc(policy.Resources, func(pattern string) bool {
			matched, _ := filepath.Match(pattern, resource)

			return matched
		})

		if covers && len(policy.ResourceNames) > 0 {
			return slices.Clone(policy.ResourceNames)
		}
	}

	return experimentResourceNames(role)
}
