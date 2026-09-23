package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/web/rbac"
)

const testSigningKey = "test-signing-key"

// initTestStore points the default store at a throw-away BoltDB file seeded
// with the given built-in roles.
func initTestStore(t *testing.T, roles ...string) {
	t.Helper()

	previous := store.DefaultStore

	endpoint := "bolt://" + filepath.Join(t.TempDir(), "phenix.bdb")
	if err := store.Init(store.Endpoint(endpoint)); err != nil {
		t.Fatalf("initializing test store: %v", err)
	}

	t.Cleanup(func() {
		store.DefaultStore = previous //nolint:reassign // restoring test store
	})

	for _, role := range roles {
		body, err := os.ReadFile(filepath.Join("..", "..", "api", "config", "default", role+".yml"))
		if err != nil {
			t.Fatalf("reading default role %s: %v", role, err)
		}

		var c store.Config
		if err := yaml.Unmarshal(body, &c); err != nil {
			t.Fatalf("parsing default role %s: %v", role, err)
		}

		if err := store.Create(&c); err != nil {
			t.Fatalf("creating default role %s: %v", role, err)
		}
	}
}

// userClaims returns unexpired JWT claims for username.
func userClaims(username string) jwt.MapClaims {
	return jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

// signToken returns a JWT with the given claims signed with key.
func signToken(t *testing.T, key string, claims jwt.MapClaims) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	return signed
}

// createTestUser stores a user assigned to a built-in role and returns a token
// the user can authenticate with.
func createTestUser(t *testing.T, username, role, note string) string {
	t.Helper()

	user := rbac.NewUser(username, "Testpass1!")
	if user == nil {
		t.Fatalf("creating user %s", username)
	}

	r, err := rbac.RoleFromConfig(role)
	if err != nil {
		t.Fatalf("getting role %s: %v", role, err)
	}

	if err := user.SetRole(r); err != nil {
		t.Fatalf("setting user %s role: %v", username, err)
	}

	token := signToken(t, testSigningKey, userClaims(username))

	if err := user.AddToken(token, note); err != nil {
		t.Fatalf("adding user %s token: %v", username, err)
	}

	return token
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func saveRequest(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/builder/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

func TestSignedTokenAuth(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"":                            false,
		"proxy-jwt":                   false,
		"dev|user|global-admin":       false,
		"signing-key":                 true,
		"proxy-jwt-lookalike-key":     true,
		"development|not-dev-auth|xx": true,
	}

	for key, want := range tests {
		if got := SignedTokenAuth(key); got != want {
			t.Errorf("SignedTokenAuth(%q): got %t, want %t", key, got, want)
		}
	}
}

// TestDevAuthServicePermissions checks the built-in roles against the service
// permissions used by the Builder, Scorch, and Tunneler routes.
func TestDevAuthServicePermissions(t *testing.T) {
	initTestStore(t, "experiment-user", "experiment-viewer", "vm-viewer")

	tests := []struct {
		role     string
		resource string
		verb     string
		want     int
	}{
		{"experiment-user", "builder", "get", http.StatusOK},
		{"experiment-user", "builder", "post", http.StatusOK},
		{"experiment-user", "builder", "put", http.StatusForbidden},
		{"experiment-user", "scorch", "get", http.StatusOK},
		{"experiment-user", "scorch", "post", http.StatusOK},
		{"experiment-user", "scorch", "delete", http.StatusOK},
		{"experiment-user", "tunneler", "get", http.StatusOK},
		{"experiment-viewer", "builder", "get", http.StatusOK},
		{"experiment-viewer", "builder", "post", http.StatusForbidden},
		{"experiment-viewer", "scorch", "get", http.StatusOK},
		{"experiment-viewer", "scorch", "post", http.StatusForbidden},
		{"experiment-viewer", "tunneler", "get", http.StatusOK},
		{"vm-viewer", "builder", "get", http.StatusOK},
		{"vm-viewer", "scorch", "get", http.StatusForbidden},
		{"vm-viewer", "tunneler", "get", http.StatusForbidden},
	}

	for _, test := range tests {
		auth := Auth("dev|test-user|"+test.role, "")
		handler := auth(RequirePermission(test.resource, test.verb)(okHandler()))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != test.want {
			t.Errorf(
				"%s %s:%s: got status %d, want %d",
				test.role, test.resource, test.verb, rec.Code, test.want,
			)
		}
	}
}

// TestSignedTokenAuthBuilderRoutes exercises the middleware chains the server
// uses for GET /builder and POST /builder/save with signed JWTs.
func TestSignedTokenAuthBuilderRoutes(t *testing.T) {
	initTestStore(t, "experiment-user", "experiment-viewer", "disabled")

	var (
		userToken     = createTestUser(t, "exp-user", "experiment-user", "test")
		viewerToken   = createTestUser(t, "exp-viewer", "experiment-viewer", "test")
		disabledToken = createTestUser(t, "disabled-user", "disabled", "test")
		forged        = signToken(t, "wrong-key", userClaims("exp-user"))
		unissued      = signToken(t, testSigningKey, unissuedClaims("exp-user"))
		auth          = Auth(testSigningKey, "")
		builder       = auth(RequirePermission("builder", "get")(okHandler()))
		save          = AuthTokenFromForm(auth(RequirePermission("builder", "get")(okHandler())))
		saveNoForm    = auth(RequirePermission("builder", "get")(okHandler()))
	)

	tests := []struct {
		name    string
		handler http.Handler
		req     *http.Request
		want    int
	}{
		{
			name:    "builder with query token",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder?token="+userToken, nil),
			want:    http.StatusOK,
		},
		{
			name:    "builder without token",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder", nil),
			want:    http.StatusForbidden,
		},
		{
			name:    "builder as viewer",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder?token="+viewerToken, nil),
			want:    http.StatusOK,
		},
		{
			name:    "builder without builder permission",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder?token="+disabledToken, nil),
			want:    http.StatusForbidden,
		},
		{
			name:    "builder with forged token",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder?token="+forged, nil),
			want:    http.StatusUnauthorized,
		},
		{
			name:    "builder with token not issued to user",
			handler: builder,
			req:     httptest.NewRequest(http.MethodGet, "/builder?token="+unissued, nil),
			want:    http.StatusUnauthorized,
		},
		{
			name:    "save with form token",
			handler: save,
			req:     saveRequest(url.Values{"token": {userToken}, "xml": {"<x/>"}}),
			want:    http.StatusOK,
		},
		{
			name:    "save without token",
			handler: save,
			req:     saveRequest(url.Values{"xml": {"<x/>"}}),
			want:    http.StatusForbidden,
		},
		{
			name:    "save as viewer",
			handler: save,
			req:     saveRequest(url.Values{"token": {viewerToken}, "xml": {"<x/>"}}),
			want:    http.StatusOK,
		},
		{
			name:    "save without builder permission",
			handler: save,
			req:     saveRequest(url.Values{"token": {disabledToken}}),
			want:    http.StatusForbidden,
		},
		{
			name:    "form token ignored without form token middleware",
			handler: saveNoForm,
			req:     saveRequest(url.Values{"token": {userToken}}),
			want:    http.StatusForbidden,
		},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		test.handler.ServeHTTP(rec, test.req)

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d (%s)", test.name, rec.Code, test.want, rec.Body.String())
		}
	}
}

