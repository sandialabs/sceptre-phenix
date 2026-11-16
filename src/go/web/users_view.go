package web

import "phenix/web/rbac"

// UserView is the user u as the users routes answer with it, without a proxy token.
func UserView(u rbac.User) User { return userFromRBAC(u) }

// RoleView is the role r as the roles routes answer with it, without its permissions.
func RoleView(r rbac.Role) Role { return roleView(r, false) }
