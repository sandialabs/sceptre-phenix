package config

import (
	"fmt"
	"io/fs"
	"slices"

	"gopkg.in/yaml.v3"

	"phenix/store"
)

// BuiltinRoles returns, sorted, the metadata name and the role name of each
// built-in role Init stores, the names rbac.RoleFromConfig finds a role by.
func BuiltinRoles() ([]string, error) {
	var names []string

	err := fs.WalkDir(defaultFS, "default", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		content, err := defaultFS.ReadFile(path)
		if err != nil {
			return err
		}

		var c store.Config
		if err := yaml.Unmarshal(content, &c); err != nil {
			return fmt.Errorf("unmarshaling default config %s: %w", path, err)
		}

		if c.Kind != "Role" {
			return nil
		}

		names = append(names, c.Metadata.Name)

		if roleName, ok := c.Spec["roleName"].(string); ok && roleName != "" {
			names = append(names, roleName)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the built-in roles: %w", err)
	}

	slices.Sort(names)

	return slices.Compact(names), nil
}
