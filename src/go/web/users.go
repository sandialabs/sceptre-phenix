package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/api/settings"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
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

	var resp []User

	switch {
	case role.Allowed(resourceUsers, "list"):
		users, err := rbac.GetUsers()
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "")
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
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "")
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

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"creating users not allowed",
			"requester",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	var req CreateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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

	user, ok := createUser(w, req.Username, req.Password, req.FirstName, req.LastName)
	if !ok {
		return nil
	}

	_ = uRole.SetResourceNames(req.ResourceNames...)

	// allow user to get and update their own user details
	uRole.AddPolicy(
		[]string{resourceUsers},
		[]string{req.Username},
		[]string{verbGet, verbPatch},
	)

	_ = user.SetRole(uRole)

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

	requester, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeSecurity,
		"user created",
		"requested",
		requester,
		"user",
		req.Username,
	)

	//nolint:gosec // XSS via taint analysis
	_, _ = w.Write(body)

	return nil
}

// GetUser handles GET requests for /users/{username}.
func GetUser(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "GetUser")

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
		return weberror.New(http.StatusInternalServerError, "unable to get user")
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

	if !role.Allowed(resourceUsers, verbPatch, uname) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"updating users not allowed",
			"requester",
			user,
			"user",
			uname,
		)

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
		return weberror.New(http.StatusInternalServerError, "unable to get user")
	}

	if req.FirstName != "" {
		err := u.UpdateFirstName(req.FirstName)
		if err != nil {
			plog.Error(plog.TypeSystem, "updating first name for user", "user", uname, "err", err)

			return weberror.New(http.StatusInternalServerError, "unable to update user")
		}

		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeSecurity,
			"user's first name updated",
			"requester",
			user,
			"user",
			uname,
			"firstname",
			req.FirstName,
		)
	}

	if req.LastName != "" {
		err := u.UpdateLastName(req.LastName)
		if err != nil {
			plog.Error(plog.TypeSystem, "updating last name for user", "user", uname, "err", err)

			return weberror.New(http.StatusInternalServerError, "unable to update user")
		}

		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeSecurity,
			"user's last name updated",
			"requester",
			user,
			"user",
			uname,
			"lastname",
			req.LastName,
		)
	}

	if req.RoleName != "" && role.Allowed("users/roles", verbPatch, uname) {
		uRole, err := rbac.RoleFromConfig(req.RoleName)
		if err != nil {
			plog.Error(plog.TypeSystem, "role not found", "role", req.RoleName)

			return weberror.New(http.StatusBadRequest, "role not found")
		}

		_ = uRole.SetResourceNames(req.ResourceNames...)

		// allow user to get their own user details
		uRole.AddPolicy(
			[]string{resourceUsers},
			[]string{uname},
			[]string{verbGet, verbPatch},
		)

		_ = u.SetRole(uRole)

		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeSecurity,
			"user's role updated",
			"requester",
			user,
			"user",
			uname,
			"role",
			req.RoleName,
		)
	}

	if req.NewPassword != "" {
		if req.Password == "" {
			plog.Error(
				plog.TypeSecurity,
				"new password provided without old password",
				"user",
				uname,
			)

			return weberror.New(http.StatusBadRequest, "cannot change password without password")
		}

		if !settings.IsPasswordValid(req.NewPassword) {
			user, _ := ctx.Value(middleware.ContextKeyUser).(string)
			plog.Error(
				plog.TypeSecurity,
				"new password does not meet requirements",
				"requester",
				user,
			)

			errStr := "new password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()

			return weberror.New(http.StatusBadRequest, errStr)
		}

		err := u.UpdatePassword(req.Password, req.NewPassword)
		if err != nil {
			plog.Error(plog.TypeSecurity, "updating password for user", "user", uname, "err", err)

			return weberror.New(http.StatusBadRequest, "unable to update password")
		}

		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeSecurity,
			"user's password updated",
			"requester",
			user,
			"user",
			uname,
		)
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

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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

	if user == uname {
		return weberror.New(http.StatusForbidden, "you cannot delete your own user")
	}

	if !role.Allowed(resourceUsers, "delete", uname) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"deleting users not allowed",
			"requester",
			user,
			"user",
			uname,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := config.Delete("user/" + uname)
	if err != nil {
		plog.Error(plog.TypeSystem, "deleting user", "user", uname, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy(resourceUsers, "delete", uname),
		bt.NewResource("user", uname, "delete"),
		nil,
	)
	plog.Info(
		plog.TypeSecurity,
		"user deleted",
		"user",
		uname,
		"requester",
		user,
	)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// createUser creates and stores a user. When it cannot, it answers 409 for a
// name in use or 500 otherwise, and returns false.
func createUser(w http.ResponseWriter, username, password, firstName, lastName string) (*rbac.User, bool) {
	user, err := rbac.NewUser(username, password, firstName, lastName)
	if err == nil {
		return user, true
	}

	if errors.Is(err, rbac.ErrUserExists) {
		weberror.Error(w, "user "+username+" already exists", http.StatusConflict)

		return nil, false
	}

	plog.Error(plog.TypeSecurity, "creating user", "user", username, "err", err)
	weberror.Error(w, "error creating user", http.StatusInternalServerError)

	return nil, false
}
