package web

import (
	"errors"
	"fmt"

	"phenix/api/config"
	"phenix/store"
	"phenix/web/rbac"
)

func Init() error {
	// To avoid users having to manually edit their "global-admin" role config to
	// handle resource names with forward slashes in the name (needed for
	// protecting config resources), we ensure it's been updated here at runtime.
	admin, err := rbac.RoleFromConfig("global-admin")
	if err != nil {
		return fmt.Errorf("getting global-admin role on startup: %w", err)
	}

	if err := admin.AddResourceName("*/*"); err != nil {
		if !errors.Is(err, rbac.ErrResourceNameExists) {
			return fmt.Errorf("ensuring */* resource name for global-admin role: %w", err)
		}
	}

	if err := admin.Save(); err != nil {
		return fmt.Errorf("saving updated global-admin role: %w", err)
	}

	if err := rbac.EnsureExperimentFilesCreatePermission(); err != nil {
		return fmt.Errorf("ensuring experiment file upload permissions: %w", err)
	}

	if err := rbac.EnsureServicePermissions(); err != nil {
		return fmt.Errorf("ensuring Builder, Scorch, and Tunneler permissions: %w", err)
	}

	if err := ensureServiceRoles(); err != nil {
		return fmt.Errorf("creating Builder and Scorch roles: %w", err)
	}

	return nil
}

// serviceRoles are built-in roles added after existing installs created their
// default configs.
var serviceRoles = []string{"role/builder", "role/scorch-viewer", "role/scorch-admin"} //nolint:gochecknoglobals // default roles

// ensureServiceRoles creates the built-in Builder and Scorch roles once, so
// existing installs get them and an administrator can delete them without a
// restart creating them again.
func ensureServiceRoles() error {
	if store.IsInitialized(store.ComponentServiceRoles) {
		return nil
	}

	if err := config.CreateDefaults(serviceRoles...); err != nil {
		return err
	}

	if err := store.InitializeComponent(store.ComponentServiceRoles); err != nil {
		return fmt.Errorf("marking service roles as created: %w", err)
	}

	return nil
}
