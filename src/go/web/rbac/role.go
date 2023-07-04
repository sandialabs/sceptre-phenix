package rbac

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"phenix/api/config"
	"phenix/store"

	v1 "phenix/types/version/v1"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
)

var (
	ErrResourceNameExists  = fmt.Errorf("resource name for role exists")
	ErrResourceNameInvalid = fmt.Errorf("invalid resource name for role")
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
		if err := mapstructure.Decode(c.Spec, &u); err != nil {
			return nil, fmt.Errorf("decoding roles config: %w", err)
		}

		roles[i] = &Role{Spec: &u, config: &c}
	}

	return roles, nil
}

// Get a role based on its name from config.
// Works for both the roleName (e.g., "Global Admin") and metadata name (e.g., "global-admin")
func RoleFromConfig(name string) (*Role, error) {
	roles, err := config.List("role")
	if err != nil {
		return nil, fmt.Errorf("getting role from store: %w", err)
	}

	for _, roleConfig := range roles {
		if roleConfig.Metadata.Name == name || roleConfig.Spec["roleName"] == name {
			var role v1.RoleSpec

			if err := mapstructure.Decode(roleConfig.Spec, &role); err != nil {
				return nil, fmt.Errorf("decoding role: %w", err)
			}

			return &Role{Spec: &role, config: &roleConfig}, nil
		}
	}
	return nil, fmt.Errorf("could not find role in store: %w", err)
}

func (this Role) Save() error {
	this.config.Spec = structs.MapDefaultCase(this.Spec, structs.CASESNAKE)

	if err := store.Update(this.config); err != nil {
		return fmt.Errorf("updating role in store: %w", err)
	}

	return nil
}

func (this *Role) SetResourceNames(names ...string) error {
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

	for _, policy := range this.Spec.Policies {
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

func (this *Role) AddResourceName(name string) error {
	for _, policy := range this.Spec.Policies {
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

func (this *Role) AddPolicy(r, rn, v []string) {
	policy := &v1.PolicySpec{
		Resources:     r,
		ResourceNames: rn,
		Verbs:         v,
	}

	this.Spec.Policies = append(this.Spec.Policies, policy)
}

func (this Role) Allowed(resource, verb string, names ...string) bool {
	for _, policy := range this.policiesForResource(resource) {
		if policy.verbAllowed(verb) {
			if len(names) == 0 {
				return true
			}

			for _, n := range names {
				if policy.resourceNameAllowed(n) {
					return true
				}
			}
		}
	}

	return false
}

func (this Role) policiesForResource(resource string) []Policy {
	if err := this.mapPolicies(); err != nil {
		return nil
	}

	var policies []Policy

	for r, p := range this.mappedPolicies {
		if matched, _ := filepath.Match(r, resource); matched {
			policies = append(policies, p...)
			continue
		}
	}

	return policies
}

func (this *Role) mapPolicies() error {
	if this.mappedPolicies != nil {
		return nil
	}

	this.mappedPolicies = make(map[string][]Policy)

	var invalid []string

	for _, policy := range this.Spec.Policies {
		for _, resource := range policy.Resources {
			// Checking to make sure pattern given in 'resource' is valid. Thus, the
			// string provided to match it against is useless.
			if _, err := filepath.Match(resource, "useless"); err != nil {
				invalid = append(invalid, resource)
				continue
			}

			mapped := this.mappedPolicies[resource]
			mapped = append(mapped, Policy{Spec: policy})
			this.mappedPolicies[resource] = mapped
		}
	}

	if len(invalid) != 0 {
		return errors.New("invalid resource(s): " + strings.Join(invalid, ", "))
	}

	return nil
}
