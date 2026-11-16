package config

import (
	"fmt"
	"slices"
	"strings"

	"phenix/store"
)

// BuiltinRoles returns, sorted, the metadata name and the role name of each
// built-in role Init stores, the names rbac.RoleFromConfig finds a role by.
func BuiltinRoles() ([]string, error) {
	configs, err := BuiltinRoleConfigs()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, 2*len(configs)) //nolint:mnd // two names each

	for _, c := range configs {
		names = append(names, c.Metadata.Name)

		if roleName, ok := c.Spec["roleName"].(string); ok && roleName != "" {
			names = append(names, roleName)
		}
	}

	slices.Sort(names)

	return slices.Compact(names), nil
}

// BuiltinRoleConfigs returns the config of each built-in role, as Init stores
// it for a new store.
func BuiltinRoleConfigs() ([]store.Config, error) {
	var configs []store.Config

	err := walkDefaults(func(_ string, _ []byte, c store.Config) error {
		if strings.EqualFold(c.Kind, "Role") {
			configs = append(configs, c)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the built-in roles: %w", err)
	}

	return configs, nil
}
