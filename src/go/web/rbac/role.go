package rbac

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

var (
	ErrResourceNameExists  = errors.New("resource name for role exists")
	ErrResourceNameInvalid = errors.New("invalid resource name for role")
	ErrRoleNotFound        = errors.New("could not find role in store")
)

type Role struct {
	Spec *v1.RoleSpec

	config         *store.Config
	mappedPolicies map[string][]Policy
}

func GetRoles() ([]*Role, error) {
	configs, err := config.List("role")
	if err != nil {
		return nil, fmt.Errorf("getting role configs: %w", err)
	}

	roles := make([]*Role, len(configs))

	for i, c := range configs {
		var u v1.RoleSpec

		err := mapstructure.Decode(c.Spec, &u)
		if err != nil {
			return nil, fmt.Errorf("decoding roles config: %w", err)
		}

		roles[i] = newRole(&u, &c)
	}

	return roles, nil
}

// RoleFromConfig gets a role based on its name from config.
// Works for both the roleName (e.g., "Global Admin") and metadata name (e.g., "global-admin").
// It returns ErrRoleNotFound when no role has the name.
func RoleFromConfig(name string) (*Role, error) {
	roles, err := config.List("role")
	if err != nil {
		return nil, fmt.Errorf("getting role from store: %w", err)
	}

	for _, roleConfig := range roles {
		if roleConfig.Metadata.Name == name || roleConfig.Spec["roleName"] == name {
			var role v1.RoleSpec

			err := mapstructure.Decode(roleConfig.Spec, &role)
			if err != nil {
				return nil, fmt.Errorf("decoding role: %w", err)
			}

			return newRole(&role, &roleConfig), nil
		}
	}

	return nil, ErrRoleNotFound
}

// newRole builds a Role and indexes its policies once, so value copies of the
// role (request contexts, broker clients) share the index instead of
// rebuilding it on every Allowed call.
func newRole(spec *v1.RoleSpec, cfg *store.Config) *Role {
	return &Role{Spec: spec, config: cfg, mappedPolicies: mapPolicies(spec)}
}

func (r Role) Save() error {
	r.config.Spec = structs.MapDefaultCase(r.Spec, structs.CASESNAKE)

	err := store.Update(r.config)
	if err != nil {
		return fmt.Errorf("updating role in store: %w", err)
	}

	return nil
}

// SetResourceNames scopes the role's policies that have no resource names (a
// missing or empty list) to names, such as a user's experiments; no names, or
// one empty name, means every resource ("*"). A policy that already names its
// resources, such as a default role's disks or hosts policy, keeps them.
func (r *Role) SetResourceNames(names ...string) error {
	// Gracefully handle when no names or a single empty name is passed,
	// defaulting to allow all.
	switch len(names) {
	case 0:
		names = []string{"*"}
	case 1:
		if names[0] == "" {
			names[0] = "*"
		}
	}

	for _, name := range names {
		if !validPattern(name) {
			return fmt.Errorf("%w: %s", ErrResourceNameInvalid, name)
		}
	}

	for _, policy := range r.Spec.Policies {
		if len(policy.ResourceNames) == 0 {
			policy.ResourceNames = slices.Clone(names)
		}
	}

	return nil
}

// AddResourceName adds name once to each policy that names resources but not
// name. A policy that names no resources stays as it is. A bad pattern returns
// ErrResourceNameInvalid before any change. It returns ErrResourceNameExists
// only when no policy changed and a policy already names name.
func (r *Role) AddResourceName(name string) error {
	if !validPattern(name) {
		return fmt.Errorf("%w: %s", ErrResourceNameInvalid, name)
	}

	var changed, exists bool

	for _, policy := range r.Spec.Policies {
		if len(policy.ResourceNames) == 0 {
			continue
		}

		if slices.Contains(policy.ResourceNames, name) {
			exists = true

			continue
		}

		policy.ResourceNames = append(policy.ResourceNames, name)
		changed = true
	}

	if exists && !changed {
		return fmt.Errorf("%w: %s", ErrResourceNameExists, name)
	}

	return nil
}

func (r *Role) AddPolicy(res, rn, v []string) {
	policy := &v1.PolicySpec{
		Resources:     res,
		ResourceNames: rn,
		Verbs:         v,
	}

	r.Spec.Policies = append(r.Spec.Policies, policy)

	// the index maps resources to policies, so a new policy invalidates it
	r.mappedPolicies = mapPolicies(r.Spec)
}

func (r Role) Allowed(resource, verb string, names ...string) bool {
	// the zero Role (no role in the request context) allows nothing
	if r.Spec == nil {
		return false
	}

	for _, policy := range r.policiesForResource(resource) {
		if policy.verbAllowed(verb) {
			if len(names) == 0 {
				return true
			}

			if slices.ContainsFunc(names, policy.resourceNameAllowed) {
				return true
			}
		}
	}

	return false
}

func (r Role) policiesForResource(resource string) []Policy {
	mapped := r.mappedPolicies
	if mapped == nil {
		// Role built without an index (e.g., a literal): build a throwaway one
		// rather than mutating a value that may be shared across goroutines.
		mapped = mapPolicies(r.Spec)
	}

	var policies []Policy

	for r, p := range mapped {
		if matched, _ := filepath.Match(r, resource); matched {
			policies = append(policies, p...)
		}
	}

	return policies
}

// mapPolicies indexes the spec's policies by resource pattern. A role with any
// invalid resource pattern allows nothing, so it maps to an empty (non-nil)
// index.
func mapPolicies(spec *v1.RoleSpec) map[string][]Policy {
	mapped := make(map[string][]Policy)

	for _, policy := range spec.Policies {
		for _, resource := range policy.Resources {
			if !validPattern(resource) {
				return map[string][]Policy{}
			}

			mapped[resource] = append(mapped[resource], Policy{Spec: policy})
		}
	}

	return mapped
}
