package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"

	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const testProxyHeader = "X-Forwarded-User"

// errStoreDown is the error a usersTestStore set to fail returns.
var errStoreDown = errors.New("store is down")

// failingReader is a request body whose reads fail.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// useAuthOptions sets the server options the sign-in handlers read: the JWT
// key testJWTKey, a one-hour lifetime and the proxy header proxyHeader.
func useAuthOptions(t *testing.T, proxyHeader string) {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o.jwtKey, o.jwtLifetime, o.proxyAuthHeader = testJWTKey, time.Hour, proxyHeader
}

// proxyJWT is the request context a proxy JWT with claims gives, as Auth's
// proxy-jwt mode sets it.
func proxyJWT(claims jwt.MapClaims) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		token.Raw = "proxy.jwt.raw"

		return context.WithValue(ctx, middleware.ContextKeyUser, token)
	}
}

// handlerAnswer is the status and message of a handler's answer; Message is
// empty for a success.
type handlerAnswer struct {
	Status  int
	Message string
}

// serveHandler answers req with h on ErrorHandler.
func serveHandler(h func(http.ResponseWriter, *http.Request) error, req *http.Request) (handlerAnswer, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	weberror.ErrorHandler(h).ServeHTTP(rec, req)

	got := handlerAnswer{Status: rec.Code}

	if rec.Code >= http.StatusBadRequest {
		var e weberror.WebError

		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		got.Message = e.Message
	}

	return got, rec
}

// sessionOf returns the token of a LoginResponse body.
func sessionOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var resp LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding login response %q: %v", rec.Body.String(), err)
	}

	return resp.Token
}

// wantSession fails the test unless user holds the session token.
func wantSession(t *testing.T, user, token string) {
	t.Helper()

	u, err := rbac.GetUser(user)
	if err != nil {
		t.Fatalf("rbac.GetUser(%s) error = %v", user, err)
	}

	if err := u.ValidateToken(token); err != nil {
		t.Errorf("user %s does not hold the session token %q: %v", user, token, err)
	}
}

