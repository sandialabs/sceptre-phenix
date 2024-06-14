package middleware

import (
	"context"
	"fmt"
	"net/http"
	"phenix/util/plog"
	"phenix/web/rbac"
	jwtutil "phenix/web/util/jwt"
	"strings"

	jwtmiddleware "github.com/cescoferraro/go-jwt-middleware"
	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
)

func fromPhenixAuthTokenHeader(r *http.Request) (string, error) {
	authHeader := r.Header.Get("X-phenix-auth-token")
	if authHeader == "" {
		return "", nil // No error, just no token
	}

	authHeaderParts := strings.Split(authHeader, " ")
	if len(authHeaderParts) != 2 || strings.ToLower(authHeaderParts[0]) != "bearer" {
		return "", fmt.Errorf("X-phenix-auth-token header format must be 'Bearer {token}'")
	}

	return authHeaderParts[1], nil
}

func NoAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := rbac.RoleFromConfig("global-admin")

		ctx := r.Context()

		ctx = context.WithValue(ctx, "user", "global-admin")
		ctx = context.WithValue(ctx, "role", *role)

		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Auth(jwtKey, proxyAuthHeader string) mux.MiddlewareFunc {
	tokenMiddleware := jwtmiddleware.New(
		jwtmiddleware.Options{
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
			Extractor: jwtmiddleware.FromFirst(fromPhenixAuthTokenHeader, jwtmiddleware.FromParameter("token")),
			ValidationKeyGetter: func(_ *jwt.Token) (interface{}, error) {
				return []byte(jwtKey), nil
			},
			SigningMethod: jwt.SigningMethodHS256,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, e string) {
				plog.Error("validating auth token", "err", e)

				// TODO: remove token from user spec?

				http.Error(w, e, http.StatusUnauthorized)
			},
		},
	)

	validTokenMiddleware := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := fromPhenixAuthTokenHeader(r)
			if err != nil {
				plog.Error("getting raw JWT from X-phenix-auth-token header", "err", err)

				http.Error(w, "missing phenix auth token header", http.StatusBadRequest)
				return
			}

			token, _, err := new(jwt.Parser).ParseUnverified(raw, jwt.MapClaims{})
			if err != nil {
				plog.Error("parsing valid JWT", "token", raw, "err", err)

				http.Error(w, "parsing auth token", http.StatusBadRequest)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, "user", token)

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

			userToken := ctx.Value("user")
			if userToken == nil {
				plog.Error("rejecting unauthorized request - missing user token", "path", r.URL.Path)
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			var (
				token  = userToken.(*jwt.Token)
				claims = token.Claims.(jwt.MapClaims)
			)

			jwtUser, err := jwtutil.UsernameFromClaims(claims)
			if err != nil {
				plog.Error("rejecting unauthorized request", "path", r.URL.Path, "err", err)
				http.Error(w, "Forbidden", http.StatusUnauthorized)
				return
			}

			if proxyAuthHeader != "" {
				if user := r.Header.Get(proxyAuthHeader); user != jwtUser {
					plog.Error("proxy user mismatch", "user", user, "token", jwtUser)
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

			ctx = context.WithValue(ctx, "user", user.Username())
			ctx = context.WithValue(ctx, "role", role)
			ctx = context.WithValue(ctx, "jwt", token.Raw)

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

			role, _ := rbac.RoleFromConfig(creds[2])

			ctx = context.WithValue(ctx, "user", creds[1])
			ctx = context.WithValue(ctx, "role", *role)

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	if jwtKey == "" {
		plog.Info("no JWT signing key provided -- disabling auth")
		return func(h http.Handler) http.Handler { return NoAuth(h) }
	} else if jwtKey == "proxy-jwt" {
		plog.Info("using JWTs from proxy")
		return func(h http.Handler) http.Handler { return validTokenMiddleware(userMiddleware(h)) }
	} else if strings.HasPrefix(jwtKey, "dev|") {
		plog.Debug("development JWT key provided -- enabling dev auth")
		return func(h http.Handler) http.Handler { return devAuthMiddleware(h) }
	}

	// First validate the token itself, then ensure the user in the token is valid.
	return func(h http.Handler) http.Handler { return tokenMiddleware.Handler(userMiddleware(h)) }
}
