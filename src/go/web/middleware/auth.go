package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	jwtmiddleware "github.com/cescoferraro/go-jwt-middleware"
	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"

	"phenix/util/plog"
	"phenix/web/rbac"
	jwtutil "phenix/web/util/jwt"
)

type ContextKey string

const (
	ContextKeyUser ContextKey = "user"
	ContextKeyRole ContextKey = "role"
	ContextKeyJWT  ContextKey = "jwt"
)

// RoleFromContext safely extracts the rbac.Role from the given context.
// If the role is missing or the wrong type, it returns an empty/default rbac.Role.
func RoleFromContext(ctx context.Context) rbac.Role {
	val := ctx.Value(ContextKeyRole)

	role, ok := val.(rbac.Role)
	if !ok {
		// Returns a zero-value rbac.Role if the assertion fails.
		// This prevents the application from panicking.
		var emptyRole rbac.Role
		return emptyRole
	}

	return role
}

// UserFromContext safely extracts the username string from the given context.
func UserFromContext(ctx context.Context) string {
	val := ctx.Value(ContextKeyUser)

	user, ok := val.(string)
	if !ok {
		return ""
	}

	return user
}

// JWTFromContext safely extracts the raw JWT string from the given context.
func JWTFromContext(ctx context.Context) string {
	val := ctx.Value(ContextKeyJWT)

	jwt, ok := val.(string)
	if !ok {
		return ""
	}

	return jwt
}

func fromPhenixAuthTokenHeader(r *http.Request) (string, error) {
	authHeader := r.Header.Get("X-Phenix-Auth-Token")
	if authHeader == "" {
		return "", nil // No error, just no token
	}

	authHeaderParts := strings.Split(authHeader, " ")
	if len(authHeaderParts) != 2 || strings.ToLower(authHeaderParts[0]) != "bearer" {
		return "", errors.New("x-phenix-auth-token header format must be 'Bearer {token}'")
	}

	return authHeaderParts[1], nil
}

// fixedRoleTTL bounds how long the no-auth and dev-auth modes reuse a role
// looked up from the store. Those modes use one role for every request, so
// listing every role config per request is wasted work, but an edited role
// config should still take effect without a restart.
const fixedRoleTTL = 30 * time.Second

type cachedRole struct {
	role    rbac.Role
	fetched time.Time
}

//nolint:gochecknoglobals // process-wide cache for the fixed no-auth/dev-auth roles
var (
	fixedRolesMu sync.Mutex
	fixedRoles   = make(map[string]cachedRole)
)

// fixedRole returns the named role, reusing a recent lookup. Failed lookups are
// not cached.
func fixedRole(name string) (rbac.Role, error) {
	fixedRolesMu.Lock()
	defer fixedRolesMu.Unlock()

	if c, ok := fixedRoles[name]; ok && time.Since(c.fetched) < fixedRoleTTL {
		return c.role, nil
	}

	role, err := rbac.RoleFromConfig(name)
	if err != nil {
		return rbac.Role{}, fmt.Errorf("getting role %s: %w", name, err)
	}

	fixedRoles[name] = cachedRole{role: *role, fetched: time.Now()}

	return *role, nil
}

func NoAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, err := fixedRole("global-admin")
		if err != nil {
			plog.Error(plog.TypeSecurity, "getting role for unauthenticated request", "err", err)
			http.Error(w, "role error", http.StatusInternalServerError)

			return
		}

		ctx := r.Context()

		ctx = context.WithValue(ctx, ContextKeyUser, "global-admin")
		ctx = context.WithValue(ctx, ContextKeyRole, role)

		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

