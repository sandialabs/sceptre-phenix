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

	if err := rbac.EnsureExperimentFilesDeletePermission(); err != nil {
		return fmt.Errorf("ensuring experiment file delete permissions: %w", err)
	}

	if err := rbac.EnsureJobsPermission(); err != nil {
		return fmt.Errorf("ensuring job permissions: %w", err)
	}

	if err := rbac.EnsureCaptureSubnetStopPermission(); err != nil {
		return fmt.Errorf("ensuring subnet capture stop permissions: %w", err)
	}

	if err := rbac.DropNullResourceNames(); err != nil {
		return fmt.Errorf("dropping null resource names: %w", err)
	}

	if err := rbac.EnsureExperimentViewerMountScope(); err != nil {
		return fmt.Errorf("scoping Experiment Viewer mount permissions: %w", err)
	}

	if err := rbac.EnsureServicePermissions(); err != nil {
		return fmt.Errorf("ensuring Scorch and port forward permissions: %w", err)
	}

	if err := ensureServiceRoles(); err != nil {
		return fmt.Errorf("creating Scorch roles: %w", err)
	}

	return nil
}

// serviceRoles are built-in roles added after existing installs created their
// default configs.
var serviceRoles = []string{"role/scorch-viewer", "role/scorch-admin"} //nolint:gochecknoglobals // default roles

// ensureServiceRoles creates the built-in Scorch roles once, so existing
// installs get them and an administrator can delete them without a restart
// creating them again.
func ensureServiceRoles() error {
	if store.IsInitialized(store.ComponentServiceRoles) {
		return nil
	}

	if err := config.CreateDefaults(serviceRoles...); err != nil {
		return fmt.Errorf("creating default roles: %w", err)
	}

	if err := store.InitializeComponent(store.ComponentServiceRoles); err != nil {
		return fmt.Errorf("marking service roles as created: %w", err)
	}

	return nil
}