// Login signs a user in by basic auth, query, JSON body, proxy header or proxy
// JWT, and refuses each request that does not name a user and password it
// can check.
func TestLoginBranches(t *testing.T) {
	type tc struct {
		proxyHeader string
		method      string
		target      string
		body        string
		header      []string // pairs
		basicAuth   bool
		ctx         func(context.Context) context.Context
		storeFails  bool
		want        handlerAnswer
		session     string // the session bob then holds; "signed" for a token signed with the key
	}

	ok := handlerAnswer{Status: http.StatusOK}
	invalid := handlerAnswer{Status: http.StatusUnauthorized, Message: "invalid credentials"}

	tests := map[string]tc{
		"basic auth": {method: http.MethodGet, target: "/login", basicAuth: true, want: ok, session: "signed"},
		"query":      {method: http.MethodGet, target: "/login?user=bob&pass=" + testPassword, want: ok, session: "signed"},
		"query without user": {
			method: http.MethodGet,
			target: "/login",
			want:   handlerAnswer{http.StatusBadRequest, "no username provided"},
		},
		"query without pass": {
			method: http.MethodGet,
			target: "/login?user=bob",
			want:   handlerAnswer{http.StatusBadRequest, "no password provided"},
		},
		"query wrong pass": {method: http.MethodGet, target: "/login?user=bob&pass=Wrongpass1!", want: invalid},
		"POST": {
			method:  http.MethodPost,
			target:  "/login",
			body:    `{"user":"bob","pass":"` + testPassword + `"}`,
			want:    ok,
			session: "signed",
		},
		"POST bad JSON": {
			method: http.MethodPost,
			target: "/login",
			body:   `{`,
			want:   handlerAnswer{http.StatusBadRequest, "invalid data provided in POST"},
		},
		"POST no user": {
			method: http.MethodPost,
			target: "/login",
			body:   `{"pass":"x"}`,
			want:   handlerAnswer{http.StatusBadRequest, "invalid username provided in POST"},
		},
		"POST no pass": {
			method: http.MethodPost,
			target: "/login",
			body:   `{"user":"bob"}`,
			want:   handlerAnswer{http.StatusBadRequest, "invalid password provided in POST"},
		},
		"POST wrong pass": {method: http.MethodPost, target: "/login", body: `{"user":"bob","pass":"Wrongpass1!"}`, want: invalid},
		"PUT":             {method: http.MethodPut, target: "/login", want: handlerAnswer{http.StatusBadRequest, "invalid method"}},
		"session not saved": {
			method: http.MethodPost, target: "/login", body: `{"user":"bob","pass":"` + testPassword + `"}`, storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, msgSaveSessionToken},
		},
		"proxy header": {
			proxyHeader: testProxyHeader, method: http.MethodGet, target: "/login", header: []string{testProxyHeader, "bob"},
			want: ok, session: "signed",
		},
		"proxy header without user": {
			proxyHeader: testProxyHeader, method: http.MethodGet, target: "/login",
			want: handlerAnswer{http.StatusUnauthorized, "proxy authentication failed"},
		},
		"proxy header, unknown user": {
			proxyHeader: testProxyHeader, method: http.MethodGet, target: "/login", header: []string{testProxyHeader, "mallory"},
			want: handlerAnswer{http.StatusNotFound, "user not found"},
		},
		"proxy header, POST": {
			proxyHeader: testProxyHeader, method: http.MethodPost, target: "/login", body: `{"user":"bob","pass":"` + testPassword + `"}`,
			want: handlerAnswer{http.StatusBadRequest, "proxy auth is enabled: log in with a GET request"},
		},
		"proxy JWT": {
			method: http.MethodGet, target: "/login", ctx: proxyJWT(jwt.MapClaims{"sub": "bob"}), want: ok, session: "proxy.jwt.raw",
		},
		"proxy JWT without user": {
			method: http.MethodGet, target: "/login", ctx: proxyJWT(jwt.MapClaims{"exp": 1.0}),
			want: handlerAnswer{http.StatusUnauthorized, "proxy user missing"},
		},
		"proxy JWT, unknown user": {
			method: http.MethodGet, target: "/login", ctx: proxyJWT(jwt.MapClaims{"sub": "mallory"}),
			want: handlerAnswer{http.StatusNotFound, "user not found"},
		},
		"proxy JWT, session not saved": {
			method: http.MethodGet, target: "/login", ctx: proxyJWT(jwt.MapClaims{"sub": "bob"}), storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, msgSaveSessionToken},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)
			createUserWithToken(t, "bob")
			useAuthOptions(t, tc.proxyHeader)

			if tc.storeFails {
				s.updateErr = errStoreDown
			}

			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			for i := 0; i+1 < len(tc.header); i += 2 {
				req.Header.Set(tc.header[i], tc.header[i+1])
			}

			if tc.basicAuth {
				req.SetBasicAuth("bob", testPassword)
			}

			if tc.ctx != nil {
				req = req.WithContext(tc.ctx(req.Context()))
			}

			got, rec := serveHandler(Login, req)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("%s %s mismatch (-want +got):\n%s\n%s", tc.method, tc.target, diff, rec.Body)
			}

			if tc.session == "" {
				return
			}

			session := sessionOf(t, rec)

			if tc.session == "signed" {
				claims := jwt.MapClaims{}
				if _, err := jwt.ParseWithClaims(
					session,
					claims,
					func(*jwt.Token) (any, error) { return []byte(testJWTKey), nil },
				); err != nil ||
					claims["sub"] != "bob" {
					t.Errorf("session %q is not a token for bob signed with the key: %v", session, err)
				}
			} else if session != tc.session {
				t.Errorf("session = %q, want %q", session, tc.session)
			}

			wantSession(t, "bob", session)
		})
	}

	t.Run("unreadable body", func(t *testing.T) {
		useUsersTestStore(t)
		useAuthOptions(t, "")

		req := httptest.NewRequest(http.MethodPost, "/login", failingReader{})
		if got, _ := serveHandler(Login, req); got != (handlerAnswer{http.StatusBadRequest, "no data provided in POST"}) {
			t.Errorf("POST /login with an unreadable body = %+v, want 400", got)
		}
	})
}

