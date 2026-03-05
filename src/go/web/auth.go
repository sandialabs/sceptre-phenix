package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"

	"phenix/api/settings"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	jwtutil "phenix/web/util/jwt"
)

const hoursInDay = 24

//nolint:funlen // handler
func Signup(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Signup")

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

	var req SignupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	var (
		ctx   = r.Context()
		token *jwt.Token
	)

	// Will only be present when this function is called if proxy JWT is enabled.
	if userToken := ctx.Value(middleware.ContextKeyUser); userToken != nil {
		token, _ = userToken.(*jwt.Token)
		claims, _ := token.Claims.(jwt.MapClaims)

		jwtUser, err := jwtutil.UsernameFromClaims(claims)
		if err != nil {
			plog.Error(
				plog.TypeSecurity,
				"proxy user missing from JWT",
				"path",
				r.URL.Path,
				"err",
				err,
			)
			http.Error(w, "proxy user missing", http.StatusUnauthorized)

			return
		}

		if req.Username != jwtUser {
			http.Error(w, "proxy user mismatch", http.StatusUnauthorized)

			return
		}
	} else if o.proxyAuthHeader != "" {
		if user := r.Header.Get(o.proxyAuthHeader); user != req.Username {
			http.Error(w, "proxy user mismatch", http.StatusUnauthorized)

			return
		}
	}

	if !settings.IsPasswordValid(req.Password) {
		plog.Error(plog.TypeSystem, "password does not meet requirements")

		errStr := "password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()
		http.Error(w, errStr, http.StatusBadRequest)

		return
	}

	u := rbac.NewUser(req.Username, req.Password)
	if u == nil {
		// can happen if username is the same as an existing user
		http.Error(w, "error creating user", http.StatusInternalServerError)

		return
	}

	u.Spec.FirstName = req.FirstName
	u.Spec.LastName = req.LastName

	var raw string

	if token == nil { // not using proxy JWT
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": u.Username(),
			"exp": time.Now().Add(o.jwtLifetime).Unix(),
		})

		// Sign and get the complete encoded token as a string using the secret
		raw, err = token.SignedString([]byte(o.jwtKey))
		if err != nil {
			http.Error(w, "failed to sign JWT", http.StatusInternalServerError)

			return
		}

		err := u.AddToken(raw, time.Now().Format(time.RFC3339))
		if err != nil {
			http.Error(w, "", http.StatusInternalServerError)

			return
		}
	} else { // using proxy JWT
		raw = token.Raw

		err := u.AddToken(raw, "proxied")
		if err != nil {
			http.Error(w, "", http.StatusInternalServerError)

			return
		}
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: raw,
	}

	body, err = json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(
		plog.TypeSecurity,
		"created user",
		"user",
		u.Username(),
		"role",
		u.RoleName(),
		"first_name",
		u.FirstName(),
		"last_name",
		u.LastName(),
	)

	_, _ = w.Write(body)
}