//nolint:funlen // middleware
func Auth(jwtKey, proxyAuthHeader string) mux.MiddlewareFunc {
	tokenMiddleware := jwtmiddleware.New(
		jwtmiddleware.Options{ //nolint:exhaustruct // partial initialization
			// Setting this to true since some resource paths don't require
			// authentication. Those that do will be caught in the
			// userMIddleware, which will also check for a `user` context
			// value being present, which is only set if valid credentials
			// were presented.
			CredentialsOptional: true,
			// Most calls to the API will include the JWT in the X-phenix-auth-token
			// header. However, calls for screenshots and VNC will need the JWT in the
			// URL since they'll be in browser links and image tags.
			// Note that we're not using the default Authorization header to allow for
			// proxy authentication via basic auth (or other means of proxy
			// authentication that might end up overwriting the Authorization header).
			Extractor: jwtmiddleware.FromFirst(
				fromPhenixAuthTokenHeader,
				jwtmiddleware.FromParameter("token"),
			),
			ValidationKeyGetter: func(_ *jwt.Token) (any, error) {
				return []byte(jwtKey), nil
			},
			SigningMethod: jwt.SigningMethodHS256,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, e string) {
				plog.Error(plog.TypeSecurity, "validating auth token", "err", e)

				// TODO: remove token from user spec?

				http.Error(w, e, http.StatusUnauthorized)
			},
		},
	)

	validTokenMiddleware := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := fromPhenixAuthTokenHeader(r)
			if err != nil {
				plog.Error(
					plog.TypeSecurity,
					"getting raw JWT from X-phenix-auth-token header",
					"err",
					err,
				)

				http.Error(w, "missing phenix auth token header", http.StatusBadRequest)

				return
			}

			token, _, err := new(jwt.Parser).ParseUnverified(raw, jwt.MapClaims{})
			if err != nil {
				plog.Error(plog.TypeSecurity, "parsing valid JWT", "token", raw, "err", err)

				http.Error(w, "parsing auth token", http.StatusBadRequest)

				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, ContextKeyUser, token)

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	userMiddleware := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/signup") {
				h.ServeHTTP(w, r)

				return
			}

			if strings.HasSuffix(r.URL.Path, "/login") {
				h.ServeHTTP(w, r)

				return
			}

			ctx := r.Context()

			userToken := userTokenFromContext(ctx)
			if userToken == nil {
				plog.Warn(
					plog.TypeSecurity,
					"rejecting unauthorized request - missing user token",
					"path",
					r.URL.Path,
				)
				http.Error(w, "Forbidden", http.StatusForbidden)

				return
			}

			var (
				token, _ = userToken.(*jwt.Token)
				claims   = token.Claims.(jwt.MapClaims)
			)

			jwtUser, err := jwtutil.UsernameFromClaims(claims)
			if err != nil {
				plog.Warn(
					plog.TypeSecurity,
					"rejecting unauthorized request",
					"path",
					r.URL.Path,
					"err",
					err,
				)
				http.Error(w, "Forbidden", http.StatusUnauthorized)

				return
			}

			if proxyAuthHeader != "" {
				if user := r.Header.Get(proxyAuthHeader); user != jwtUser {
					plog.Error(
						plog.TypeSecurity,
						"proxy user mismatch",
						"user",
						user,
						"token",
						jwtUser,
					)
					http.Error(w, "proxy user mismatch", http.StatusUnauthorized)

					return
				}
			}

			user, err := rbac.GetUser(jwtUser)
			if err != nil {
				http.Error(w, "user error", http.StatusUnauthorized)

				return
			}

			// Check to see that the token is still associated w/ the user (ie. the
			// user didn't delete it because it became compromised).
			if err := user.ValidateToken(token.Raw); err != nil {
				http.Error(w, "user token error", http.StatusUnauthorized)

				return
			}

			role, err := user.Role()
			if err != nil {
				http.Error(w, "user role error", http.StatusUnauthorized)

				return
			}

			ctx = context.WithValue(ctx, ContextKeyUser, user.Username())
			ctx = context.WithValue(ctx, ContextKeyRole, role)
			ctx = context.WithValue(ctx, ContextKeyJWT, token.Raw)

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	// For testing, treats all requests as coming from a given user/role.
	// To use start with jwt key of format "dev|<username>|<role>"
	// e.g., "dev|testuser|global-viewer"
	devAuthMiddleware := func(h http.Handler) http.Handler {
		creds := strings.Split(jwtKey, "|")

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			role, err := fixedRole(creds[2])
			if err != nil {
				plog.Error(plog.TypeSecurity, "getting role for dev auth", "err", err)
				http.Error(w, "role error", http.StatusInternalServerError)

				return
			}

			ctx = context.WithValue(ctx, ContextKeyUser, creds[1])
			ctx = context.WithValue(ctx, ContextKeyRole, role)

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	switch {
	case jwtKey == "":
		plog.Info(plog.TypeSecurity, "no JWT signing key provided -- disabling auth")

		return NoAuth
	case jwtKey == "proxy-jwt":
		plog.Info(plog.TypeSecurity, "using JWTs from proxy")

		return func(h http.Handler) http.Handler { return validTokenMiddleware(userMiddleware(h)) }
	case strings.HasPrefix(jwtKey, "dev|"):
		plog.Debug(plog.TypeSecurity, "development JWT key provided -- enabling dev auth")

		return func(h http.Handler) http.Handler { return devAuthMiddleware(h) }
	}

	// First validate the token itself, then ensure the user in the token is valid.
	return func(h http.Handler) http.Handler { return tokenMiddleware.Handler(userMiddleware(h)) }
}

