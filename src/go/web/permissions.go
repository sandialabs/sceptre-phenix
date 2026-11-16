package web

import (
	"encoding/json"
	"net/http"

	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetPermissions - GET /permissions.
//
// It lists each resource that phenix checks a role for, with the verbs it
// checks, as phenix util role-table does. It needs the permission that GET
// /roles needs.
func GetPermissions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	if !middleware.RoleFromContext(ctx).Allowed(resourceRoles, "list") {
		plog.Warn(plog.TypeSecurity, "listing permissions not allowed", "user", middleware.UserFromContext(ctx))

		return weberror.New(http.StatusForbidden, "forbidden to list permissions")
	}

	body, err := json.Marshal(util.WithRoot("permissions", rbac.KnownPermissions()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
