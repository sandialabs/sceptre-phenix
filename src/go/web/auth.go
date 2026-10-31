package web

import (
	"encoding/json"
	"errors"
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
	"phenix/web/audit"
	"phenix/web/capability"
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

	msgSaveSessionToken = "failed to save session token"
)

// How a user signed in, as phenix.auth.method.
const (
	authPassword    = "password"
	authProxyHeader = "proxy-header"
	authProxyJWT    = "proxy-jwt"
)

// errInvalidCreds is the audited error of a sign-in with an unknown user or
// a wrong password.
var errInvalidCreds = errors.New("invalid credentials")

// authFields are the audit fields of a sign-in or sign-up by user with method.
func authFields(user, method string) audit.Fields {
	return audit.Fields{User: user, Extra: []any{"phenix.auth.method", method}}
}

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
		ctx    = r.Context()
		token  *jwt.Token
		method = authPassword
	)

	created := audit.Fields{User: req.Username, TargetUser: req.Username}

	// Will only be present when this function is called if proxy JWT is enabled.
	if userToken := ctx.Value(middleware.ContextKeyUser); userToken != nil {
		method = authProxyJWT
		token, _ = userToken.(*jwt.Token)
		claims, _ := token.Claims.(jwt.MapClaims)

		jwtUser, err := jwtutil.UsernameFromClaims(claims)
		if err != nil {
			audit.Failure(r, audit.UserCreate, "signing up failed", created, err)

			return weberror.New(http.StatusUnauthorized, "proxy user missing")
		}

		if req.Username != jwtUser {
			created.Reason = "proxy user mismatch"
			audit.Denied(r, audit.UserCreate, "signing up not allowed", created)

			return weberror.New(http.StatusUnauthorized, "proxy user mismatch")
		}
	} else if o.proxyAuthHeader != "" {
		method = authProxyHeader

		if user := r.Header.Get(o.proxyAuthHeader); user != req.Username {
			created.Reason = "proxy user mismatch"
			audit.Denied(r, audit.UserCreate, "signing up not allowed", created)

			return weberror.New(http.StatusUnauthorized, "proxy user mismatch")
		}
	}

	if !settings.IsPasswordValid(req.Password) {
		plog.Error(plog.TypeSystem, "password does not meet requirements")

		errStr := "password does not meet the requirements:\n" + settings.GetPasswordSettingsHTML()

		return weberror.New(http.StatusBadRequest, errStr)
	}

	u, err := rbac.NewUser(req.Username, req.Password, req.FirstName, req.LastName)
	if err != nil {
		audit.Failure(r, audit.UserCreate, "signing up failed", created, err)

		return createUserError(req.Username, err)
	}

	audit.Success(r, audit.UserCreate, "user signed up", created)

	var raw string

	if token == nil { // not using proxy JWT
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			jwtClaimSub: u.Username(),
			jwtClaimExp: time.Now().Add(o.jwtLifetime).Unix(),
		})

		// Sign and get the complete encoded token as a string using the secret
		raw, err = token.SignedString([]byte(o.jwtKey))
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(u.Username(), method), err)

			return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
		}

		err := u.AddToken(raw, time.Now().Format(time.RFC3339))
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(u.Username(), method), err)

			return weberror.New(http.StatusInternalServerError, msgSaveSessionToken)
		}
	} else { // using proxy JWT
		raw = token.Raw

		err := u.AddToken(raw, "proxied")
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(u.Username(), method), err)

			return weberror.New(http.StatusInternalServerError, msgSaveSessionToken)
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

	audit.Success(r, audit.SignIn, "user signed in", authFields(u.Username(), method))

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

