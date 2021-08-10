package web

import (
	"errors"
	"fmt"
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

	return nil
}
