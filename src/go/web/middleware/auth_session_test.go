package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
)

// rolelessStore holds one user, alice, who holds session and has no role,
// and no role configs, not even the disabled role.
type rolelessStore struct {
	*roleStore

	session string
}

func (s *rolelessStore) Get(c *store.Config) error {
	if c.Kind != "User" || c.Metadata.Name != "alice" {
		return store.ErrNotExist
	}

	c.Spec = map[string]any{
		"username": "alice",
		"tokens":   map[string]any{base64.StdEncoding.EncodeToString([]byte(s.session)): "test"},
	}

	return nil
}

// A signed in user reaches the handler with the user, role and session JWT
// in the request context.
//
//nolint:paralleltest // replaces the store
func TestAuthPutsTheSessionInTheContext(t *testing.T) {
	session := signedToken(t, "alice", testJWTKey)

	s := &userStore{roleStore: useRoles(t, nil), users: map[string][]string{"alice": {session}}}
	store.DefaultStore = s //nolint:reassign // install test double; useRoles restores the original

	type seen struct{ User, Role, JWT string }

	var got seen

	h := Auth(testJWTKey, "")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = seen{UserFromContext(r.Context()), RoleFromContext(r.Context()).Spec.Name, JWTFromContext(r.Context())}
	}))

	for name, inQuery := range map[string]bool{"header": false, "query": true} {
		t.Run(name, func(t *testing.T) {
			got = seen{}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, request(t, session, inQuery))

			if rec.Code != http.StatusOK {
				t.Fatalf("GET /readyz with a session in the %s = %d, want 200", name, rec.Code)
			}

			if diff := cmp.Diff(seen{"alice", "role of alice", session}, got); diff != "" {
				t.Errorf("handler context mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A user whose role cannot be read is refused, and ResolveRole fails for it.
//
//nolint:paralleltest // replaces the store
func TestAuthRefusesAUserWithoutARole(t *testing.T) {
	session := signedToken(t, "alice", testJWTKey)

	store.DefaultStore = &rolelessStore{roleStore: useRoles(t, nil), session: session} //nolint:reassign // install test double

	option, refusals := recordRefusals()

	var reached bool

	h := Auth(testJWTKey, "", option)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(t, session, false))

	if reached || rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /readyz as a user without a role: reached = %t, status = %d, want false, 401", reached, rec.Code)
	}

	want := []refused{{Path: "/readyz", Reason: RefusedNoRole, User: "alice", Verified: true}}
	if diff := cmp.Diff(want, refusals()); diff != "" {
		t.Errorf("refusals mismatch (-want +got):\n%s", diff)
	}

	if role, err := ResolveRole(testJWTKey, "alice", session); err == nil {
		t.Errorf("ResolveRole(alice) = %q, want an error", role.Spec.Name)
	}
}

// An empty JWT key turns auth off: every request is global-admin.
//
//nolint:paralleltest // replaces the store
func TestAuthWithoutAKeyIsNoAuth(t *testing.T) {
	useRoles(t, map[string]string{"global-admin": "Global Admin"})

	var user, role string

	h := Auth("", "")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		user, role = UserFromContext(r.Context()), RoleFromContext(r.Context()).Spec.Name
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/experiments/exp", nil))

	if rec.Code != http.StatusOK || user != "global-admin" || role != "Global Admin" {
		t.Errorf("DELETE without auth = %d as %s/%s, want 200 as global-admin/Global Admin", rec.Code, user, role)
	}
}
