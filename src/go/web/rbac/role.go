package rbac

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

var (
	ErrResourceNameExists  = errors.New("resource name for role exists")
	ErrResourceNameInvalid = errors.New("invalid resource name for role")
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

		roles[i] = &Role{Spec: &u, config: &c} //nolint:exhaustruct // partial initialization
	}

	return roles, nil
}

// RoleFromConfig gets a role based on its name from config.
// Works for both the roleName (e.g., "Global Admin") and metadata name (e.g., "global-admin").
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

			return &Role{Spec: &role, config: &roleConfig}, nil //nolint:exhaustruct // partial initialization
		}
	}

	return nil, fmt.Errorf("could not find role in store: %w", err)
}

func (r Role) Save() error {
	r.config.Spec = structs.MapDefaultCase(r.Spec, structs.CASESNAKE)

	err := store.Update(r.config)
	if err != nil {
		return fmt.Errorf("updating role in store: %w", err)
	}

	return nil
}

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

	for _, policy := range r.Spec.Policies {
		if policy.ResourceNames != nil {
			return fmt.Errorf("%w: resource names already exist for policy", ErrResourceNameExists)
		}

		for _, name := range names {
			// Checking to make sure pattern given in 'name' is valid. Thus, the
			// string provided to match it against is useless.
			if _, err := filepath.Match(name, "useless"); err != nil {
				return fmt.Errorf("%w: %s", ErrResourceNameInvalid, name)
			}

			policy.ResourceNames = append(policy.ResourceNames, name)
		}
	}

	return nil
}

func (r *Role) AddResourceName(name string) error {
	for _, policy := range r.Spec.Policies {
		for _, existing := range policy.ResourceNames {
			if name == existing {
				return fmt.Errorf("%w: %s", ErrResourceNameExists, name)
			}

			// Checking to make sure pattern given in 'name' is valid. Thus, the
			// string provided to match it against is useless.
			if _, err := filepath.Match(name, "useless"); err != nil {
				return fmt.Errorf("%w: %s", ErrResourceNameInvalid, name)
			}

			policy.ResourceNames = append(policy.ResourceNames, name)
		}
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
}

func (r Role) Allowed(resource, verb string, names ...string) bool {
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
	err := r.mapPolicies()
	if err != nil {
		return nil
	}

	var policies []Policy

	for r, p := range r.mappedPolicies {
		if matched, _ := filepath.Match(r, resource); matched {
			policies = append(policies, p...)

			continue
		}
	}

	return policies
}

func (r *Role) mapPolicies() error {
	if r.mappedPolicies != nil {
		return nil
	}

	r.mappedPolicies = make(map[string][]Policy)

	var invalid []string

	for _, policy := range r.Spec.Policies {
		for _, resource := range policy.Resources {
			// Checking to make sure pattern given in 'resource' is valid. Thus, the
			// string provided to match it against is useless.
			if _, err := filepath.Match(resource, "useless"); err != nil {
				invalid = append(invalid, resource)

				continue
			}

			mapped := r.mappedPolicies[resource]
			mapped = append(mapped, Policy{Spec: policy})
			r.mappedPolicies[resource] = mapped
		}
	}

	if len(invalid) != 0 {
		return errors.New("invalid resource(s): " + strings.Join(invalid, ", "))
	}

	return nil
}
