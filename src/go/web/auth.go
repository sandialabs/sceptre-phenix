package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"

	"phenix/api/settings"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	jwtutil "phenix/web/util/jwt"
	"phenix/web/weberror"
)

// dummyPasswordHash returns a hash to compare against when a login names an
// unknown user, so that the request takes as long as one with a wrong password.
// It is computed on first use rather than at startup, which every phenix
// command, not only the server, would otherwise pay for.
var dummyPasswordHash = sync.OnceValue(func() []byte { //nolint:gochecknoglobals // computed once
	hash, _ := bcrypt.GenerateFromPassword([]byte("phenix-dummy-password"), bcrypt.DefaultCost)

	return hash
})

const (
	hoursInDay  = 24
	jwtClaimSub = "sub"
	jwtClaimExp = "exp"
)

//nolint:funlen // handler
func Signup(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Signup")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	var req SignupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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

			return weberror.New(http.StatusUnauthorized, "proxy user missing")
		}

		if req.Username != jwtUser {
			return weberror.New(http.StatusUnauthorized, "proxy user mismatch")
		}
	} else if o.proxyAuthHeader != "" {
		if user := r.Header.Get(o.proxyAuthHeader); user != req.Username {
			return weberror.New(http.StatusUnauthorized, "proxy user mismatch")
		}
	}

	if !settings.IsPasswordValid(req.Password) {
		plog.Error(plog.TypeSystem, "password does not meet requirements")

		errStr := "password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()

		return weberror.New(http.StatusBadRequest, errStr)
	}

	u, ok := createUser(w, req.Username, req.Password, req.FirstName, req.LastName)
	if !ok {
		return nil
	}

	var raw string

	if token == nil { // not using proxy JWT
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			jwtClaimSub: u.Username(),
			jwtClaimExp: time.Now().Add(o.jwtLifetime).Unix(),
		})

		// Sign and get the complete encoded token as a string using the secret
		raw, err = token.SignedString([]byte(o.jwtKey))
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
		}

		err := u.AddToken(raw, time.Now().Format(time.RFC3339))
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "")
		}
	} else { // using proxy JWT
		raw = token.Raw

		err := u.AddToken(raw, "proxied")
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "")
		}
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: raw,
	}

	body, err = json.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
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

	return nil
}

//nolint:funlen // handler
func Login(w http.ResponseWriter, r *http.Request) error {
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

			return weberror.New(http.StatusUnauthorized, "proxy user missing")
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
						return weberror.New(http.StatusBadRequest, "no username provided")
					}

					pass = query.Get("pass")
					if pass == "" {
						return weberror.New(http.StatusBadRequest, "no password provided")
					}
				}
			} else {
				user = r.Header.Get(o.proxyAuthHeader)

				if user == "" {
					plog.Error(plog.TypeSecurity, "proxy authentication failed")

					return weberror.New(http.StatusUnauthorized, "proxy authentication failed")
				}

				proxied = true
			}
		case http.MethodPost:
			if o.proxyAuthHeader != "" {
				return weberror.New(http.StatusBadRequest, "proxy auth enabled -- must login via GET request")
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				return weberror.New(http.StatusBadRequest, "no data provided in POST")
			}

			var req LoginRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return weberror.New(http.StatusBadRequest, "invalid data provided in POST")
			}

			if user = req.Username; user == "" {
				return weberror.New(http.StatusBadRequest, "invalid username provided in POST")
			}

			if pass = req.Password; pass == "" {
				return weberror.New(http.StatusBadRequest, "invalid password provided in POST")
			}
		default:
			return weberror.New(http.StatusBadRequest, "invalid method")
		}
	}

	u, err := rbac.GetUser(user)
	if err != nil {
		plog.Error(plog.TypeSecurity, "attempted login with unknown username", "username", user)

		// With proxy auth the proxy has already vouched for the name, and the
		// proxy sign-up page takes it from the metadata of this 404.
		if proxied {
			return weberror.New(http.StatusNotFound, "user not found").WithMetadata("username", user, true)
		}

		// Answer exactly as for a wrong password, after the same bcrypt work, so
		// the response does not reveal which usernames exist.
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(pass))

		return weberror.New(http.StatusUnauthorized, "invalid creds")
	}

	if !proxied {
		err := u.ValidatePassword(pass)
		if err != nil {
			plog.Error(plog.TypeSecurity, "attempted login with invalid credentials", "user", user)

			return weberror.New(http.StatusUnauthorized, "invalid creds")
		}
	}

	var signed string

	if token == nil {
		token = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			jwtClaimSub: u.Username(),
			jwtClaimExp: time.Now().Add(o.jwtLifetime).Unix(),
		})

		// Sign and get the complete encoded token as a string using the secret
		signed, err = token.SignedString([]byte(o.jwtKey))
		if err != nil {
			plog.Error(plog.TypeSecurity, "failed to sign JWT")

			return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
		}

		err := u.AddToken(signed, time.Now().Format(time.RFC3339))
		if err != nil {
			plog.Error(plog.TypeSecurity, "error adding token", "err", err)

			return weberror.New(http.StatusInternalServerError, "")
		}
	} else {
		signed = token.Raw

		err := u.AddToken(signed, "proxied")
		if err != nil {
			plog.Error(plog.TypeSecurity, "error adding token", "err", err)

			return weberror.New(http.StatusInternalServerError, "")
		}
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: signed,
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	plog.Info(plog.TypeSecurity, "user signed in", "user", u.Username())

	_, _ = w.Write(body)

	return nil
}

