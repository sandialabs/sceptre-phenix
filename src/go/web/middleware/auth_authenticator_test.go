package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

const testJWTKey = "secret"

// useBoltStore makes a new BoltDB store in a temporary directory the store
// for the rest of the test.
func useBoltStore(t *testing.T) {
	t.Helper()

	s := store.NewBoltDB()

	if err := s.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "store.bdb"))); err != nil {
		t.Fatalf("initializing the BoltDB store: %v", err)
	}

	original := store.DefaultStore

	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	store.DefaultStore = s //nolint:reassign // install test double
}

// signedToken returns a token for user signed with key.
func signedToken(t *testing.T, user, key string) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user,
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("signing a token for %s: %v", user, err)
	}

	return signed
}

// request is a health probe, with the token in the header or the query.
func request(t *testing.T, token string, inQuery bool) *http.Request {
	t.Helper()

	target := "/readyz"
	if token != "" && inQuery {
		target += "?token=" + token
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)

	if token != "" && !inQuery {
		r.Header.Set("X-Phenix-Auth-Token", "Bearer "+token)
	}

	return r
}

//nolint:paralleltest // replaces the store
func TestAuthenticator(t *testing.T) {
	useBoltStore(t)

	user := rbac.NewUser("alice", "password")
	if user == nil {
		t.Fatal("NewUser(alice) = nil")
	}

	role := &rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}
	if err := user.SetRole(role); err != nil {
		t.Fatalf("SetRole(Global Viewer) error = %v", err)
	}

	valid := signedToken(t, "alice", testJWTKey)

	if err := user.AddToken(valid, "test"); err != nil {
		t.Fatalf("AddToken() error = %v", err)
	}

	unknown := signedToken(t, "alice", "other-key")

	tests := []struct {
		name    string
		jwtKey  string
		proxy   string
		request *http.Request
		want    bool
	}{
		{"auth disabled", "", "", request(t, "", false), true},
		{"dev auth", "dev|alice|global-viewer", "", request(t, "", false), true},
		{"no token", testJWTKey, "", request(t, "", false), false},
		{"garbage token", testJWTKey, "", request(t, "garbage", false), false},
		{"token signed with another key", testJWTKey, "", request(t, unknown, false), false},
		{"valid token", testJWTKey, "", request(t, valid, false), true},
		{"valid token in query", testJWTKey, "", request(t, valid, true), true},
		{"proxy without user header", testJWTKey, "X-Forwarded-User", request(t, valid, false), false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Authenticator(test.jwtKey, test.proxy)(test.request); got != test.want {
				t.Errorf("Authenticator(%q, %q)(GET %s) = %v, want %v", test.jwtKey, test.proxy, test.request.URL, got, test.want)
			}
		})
	}

	proxied := request(t, valid, false)
	proxied.Header.Set("X-Forwarded-User", "alice")

	if !Authenticator(testJWTKey, "X-Forwarded-User")(proxied) {
		t.Error("Authenticator(key, X-Forwarded-User)(a matching user header) = false, want true")
	}

	// a path Auth lets through without a user still needs one here
	login := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login?token=garbage", nil)

	if Authenticator(testJWTKey, "")(login) {
		t.Error("Authenticator(key, \"\")(GET /login?token=garbage) = true, want false")
	}

	if err := user.DeleteToken(valid); err != nil {
		t.Fatalf("DeleteToken() error = %v", err)
	}

	if Authenticator(testJWTKey, "")(request(t, valid, false)) {
		t.Error("Authenticator(key, \"\")(a revoked token) = true, want false")
	}
}