//nolint:funlen // handler
func Login(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Login")

	var (
		user, pass string
		proxied    bool
		method     = authPassword
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
			audit.Failure(r, audit.SignIn, "signing in failed", authFields("", authProxyJWT), err)

			return weberror.New(http.StatusUnauthorized, "proxy user missing")
		}

		proxied = true
		method = authProxyJWT
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
					audit.Failure(r, audit.SignIn, "signing in failed", authFields("", authProxyHeader),
						errors.New("no proxy user"))

					return weberror.New(http.StatusUnauthorized, "proxy authentication failed")
				}

				proxied = true
				method = authProxyHeader
			}
		case http.MethodPost:
			if o.proxyAuthHeader != "" {
				return weberror.New(http.StatusBadRequest, "proxy auth is enabled: log in with a GET request")
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
		audit.Failure(r, audit.SignIn, "signing in failed", authFields(user, method), errInvalidCreds)

		// With proxy auth the proxy has already vouched for the name, and the
		// proxy sign-up page takes it from the metadata of this 404.
		if proxied {
			return weberror.New(http.StatusNotFound, "user not found").WithMetadata("username", user, true)
		}

		// Answer exactly as for a wrong password, after the same bcrypt work, so
		// the response does not reveal which usernames exist.
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(pass))

		return weberror.New(http.StatusUnauthorized, "invalid credentials")
	}

	if !proxied {
		err := u.ValidatePassword(pass)
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(user, method), errInvalidCreds)

			return weberror.New(http.StatusUnauthorized, "invalid credentials")
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
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(user, method), err)

			return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
		}

		err := u.AddToken(signed, time.Now().Format(time.RFC3339))
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(user, method), err)

			return weberror.New(http.StatusInternalServerError, msgSaveSessionToken)
		}
	} else {
		signed = token.Raw

		err := u.AddToken(signed, "proxied")
		if err != nil {
			audit.Failure(r, audit.SignIn, "signing in failed", authFields(user, method), err)

			return weberror.New(http.StatusInternalServerError, msgSaveSessionToken)
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

	audit.Success(r, audit.SignIn, "user signed in", authFields(u.Username(), method))

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// Logout - POST /logout.
func Logout(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "Logout")

	var (
		ctx      = r.Context()
		user, _  = ctx.Value(middleware.ContextKeyUser).(string)
		token, _ = ctx.Value(middleware.ContextKeyJWT).(string)
	)

	u, err := rbac.GetUser(user)
	if err != nil {
		audit.Failure(r, audit.SignOut, "signing out failed", audit.Fields{}, err)

		return weberror.New(http.StatusBadRequest, "cannot find user")
	}

	if err := u.DeleteToken(token); err != nil {
		audit.Failure(r, audit.SignOut, "signing out failed", audit.Fields{}, err)

		return weberror.New(http.StatusInternalServerError, "failed to delete session token")
	}

	// the capture grants given under the session end with it
	if token != "" {
		revokeCaptureGrants("signed out", func(g capability.Grant) bool {
			return g.User == u.Username() && g.Session == token
		}, "user", u.Username())
	}

	audit.Success(r, audit.SignOut, "user signed out", audit.Fields{})
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
		created = audit.Fields{TargetUser: uname}
	)

	if !role.Allowed("users", "patch", uname) {
		audit.Denied(r, audit.UserTokenCreate, "creating token not allowed", created)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	// a token for another user lets its holder act as that user
	if requester := middleware.UserFromContext(ctx); uname != requester && !role.Allowed("users/tokens", "create", uname) {
		audit.Denied(r, audit.UserTokenCreate, "creating token for another user not allowed", created)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		audit.Failure(r, audit.UserTokenCreate, "creating token failed", created, err)

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
	created.Extra = []any{"phenix.token.expires", exp.UTC().Format(time.RFC3339)}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		jwtClaimSub: u.Username(),
		"exp":       exp.Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		audit.Failure(r, audit.UserTokenCreate, "creating token failed", created, err)

		return weberror.New(http.StatusInternalServerError, "failed to sign JWT")
	}

	note := "manually generated - " + time.Now().Format(time.RFC3339)
	if desc := req.Description; desc != "" {
		note = req.Description
	}

	if err := u.AddToken(signed, note); err != nil {
		audit.Failure(r, audit.UserTokenCreate, "creating token failed", created, err)

		return weberror.New(http.StatusInternalServerError, "failed to save token")
	}

	resp := CreateTokenResponse{
		Token:       signed,
		Description: note,
		Expiration:  exp.Format(time.RFC3339),
	}

	body, _ = json.Marshal(resp)

	audit.Success(r, audit.UserTokenCreate, "created token", created)

	w.Header().Set("Content-Type", mimeJSON)
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

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
