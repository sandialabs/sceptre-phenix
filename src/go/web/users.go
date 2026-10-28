package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/api/settings"
	"phenix/store"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/capability"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetUsers handles GET requests for /users.
func GetUsers(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetUsers")

	var (
		ctx      = r.Context()
		uname, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role, _  = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	resp := []User{}

	switch {
	case role.Allowed(resourceUsers, "list"):
		users, err := rbac.GetUsers()
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "unable to list users")
		}

		for _, rbacUser := range users {
			if role.Allowed(resourceUsers, "list", rbacUser.Username()) {
				user := userFromRBAC(*rbacUser)

				if rbacUser.Username() == uname {
					user.ProxyToken = rbacUser.GetProxyToken()
				}

				resp = append(resp, user)
			}
		}
	case role.Allowed(resourceUsers, verbGet, uname):
		rbacUser, err := rbac.GetUser(uname)
		if errors.Is(err, store.ErrNotExist) {
			// a requester without a stored user, as with dev auth, lists no users
			break
		} else if err != nil {
			return weberror.New(http.StatusInternalServerError, "unable to get user")
		}

		user := userFromRBAC(*rbacUser)
		user.ProxyToken = rbacUser.GetProxyToken()

		resp = append(resp, user)
	default:
		plog.Warn(plog.TypeSecurity, "getting users not allowed", "user", uname)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := json.Marshal(util.WithRoot(resourceUsers, resp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling users", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// CreateUser - POST /users.
func CreateUser(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateUser")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed(resourceUsers, "create") {
		audit.Denied(r, audit.UserCreate, "creating users not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	var req CreateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if !settings.IsPasswordValid(req.Password) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSystem,
			"password does not meet requirements",
			"requester",
			user,
		)

		errStr := "password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()

		return weberror.New(http.StatusBadRequest, errStr)
	}

	// The role is found before the user is created, so a request naming no
	// role creates no user.
	uRole, err := rbac.RoleFromConfig(req.RoleName)
	if err != nil {
		plog.Error(plog.TypeSystem, "role not found", "role", req.RoleName)

		return weberror.New(http.StatusBadRequest, "role not found")
	}

	_ = uRole.SetResourceNames(req.ResourceNames...)

	// allow user to get and update their own user details
	uRole.AddPolicy(
		[]string{resourceUsers},
		[]string{req.Username},
		[]string{verbGet, verbPatch},
	)

	created := audit.Fields{TargetUser: req.Username, Extra: []any{"user.target.roles", []string{req.RoleName}}}

	user, err := rbac.NewUser(req.Username, req.Password, req.FirstName, req.LastName)
	if err != nil {
		audit.Failure(r, audit.UserCreate, "creating user failed", created, err)

		return createUserError(req.Username, err)
	}

	if err := user.SetRole(uRole); err != nil {
		plog.Error(plog.TypeSystem, "setting user role", "user", req.Username, "err", err)

		// do not leave a user without a role behind
		_ = config.Delete("user/" + req.Username)

		audit.Failure(r, audit.UserCreate, "creating user failed", created, err)

		return weberror.New(http.StatusInternalServerError, "error setting user role")
	}

	resp := userFromRBAC(*user)

	body, err = json.Marshal(resp)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling user", "user", user.Username(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy(resourceUsers, "create", ""),
		bt.NewResource("user", req.Username, "create"),
		body,
	)

	audit.Success(r, audit.UserCreate, "user created", created)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetUser handles GET requests for /users/{username}.
func GetUser(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetUser")

	var (
		ctx      = r.Context()
		uname, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role, _  = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars     = mux.Vars(r)
		username = vars["username"]
	)

	if !role.Allowed(resourceUsers, verbGet, username) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting users not allowed",
			"requester",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	rbacUser, err := rbac.GetUser(username)
	if err != nil {
		return userLookupError(err)
	}

	user := userFromRBAC(*rbacUser)

	if rbacUser.Username() == uname {
		user.ProxyToken = rbacUser.GetProxyToken()
	}

	body, err := json.Marshal(user)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling user", "user", rbacUser.Username(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// UpdateUser - PATCH /users/{username}.
//
//nolint:funlen // handler
func UpdateUser(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateUser")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		uname   = vars["username"]
	)

	target := audit.Fields{TargetUser: uname}

	if !role.Allowed(resourceUsers, verbPatch, uname) {
		audit.Denied(r, audit.UserUpdate, "updating user not allowed", target)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req UpdateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		audit.Failure(r, audit.UserUpdate, "updating user failed", target, err)

		return userLookupError(err)
	}

	// Every part of the change is checked before the first write, so a
	// refused request changes nothing.
	if req.RoleName != "" && req.RoleName != u.RoleName() && !role.Allowed("users/roles", verbPatch, uname) {
		// the role is left as it is; the UI sends the current role with every edit
		audit.Denied(r, audit.UserRoleChange, "changing user's role not allowed", target)
	}

	var uRole *rbac.Role

	if req.RoleName != "" && role.Allowed("users/roles", verbPatch, uname) {
		if uRole, err = rbac.RoleFromConfig(req.RoleName); err != nil {
			plog.Error(plog.TypeSystem, "role not found", "role", req.RoleName)

			return weberror.New(http.StatusBadRequest, "role not found")
		}
	}

	if req.NewPassword != "" {
		if err := checkPasswordChange(r, u, req); err != nil {
			return err
		}
	}

	if req.FirstName != "" || req.LastName != "" {
		if err := updateUserNames(r, u, req); err != nil {
			return err
		}
	}

	if uRole != nil {
		_ = uRole.SetResourceNames(req.ResourceNames...)

		// allow user to get their own user details
		uRole.AddPolicy(
			[]string{resourceUsers},
			[]string{uname},
			[]string{verbGet, verbPatch},
		)

		roleChanged := audit.Fields{TargetUser: uname, Extra: []any{"user.target.roles", []string{req.RoleName}}}

		if err := u.SetRole(uRole); err != nil {
			plog.Error(plog.TypeSystem, "setting role for user", "user", uname, "err", err)
			audit.Failure(r, audit.UserRoleChange, "changing user's role failed", roleChanged, err)

			return weberror.New(http.StatusInternalServerError, "unable to update user role")
		}

		audit.Success(r, audit.UserRoleChange, "user's role changed", roleChanged)
	}

	if req.NewPassword != "" {
		if err := u.UpdatePassword(req.Password, req.NewPassword); err != nil {
			plog.Error(plog.TypeSystem, "updating password for user", "user", uname, "err", err)
			audit.Failure(r, audit.UserPasswordChange, "changing user's password failed", target, err)

			return weberror.New(http.StatusInternalServerError, "unable to update password")
		}

		audit.Success(r, audit.UserPasswordChange, "user's password changed", target)
	}

	resp := userFromRBAC(*u)

	body, err = json.Marshal(resp)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling user", "user", uname, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy(resourceUsers, verbPatch, uname),
		bt.NewResource("user", uname, "update"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// checkPasswordChange returns the 400 to answer when req, which sets a new
// password, may not change the password of u: the current password is
// missing or wrong, or the new one does not meet the password rules.
func checkPasswordChange(r *http.Request, u *rbac.User, req UpdateUserRequest) error {
	if req.Password == "" {
		return weberror.New(http.StatusBadRequest, "cannot change password without the current password")
	}

	if !settings.IsPasswordValid(req.NewPassword) {
		errStr := "new password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()

		return weberror.New(http.StatusBadRequest, errStr)
	}

	if err := u.ValidatePassword(req.Password); err != nil {
		target := audit.Fields{TargetUser: u.Username()}
		audit.Failure(r, audit.UserPasswordChange, "changing user's password failed", target, err)

		return weberror.New(http.StatusBadRequest, "unable to update password")
	}

	return nil
}

// userLookupError is the answer to err, from rbac.GetUser: 404 when the user
// does not exist, else 500.
func userLookupError(err error) *weberror.WebError {
	if errors.Is(err, store.ErrNotExist) {
		return weberror.New(http.StatusNotFound, "user not found")
	}

	plog.Error(plog.TypeSystem, "getting user", "err", err)

	return weberror.New(http.StatusInternalServerError, "unable to get user")
}

// updateUserNames sets the first and last names req gives the user, or returns
// the error to answer when that fails.
func updateUserNames(r *http.Request, u *rbac.User, req UpdateUserRequest) error {
	var (
		uname   = u.Username()
		updated = audit.Fields{TargetUser: uname}
		changed []string
	)

	if req.FirstName != "" {
		if err := u.UpdateFirstName(req.FirstName); err != nil {
			plog.Error(plog.TypeSystem, "updating first name for user", "user", uname, "err", err)
			audit.Failure(r, audit.UserUpdate, "updating user failed", updated, err)

			return weberror.New(http.StatusInternalServerError, "unable to update user")
		}

		changed = append(changed, "first_name")
	}

	if req.LastName != "" {
		if err := u.UpdateLastName(req.LastName); err != nil {
			plog.Error(plog.TypeSystem, "updating last name for user", "user", uname, "err", err)
			audit.Failure(r, audit.UserUpdate, "updating user failed", updated, err)

			return weberror.New(http.StatusInternalServerError, "unable to update user")
		}

		changed = append(changed, "last_name")
	}

	updated.Extra = []any{"phenix.user.changed", changed}
	audit.Success(r, audit.UserUpdate, "user updated", updated)

	return nil
}

// DeleteUser - DELETE /users/{username}.
func DeleteUser(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeleteUser")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		uname   = vars["username"]
	)

	deleted := audit.Fields{TargetUser: uname}

	if user == uname {
		deleted.Reason = "own user"
		audit.Denied(r, audit.UserDelete, "deleting own user not allowed", deleted)

		return weberror.New(http.StatusForbidden, "you cannot delete your own user")
	}

	if !role.Allowed(resourceUsers, "delete", uname) {
		audit.Denied(r, audit.UserDelete, "deleting user not allowed", deleted)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := config.Delete("user/" + uname)
	if err != nil {
		plog.Error(plog.TypeSystem, "deleting user", "user", uname, "err", err)
		audit.Failure(r, audit.UserDelete, "deleting user failed", deleted, err)

		if errors.Is(err, store.ErrNotExist) {
			return weberror.New(http.StatusNotFound, "user not found")
		}

		return weberror.New(http.StatusInternalServerError, "unable to delete user")
	}

	revokeCaptureGrants("user deleted", func(g capability.Grant) bool { return g.User == uname }, "user", uname)

	broker.Broadcast(
		bt.NewRequestPolicy(resourceUsers, "delete", uname),
		bt.NewResource("user", uname, "delete"),
		nil,
	)
	audit.Success(r, audit.UserDelete, "user deleted", deleted)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// createUserError is the answer to err, from rbac.NewUser for username: 400 for
// an invalid name, 409 for a name in use, 500 otherwise.
func createUserError(username string, err error) *weberror.WebError {
	if errors.Is(err, rbac.ErrInvalidUsername) {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if errors.Is(err, rbac.ErrUserExists) {
		return weberror.New(http.StatusConflict, "user "+username+" already exists")
	}

	plog.Error(plog.TypeSecurity, "creating user", "user", username, "err", err)

	return weberror.New(http.StatusInternalServerError, "error creating user")
}