//nolint:funlen // handler
func Login(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Login")

	var (
		user, pass string
		proxied    bool
	)

	var (
		ctx   = r.Context()
		token *jwt.Token
	)

	// Will only be present when this function is called if proxy JWT is enabled.
	if userToken := ctx.Value(middleware.ContextKeyUser); userToken != nil {
		token, _ = userToken.(*jwt.Token)

		var (
			claims, _ = token.Claims.(jwt.MapClaims)
			err       error
		)

		user, err = jwtutil.UsernameFromClaims(claims)
		if err != nil {
			plog.Error(
				plog.TypeSecurity,
				"proxy user missing from JWT",
				"path",
				r.URL.Path,
				"token",
				token.Raw,
				"err",
				err,
			)
			http.Error(w, "proxy user missing", http.StatusUnauthorized)

			return
		}

		proxied = true
	} else {
		switch r.Method {
		case http.MethodGet:
			if o.proxyAuthHeader == "" {
				var ok bool

				user, pass, ok = r.BasicAuth()

				if !ok {
					query := r.URL.Query()

					user = query.Get("user")
					if user == "" {
						http.Error(w, "no username provided", http.StatusBadRequest)

						return
					}

					pass = query.Get("pass")
					if pass == "" {
						http.Error(w, "no password provided", http.StatusBadRequest)

						return
					}
				}
			} else {
				user = r.Header.Get(o.proxyAuthHeader)

				if user == "" {
					plog.Error(plog.TypeSecurity, "proxy authentication failed")
					http.Error(w, "proxy authentication failed", http.StatusUnauthorized)

					return
				}

				proxied = true
			}
		case http.MethodPost:
			if o.proxyAuthHeader != "" {
				http.Error(
					w,
					"proxy auth enabled -- must login via GET request",
					http.StatusBadRequest,
				)

				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "no data provided in POST", http.StatusBadRequest)

				return
			}

			var req LoginRequest
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "invalid data provided in POST", http.StatusBadRequest)

				return
			}

			if user = req.Username; user == "" {
				http.Error(w, "invalid username provided in POST", http.StatusBadRequest)

				return
			}

			if pass = req.Password; pass == "" {
				http.Error(w, "invalid password provided in POST", http.StatusBadRequest)

				return
			}
		default:
			http.Error(w, "invalid method", http.StatusBadRequest)

			return
		}
	}

	u, err := rbac.GetUser(user)
	if err != nil {
		plog.Error(plog.TypeSecurity, "attempted login with unknown username", "username", user)

		http.Error(w, user, http.StatusNotFound)

		return
	}

	if !proxied {
		err := u.ValidatePassword(pass)
		if err != nil {
			plog.Error(plog.TypeSecurity, "attempted login with invalid credentials", "user", user)
			http.Error(w, "invalid creds", http.StatusUnauthorized)

			return
		}
	}

	var signed string

	if token == nil {
		token = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": u.Username(),
			"exp": time.Now().Add(o.jwtLifetime).Unix(),
		})

		// Sign and get the complete encoded token as a string using the secret
		signed, err = token.SignedString([]byte(o.jwtKey))
		if err != nil {
			plog.Error(plog.TypeSecurity, "failed to sign JWT")
			http.Error(w, "failed to sign JWT", http.StatusInternalServerError)

			return
		}

		err := u.AddToken(signed, time.Now().Format(time.RFC3339))
		if err != nil {
			plog.Error(plog.TypeSecurity, "error adding token", "err", err)
			http.Error(w, "", http.StatusInternalServerError)

			return
		}
	} else {
		signed = token.Raw

		err := u.AddToken(signed, "proxied")
		if err != nil {
			plog.Error(plog.TypeSecurity, "error adding token", "err", err)
			http.Error(w, "", http.StatusInternalServerError)

			return
		}
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: signed,
	}

	body, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(plog.TypeSecurity, "user signed in", "user", u.Username())

	_, _ = w.Write(body)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Logout")

	var (
		ctx      = r.Context()
		user, _  = ctx.Value(middleware.ContextKeyUser).(string)
		token, _ = ctx.Value(middleware.ContextKeyJWT).(string)
	)

	u, err := rbac.GetUser(user)
	if err != nil {
		plog.Error(plog.TypeSecurity, "attempted logout with unknown username", "username", user)
		http.Error(w, "cannot find user", http.StatusBadRequest)

		return
	}

	if err := u.DeleteToken(token); err != nil {
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	plog.Info(plog.TypeSecurity, "user logged out", "user", u.Username())
	w.WriteHeader(http.StatusNoContent)
}

// CreateUserToken - POST /users/{username}/tokens.
//
//nolint:funlen // handler
func CreateUserToken(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateUserToken")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		uname   = vars["username"]
	)

	if !role.Allowed("users", "patch", uname) {
		plog.Warn(
			plog.TypeSecurity,
			"user forbidden to perform action",
			"username",
			uname,
			"action",
			"users/patch/"+uname,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		plog.Error(
			plog.TypeSecurity,
			"attempted to create token with unknown username",
			"username",
			uname,
		)
		http.Error(w, "unable to get user", http.StatusInternalServerError)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req CreateTokenRequest

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	dur, err := time.ParseDuration(req.Lifetime)
	if err != nil {
		days, err := strconv.Atoi(req.Lifetime)
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"invalid token lifetime provided",
				"username",
				uname,
				"duration",
				req.Lifetime,
			)
			http.Error(w, "invalid token lifetime provided", http.StatusBadRequest)

			return
		}

		dur = time.Duration(days) * hoursInDay * time.Hour
	}

	exp := time.Now().Add(dur)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": exp.Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)

		return
	}

	note := "manually generated - " + time.Now().Format(time.RFC3339)
	if desc := req.Description; desc != "" {
		note = req.Description
	}

	if err := u.AddToken(signed, note); err != nil {
		plog.Error(plog.TypeSecurity, "error adding token", "err", err)
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	resp := CreateTokenResponse{
		Token:       signed,
		Description: note,
		Expiration:  exp.Format(time.RFC3339),
	}

	body, _ = json.Marshal(resp)

	plog.Info(plog.TypeSecurity, "create token for user", "user", u.Username())

	_, _ = w.Write(body)
}

// GetRoles - GET /roles.
func GetRoles(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetRoles")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("roles", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSecurity,
			"listing roles not allowed",
			"username",
			user,
		)
		http.Error(w, "forbidden to list roles", http.StatusForbidden)

		return
	}

	var resp []Role

	roles, err := rbac.GetRoles()
	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving roles", "err", err)
		http.Error(w, "error retrieving roles", http.StatusInternalServerError)

		return
	}

	for _, r := range roles {
		resp = append(resp, roleFromRBAC(*r))
	}

	body, err := json.Marshal(util.WithRoot("roles", resp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling roles", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body)
}