// Signup creates the user and signs them in; behind a proxy only as the user
// the proxy names; a request it cannot read, a weak password or a session it
// cannot save is refused.
func TestSignupBranches(t *testing.T) {
	const carol = `{"username":"carol","password":"` + testPassword + `","first_name":"C","last_name":"D"}`

	tests := map[string]struct {
		proxyHeader string
		body        string
		header      []string
		ctx         func(context.Context) context.Context
		storeFails  bool
		want        handlerAnswer
		session     string // the session carol holds; "signed" for a token signed with the key
	}{
		"password":      {body: carol, want: handlerAnswer{Status: http.StatusOK}, session: "signed"},
		"bad JSON":      {body: `{`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"not JSON":      {body: `username=carol`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"weak password": {body: `{"username":"carol","password":"x"}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"session not saved": {
			body: carol, storeFails: true, want: handlerAnswer{http.StatusInternalServerError, msgSaveSessionToken},
		},
		"proxy header": {
			proxyHeader: testProxyHeader, body: carol, header: []string{testProxyHeader, "carol"},
			want: handlerAnswer{Status: http.StatusOK}, session: "signed",
		},
		"proxy header names another user": {
			proxyHeader: testProxyHeader, body: carol, header: []string{testProxyHeader, "dave"},
			want: handlerAnswer{http.StatusUnauthorized, "proxy user mismatch"},
		},
		"proxy JWT": {
			body: carol, ctx: proxyJWT(jwt.MapClaims{"sub": "carol"}), want: handlerAnswer{Status: http.StatusOK},
			session: "proxy.jwt.raw",
		},
		"proxy JWT names another user": {
			body: carol, ctx: proxyJWT(jwt.MapClaims{"sub": "dave"}), want: handlerAnswer{http.StatusUnauthorized, "proxy user mismatch"},
		},
		"proxy JWT without user": {
			body: carol, ctx: proxyJWT(jwt.MapClaims{}), want: handlerAnswer{http.StatusUnauthorized, "proxy user missing"},
		},
		"proxy JWT, session not saved": {
			body: carol, ctx: proxyJWT(jwt.MapClaims{"sub": "carol"}), storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, msgSaveSessionToken},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)
			useAuthOptions(t, tc.proxyHeader)
			events := captureAudit(t)

			if tc.storeFails {
				s.updateErr = errStoreDown
			}

			req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(tc.body))
			for i := 0; i+1 < len(tc.header); i += 2 {
				req.Header.Set(tc.header[i], tc.header[i+1])
			}

			if tc.ctx != nil {
				req = req.WithContext(tc.ctx(req.Context()))
			}

			got, rec := serveHandler(Signup, req)
			if tc.want.Status == http.StatusBadRequest {
				got.Message = "" // the password rules, as settings render them, or the JSON decoder's error
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("POST /signup mismatch (-want +got):\n%s\n%s", diff, rec.Body)
			}

			if tc.want.Message == "proxy user mismatch" {
				wantRefusedFor(t, events(), "user-create", "proxy user mismatch")
			}

			if tc.session == "" {
				return
			}

			session := sessionOf(t, rec)
			if tc.session != "signed" && session != tc.session {
				t.Errorf("session = %q, want %q", session, tc.session)
			}

			wantSession(t, "carol", session)
		})
	}

	t.Run("unreadable body", func(t *testing.T) {
		useUsersTestStore(t)
		useAuthOptions(t, "")

		req := httptest.NewRequest(http.MethodPost, "/signup", failingReader{})
		if got, _ := serveHandler(Signup, req); got.Status != http.StatusInternalServerError {
			t.Errorf("POST /signup with an unreadable body = %+v, want 500", got)
		}
	})
}

// wantRefusedFor fails the test unless events hold a refusal of action for reason.
func wantRefusedFor(t *testing.T, events []auditEvent, action, reason string) {
	t.Helper()

	wantAudit(t, events, action, "failure", map[string]any{"event.reason": reason})
}

// Logout drops the session token of the signed in user.
func TestLogoutBranches(t *testing.T) {
	signedIn := func(user, token string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/logout", nil)
		ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, user)
		ctx = context.WithValue(ctx, middleware.ContextKeyJWT, token)

		return req.WithContext(ctx)
	}

	t.Run("signed in", func(t *testing.T) {
		useUsersTestStore(t)
		createUserWithToken(t, "bob")

		if got, _ := serveHandler(Logout, signedIn("bob", testToken)); got != (handlerAnswer{Status: http.StatusNoContent}) {
			t.Fatalf("POST /logout as bob = %+v, want 204", got)
		}

		bob, err := rbac.GetUser("bob")
		if err != nil {
			t.Fatal(err)
		}

		if err := bob.ValidateToken(testToken); err == nil {
			t.Error("bob still holds the session token after POST /logout")
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		useUsersTestStore(t)

		want := handlerAnswer{Status: http.StatusNoContent}
		if got, _ := serveHandler(Logout, signedIn("mallory", testToken)); got != want {
			t.Errorf("POST /logout as mallory = %+v, want %+v", got, want)
		}
	})

	t.Run("token not dropped", func(t *testing.T) {
		s := useUsersTestStore(t)
		createUserWithToken(t, "bob")

		s.updateErr = errStoreDown

		want := handlerAnswer{http.StatusInternalServerError, "failed to delete session token"}
		if got, _ := serveHandler(Logout, signedIn("bob", testToken)); got != want {
			t.Errorf("POST /logout as bob with the store down = %+v, want %+v", got, want)
		}
	})
}

// CreateUserToken makes a token of the lifetime asked for, as a duration or
// a number of days, and refuses a request it cannot read or complete.
func TestCreateUserTokenBranches(t *testing.T) {
	own := testRole(resourceUsers, verbPatch, "test-user")

	tests := map[string]struct {
		user       string // the user the route names
		body       string
		storeFails bool
		want       handlerAnswer
		lifetime   time.Duration
		desc       string
	}{
		"hours": {
			user:     "test-user",
			body:     `{"lifetime":"2h","desc":"ci"}`,
			want:     handlerAnswer{Status: http.StatusOK},
			lifetime: 2 * time.Hour,
			desc:     "ci",
		},
		"days": {
			user:     "test-user",
			body:     `{"lifetime":"7"}`,
			want:     handlerAnswer{Status: http.StatusOK},
			lifetime: 7 * 24 * time.Hour,
		},
		"bad lifetime": {
			user: "test-user",
			body: `{"lifetime":"forever"}`,
			want: handlerAnswer{http.StatusBadRequest, "invalid token lifetime provided"},
		},
		"bad JSON": {user: "test-user", body: `{`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"unknown user": {
			user: "nobody",
			body: `{"lifetime":"1h"}`,
			want: handlerAnswer{http.StatusNotFound, "user not found"},
		},
		"token not saved": {
			user:       "test-user",
			body:       `{"lifetime":"1h"}`,
			storeFails: true,
			want:       handlerAnswer{http.StatusInternalServerError, "failed to save token"},
		},
		"another user": {user: "bob", body: `{"lifetime":"1h"}`, want: handlerAnswer{http.StatusForbidden, "forbidden"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)
			createUserWithToken(t, "test-user")
			useAuthOptions(t, "")

			if tc.storeFails {
				s.updateErr = errStoreDown
			}

			role := combinedRole(own, testRole(resourceUsers, verbPatch, "nobody"), testRole("users/tokens", "create", "nobody"))
			req := roleRequest(http.MethodPost, "/api/v1/users/"+tc.user+"/tokens", tc.body, map[string]string{"username": tc.user}, role)

			got, rec := serveHandler(CreateUserToken, req)
			if tc.want.Status == http.StatusBadRequest && tc.want.Message == "" {
				got.Message = "" // the JSON decoder's error
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("POST /users/%s/tokens mismatch (-want +got):\n%s\n%s", tc.user, diff, rec.Body)
			}

			if tc.want.Status != http.StatusOK {
				return
			}

			var resp CreateTokenResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}

			exp, err := time.Parse(time.RFC3339, resp.Expiration)
			if err != nil {
				t.Fatalf("token expiration %q: %v", resp.Expiration, err)
			}

			if d := time.Until(exp); d < tc.lifetime-time.Minute || d > tc.lifetime {
				t.Errorf("token expires in %v, want %v", d, tc.lifetime)
			}

			if tc.desc != "" && resp.Description != tc.desc {
				t.Errorf("token description = %q, want %q", resp.Description, tc.desc)
			}

			if tc.desc == "" && !strings.HasPrefix(resp.Description, "manually generated - ") {
				t.Errorf("token description = %q, want the default note", resp.Description)
			}

			wantSession(t, "test-user", resp.Token)
		})
	}

	t.Run("unreadable body", func(t *testing.T) {
		useUsersTestStore(t)
		createUserWithToken(t, "test-user")

		req := roleRequest(http.MethodPost, "/api/v1/users/test-user/tokens", "", map[string]string{"username": "test-user"}, own)
		req.Body = io.NopCloser(failingReader{})

		if got, _ := serveHandler(CreateUserToken, req); got.Status != http.StatusInternalServerError {
			t.Errorf("POST /users/test-user/tokens with an unreadable body = %+v, want 500", got)
		}
	})
}

func TestGetRolesBranches(t *testing.T) {
	t.Run("lists the roles", func(t *testing.T) {
		useUsersTestStore(t)

		got, rec := serveHandler(GetRoles, roleRequest(http.MethodGet, "/roles", "", nil, testRole("roles", "list")))
		if got.Status != http.StatusOK {
			t.Fatalf("GET /roles = %+v, want 200", got)
		}

		var body struct {
			Roles []struct {
				Name string `json:"name"`
			} `json:"roles"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}

		names := make(map[string]bool)
		for _, r := range body.Roles {
			names[r.Name] = true
		}

		if diff := cmp.Diff(map[string]bool{usersTestRole: true, "Disabled": true}, names); diff != "" {
			t.Errorf("GET /roles names mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		useUsersTestStore(t)

		want := handlerAnswer{http.StatusForbidden, "forbidden to list roles"}
		if got, _ := serveHandler(GetRoles, roleRequest(http.MethodGet, "/roles", "", nil, testRole("users", "list"))); got != want {
			t.Errorf("GET /roles without roles list = %+v, want %+v", got, want)
		}
	})

	t.Run("store down", func(t *testing.T) {
		s := useUsersTestStore(t)
		s.listErr, s.listKind = errStoreDown, "Role"

		want := handlerAnswer{http.StatusInternalServerError, "error retrieving roles"}
		if got, _ := serveHandler(GetRoles, roleRequest(http.MethodGet, "/roles", "", nil, testRole("roles", "list"))); got != want {
			t.Errorf("GET /roles with the store down = %+v, want %+v", got, want)
		}
	})

	t.Run("permissions", func(t *testing.T) {
		useUsersTestStore(t)

		top := topLevelPermissions()
		for query, want := range map[string][]rbac.Permission{"": nil, "?permissions=false": nil, "?permissions=true": top} {
			got, rec := serveHandler(GetRoles, roleRequest(http.MethodGet, "/roles"+query, "", nil, testRole("roles", "list")))
			if got.Status != http.StatusOK {
				t.Fatalf("GET /roles%s = %+v, want 200", query, got)
			}

			var body struct {
				Roles []Role `json:"roles"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}

			for _, role := range body.Roles {
				if diff := cmp.Diff(want, role.Permissions); diff != "" {
					t.Errorf("GET /roles%s role %s permissions mismatch (-want +got):\n%s", query, role.Name, diff)
				}
			}
		}
	})

	t.Run("bad permissions value", func(t *testing.T) {
		useUsersTestStore(t)

		req := roleRequest(http.MethodGet, "/roles?permissions=yes", "", nil, testRole("roles", "list"))
		if got, _ := serveHandler(GetRoles, req); got.Status != http.StatusBadRequest {
			t.Errorf("GET /roles?permissions=yes = %+v, want 400", got)
		}
	})
}

// topLevelPermissions are the known permissions whose resource has no "/",
// which the "*" resource of the users test store's roles matches.
func topLevelPermissions() []rbac.Permission {
	var top []rbac.Permission

	for _, p := range rbac.Permissions {
		if !strings.Contains(p.Resource, "/") {
			top = append(top, p)
		}
	}

	return top
}

// GET /roles/{name} finds a role by its config name or display name.
func TestGetRole(t *testing.T) {
	rolesGet := testRole("roles", "get", "*")
	everything := []Policy{{Resources: []string{"*"}, ResourceNames: []string{"*"}, Verbs: []string{"*"}}}

	tests := []struct {
		name   string
		target string
		role   rbac.Role
		want   Role
	}{
		{name: "config name", target: "/roles/disabled", role: rolesGet, want: Role{Name: "Disabled", Policies: everything}},
		{name: "display name", target: "/roles/Disabled", role: rolesGet, want: Role{Name: "Disabled", Policies: everything}},
		{
			name: "with permissions", target: "/roles/disabled?permissions=true", role: rolesGet,
			want: Role{Name: "Disabled", Policies: everything, Permissions: topLevelPermissions()},
		},
		{
			name: "name-scoped policy", target: "/roles/disabled", role: testRole("roles", "get", "disabled"),
			want: Role{Name: "Disabled", Policies: everything},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useUsersTestStore(t)

			name, _, _ := strings.Cut(strings.TrimPrefix(tt.target, "/roles/"), "?")

			got, rec := serveHandler(GetRole, roleRequest(http.MethodGet, tt.target, "", map[string]string{"name": name}, tt.role))
			if got.Status != http.StatusOK {
				t.Fatalf("GET %s = %+v, want 200", tt.target, got)
			}

			var role Role
			if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, role); diff != "" {
				t.Errorf("GET %s mismatch (-want +got):\n%s", tt.target, diff)
			}
		})
	}
}

// GET /roles/{name} refuses a role without roles get, and tells an unknown
// role and a store failure apart.
func TestGetRoleErrors(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		role    rbac.Role
		listErr bool
		want    handlerAnswer
	}{
		{
			name: "forbidden", target: "/roles/disabled", role: testRole("roles", "list"),
			want: handlerAnswer{http.StatusForbidden, "forbidden to get role"},
		},
		{
			name: "policy scoped to another role", target: "/roles/disabled", role: testRole("roles", "get", "viewer"),
			want: handlerAnswer{http.StatusForbidden, "forbidden to get role"},
		},
		{
			name:   "unknown",
			target: "/roles/nobody",
			role:   testRole("roles", "get", "*"),
			want:   handlerAnswer{http.StatusNotFound, "role not found"},
		},
		{
			name: "bad permissions value", target: "/roles/disabled?permissions=yes", role: testRole("roles", "get", "*"),
			want: handlerAnswer{http.StatusBadRequest, `permissions must be true or false, not "yes"`},
		},
		{
			name: "store down", target: "/roles/disabled", role: testRole("roles", "get", "*"), listErr: true,
			want: handlerAnswer{http.StatusInternalServerError, "error retrieving role"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := useUsersTestStore(t)
			if tt.listErr {
				s.listErr, s.listKind = errStoreDown, "Role"
			}

			name, _, _ := strings.Cut(strings.TrimPrefix(tt.target, "/roles/"), "?")

			if got, _ := serveHandler(
				GetRole,
				roleRequest(http.MethodGet, tt.target, "", map[string]string{"name": name}, tt.role),
			); got != tt.want {
				t.Errorf("GET %s = %+v, want %+v", tt.target, got, tt.want)
			}
		})
	}
}
