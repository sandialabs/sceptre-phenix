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
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/capability"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// GetUsers handles GET requests for /users.
func GetUsers(w http.ResponseWriter, r *http.Request) {
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
			http.Error(w, "", http.StatusInternalServerError)

			return
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
			http.Error(w, "", http.StatusInternalServerError)

			return
		}

		user := userFromRBAC(*rbacUser)
		user.ProxyToken = rbacUser.GetProxyToken()

		resp = append(resp, user)
	default:
		plog.Warn(plog.TypeSecurity, "getting users not allowed", "user", uname)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := json.Marshal(util.WithRoot(resourceUsers, resp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling users", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// CreateUser - POST /users.
//
//nolint:funlen // handler
func CreateUser(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	var req CreateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
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
		http.Error(w, errStr, http.StatusBadRequest)

		return
	}

	// check the role before storing anything, so an unknown role leaves no user
	uRole, err := rbac.RoleFromConfig(req.RoleName)
	if err != nil {
		plog.Error(plog.TypeSystem, "role not found", "role", req.RoleName)
		http.Error(w, "role not found", http.StatusBadRequest)

		return
	}

	_ = uRole.SetResourceNames(req.ResourceNames...)

	// allow user to get and update their own user details
	uRole.AddPolicy(
		[]string{resourceUsers},
		[]string{req.Username},
		[]string{verbGet, verbPatch},
	)

	user, err := rbac.CreateUser(req.Username, req.Password)
	if errors.Is(err, store.ErrExist) {
		http.Error(w, "user already exists", http.StatusConflict)

		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "creating user", "user", req.Username, "err", err)
		http.Error(w, "error creating user", http.StatusInternalServerError)

		return
	}

	user.Spec.FirstName = req.FirstName
	user.Spec.LastName = req.LastName

	if err := user.SetRole(uRole); err != nil {
		plog.Error(plog.TypeSystem, "setting user role", "user", req.Username, "err", err)

		// do not leave a user without a role behind
		_ = config.Delete("user/" + req.Username)

		http.Error(w, "error setting user role", http.StatusInternalServerError)

		return
	}

	resp := userFromRBAC(*user)

	body, err = json.Marshal(resp)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling user", "user", user.Username(), "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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

	w.Header().Set("Content-Type", mimeJSON)
	//nolint:gosec // XSS via taint analysis
	_, _ = w.Write(body)
}

// GetUser handles GET requests for /users/{username}.
func GetUser(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	rbacUser, err := rbac.GetUser(username)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)

		return
	}

	user := userFromRBAC(*rbacUser)

	if rbacUser.Username() == uname {
		user.ProxyToken = rbacUser.GetProxyToken()
	}

	body, err := json.Marshal(user)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling user", "user", rbacUser.Username(), "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)
}

// UpdateUser - PATCH /users/{username}.
//
//nolint:funlen // handler
func UpdateUser(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req UpdateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)

		return
	}

	if req.FirstName != "" {
		err := u.UpdateFirstName(req.FirstName)
		if err != nil {
			plog.Error(plog.TypeSystem, "updating first name for user", "user", uname, "err", err)
			http.Error(w, "unable to update user", http.StatusInternalServerError)

			return
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
			http.Error(w, "unable to update user", http.StatusInternalServerError)

			return
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
			http.Error(w, "role not found", http.StatusBadRequest)

			return
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
			http.Error(w, "cannot change password without password", http.StatusBadRequest)

			return
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
			http.Error(w, errStr, http.StatusBadRequest)

			return
		}

		err := u.UpdatePassword(req.Password, req.NewPassword)
		if err != nil {
			plog.Error(plog.TypeSecurity, "updating password for user", "user", uname, "err", err)
			http.Error(w, "unable to update password", http.StatusBadRequest)

			return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy(resourceUsers, verbPatch, uname),
		bt.NewResource("user", uname, "update"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// DeleteUser - DELETE /users/{username}.
func DeleteUser(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeleteUser")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		uname   = vars["username"]
	)

	if user == uname {
		http.Error(w, "you cannot delete your own user", http.StatusForbidden)

		return
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := config.Delete("user/" + uname)
	if err != nil {
		plog.Error(plog.TypeSystem, "deleting user", "user", uname, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	revokeCaptureGrants("user deleted", func(g capability.Grant) bool { return g.User == uname }, "user", uname)

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
}