// ResolveRole looks up again the role of user, who signed in with the session
// JWT rawJWT, in the auth mode the JWT signing key jwtKey selects, as Auth
// does for each request: with sign-in disabled, every user is global-admin;
// dev auth gives its one user its role; otherwise the user must still exist
// and still hold the session (not have signed out or deleted it), and gets
// the role it has now. It does not check the JWT's signature or expiry,
// which Auth checked when it was presented.
func ResolveRole(jwtKey, user, rawJWT string) (rbac.Role, error) {
	switch {
	case jwtKey == "":
		return fixedRole("global-admin")
	case strings.HasPrefix(jwtKey, "dev|"):
		creds := strings.Split(jwtKey, "|")
		if len(creds) != 3 || user != creds[1] {
			return rbac.Role{}, fmt.Errorf("user %s is not the dev auth user", user)
		}

		return fixedRole(creds[2])
	}

	u, err := rbac.GetUser(user)
	if err != nil {
		return rbac.Role{}, fmt.Errorf("getting user %s: %w", user, err)
	}

	if err := u.ValidateToken(rawJWT); err != nil {
		return rbac.Role{}, fmt.Errorf("checking session of user %s: %w", user, err)
	}

	role, err := u.Role()
	if err != nil {
		return rbac.Role{}, fmt.Errorf("getting role of user %s: %w", user, err)
	}

	return role, nil
}

// Authenticator reports whether a request carries credentials the API would
// accept, without rejecting it: with auth disabled (no JWT signing key, or a
// dev key) every request does. Otherwise a request that presents a token is
// passed through the same chain Auth builds, with its token, user, revocation
// and proxy user checks, and counts as authenticated when it gets through. A
// request without a token is not, and logs nothing, so unauthenticated health
// probes do not fill the security log.
func Authenticator(jwtKey, proxyAuthHeader string) func(*http.Request) bool {
	if jwtKey == "" || strings.HasPrefix(jwtKey, "dev|") {
		return func(*http.Request) bool { return true }
	}

	chain := Auth(jwtKey, proxyAuthHeader)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if reached, ok := r.Context().Value(authenticatedKey{}).(*bool); ok {
			*reached = true
		}
	}))

	return func(r *http.Request) bool {
		if r.Header.Get("X-Phenix-Auth-Token") == "" && r.URL.Query().Get("token") == "" {
			return false
		}

		var reached bool

		probe := r.Clone(context.WithValue(r.Context(), authenticatedKey{}, &reached))

		// Auth lets requests for paths ending in /login or /signup through
		// without a user, which must not count here
		probe.URL.Path = "/"

		chain.ServeHTTP(discardWriter{header: make(http.Header)}, probe)

		return reached
	}
}

// authenticatedKey is the context key of the flag Authenticator's chain sets
// when a request gets through it.
type authenticatedKey struct{}

// discardWriter is a response writer that drops what Auth writes when it
// rejects a request Authenticator checks.
type discardWriter struct {
	header http.Header
}

func (w discardWriter) Header() http.Header { return w.header }

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }

func (discardWriter) WriteHeader(int) {}

// userTokenFromContext returns the parsed JWT from phenix or JWT middleware context keys.
func userTokenFromContext(ctx context.Context) any {
	if token := ctx.Value(ContextKeyUser); token != nil {
		return token
	}

	return ctx.Value("user")
}