func Logout(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Logout")

	var (
		ctx      = r.Context()
		user, _  = ctx.Value(middleware.ContextKeyUser).(string)
		token, _ = ctx.Value(middleware.ContextKeyJWT).(string)
	)

	u, err := rbac.GetUser(user)
	if err != nil {
		plog.Error(plog.TypeSecurity, "attempted logout with unknown username", "username", user)

		return weberror.New(http.StatusBadRequest, "cannot find user")
	}

	if err := u.DeleteToken(token); err != nil {
		return weberror.New(http.StatusInternalServerError, "")
	}

	plog.Info(plog.TypeSecurity, "user logged out", "user", u.Username())
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// CreateUserToken - POST /users/{username}/tokens.
func CreateUserToken(w http.ResponseWriter, r *http.Request) error {
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

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		plog.Error(
			plog.TypeSecurity,
			"attempted to create token with unknown username",
			"username",
			uname,
		)

		return weberror.New(http.StatusInternalServerError, "unable to get user")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req CreateTokenRequest

	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
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

			return weberror.New(http.StatusBadRequest, "invalid token lifetime provided")
		}

		dur = time.Duration(days) * hoursInDay * time.Hour
	}

	exp := time.Now().Add(dur)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		jwtClaimSub: u.Username(),
		"exp":       exp.Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
	}

	note := "manually generated - " + time.Now().Format(time.RFC3339)
	if desc := req.Description; desc != "" {
		note = req.Description
	}

	if err := u.AddToken(signed, note); err != nil {
		plog.Error(plog.TypeSecurity, "error adding token", "err", err)

		return weberror.New(http.StatusInternalServerError, "")
	}

	resp := CreateTokenResponse{
		Token:       signed,
		Description: note,
		Expiration:  exp.Format(time.RFC3339),
	}

	body, _ = json.Marshal(resp)

	plog.Info(plog.TypeSecurity, "create token for user", "user", u.Username())

	_, _ = w.Write(body)

	return nil
}

// GetRoles - GET /roles.
func GetRoles(w http.ResponseWriter, r *http.Request) error {
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

		return weberror.New(http.StatusForbidden, "forbidden to list roles")
	}

	var resp []Role

	roles, err := rbac.GetRoles()
	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving roles", "err", err)

		return weberror.New(http.StatusInternalServerError, "error retrieving roles")
	}

	for _, r := range roles {
		resp = append(resp, roleFromRBAC(*r))
	}

	body, err := json.Marshal(util.WithRoot("roles", resp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling roles", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	_, _ = w.Write(body)

	return nil
}