// unissuedClaims returns valid claims that differ from any token stored for
// the user, standing in for a token the user revoked.
func unissuedClaims(username string) jwt.MapClaims {
	claims := userClaims(username)
	claims["jti"] = "unissued"

	return claims
}

// TestProxyAuthRequiresProxyHeaders verifies proxy auth ignores tokens in the
// query string, so standalone service links cannot bypass the proxy.
func TestProxyAuthRequiresProxyHeaders(t *testing.T) {
	initTestStore(t, "experiment-user")

	var (
		token   = createTestUser(t, "proxy-user", "experiment-user", "proxied")
		auth    = Auth("proxy-jwt", "X-Forwarded-User")
		builder = auth(RequirePermission("builder", "get")(okHandler()))
	)

	tests := []struct {
		name  string
		query string
		token string
		user  string
		want  int
	}{
		{name: "query token only", query: "?token=" + token, want: http.StatusBadRequest},
		{name: "proxy headers", token: token, user: "proxy-user", want: http.StatusOK},
		{name: "proxy user mismatch", token: token, user: "someone-else", want: http.StatusUnauthorized},
	}

	for _, test := range tests {
		req := httptest.NewRequest(http.MethodGet, "/builder"+test.query, nil)

		if test.token != "" {
			req.Header.Set("X-Phenix-Auth-Token", "bearer "+test.token)
		}

		if test.user != "" {
			req.Header.Set("X-Forwarded-User", test.user)
		}

		rec := httptest.NewRecorder()
		builder.ServeHTTP(rec, req)

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d (%s)", test.name, rec.Code, test.want, rec.Body.String())
		}
	}
}
