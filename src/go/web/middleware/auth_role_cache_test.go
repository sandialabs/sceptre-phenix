package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"phenix/store"
)

// roleStore is a store holding only role configs that counts how often they
// are listed. Any other store call panics on the nil embedded interface.
type roleStore struct {
	store.Store

	roles store.Configs
	lists atomic.Int32
}

func (s *roleStore) List(...string) (store.Configs, error) {
	s.lists.Add(1)

	return s.roles, nil
}

// useRoles stores roles, mapping metadata names to role names, and empties
// the fixed role cache.
func useRoles(t *testing.T, roles map[string]string) *roleStore {
	t.Helper()

	s := new(roleStore)

	for name, roleName := range roles {
		s.roles = append(s.roles, store.Config{
			Kind:     "Role",
			Metadata: store.ConfigMetadata{Name: name},
			Spec:     map[string]any{"roleName": roleName},
		})
	}

	resetCache := func() {
		fixedRolesMu.Lock()
		fixedRoles = make(map[string]cachedRole)
		fixedRolesMu.Unlock()
	}

	original := store.DefaultStore

	t.Cleanup(func() {
		store.DefaultStore = original //nolint:reassign // restore test double
		resetCache()
	})

	store.DefaultStore = s //nolint:reassign // install test double
	resetCache()

	return s
}

// No-auth and dev-auth look up one stored role once and reuse it; a missing
// role answers 500, uncached.
//
//nolint:paralleltest // replaces the store and the fixed role cache
func TestFixedRoleModes(t *testing.T) {
	devAuth := Auth("dev|alice|global-viewer", "")

	tests := map[string]struct {
		auth    func(http.Handler) http.Handler
		roles   map[string]string
		status  int
		context string // the role and user the handler sees
		lookups int32
	}{
		"no auth": {
			auth: NoAuth, roles: map[string]string{"global-admin": "Global Admin"},
			status: http.StatusOK, context: "Global Admin/global-admin", lookups: 1,
		},
		"dev auth": {
			auth: devAuth, roles: map[string]string{"global-viewer": "Global Viewer"},
			status: http.StatusOK, context: "Global Viewer/alice", lookups: 1,
		},
		"no auth without the role": {
			auth: NoAuth, roles: nil, status: http.StatusInternalServerError, lookups: 3,
		},
		"dev auth without the role": {
			auth: devAuth, roles: map[string]string{"global-admin": "Global Admin"},
			status: http.StatusInternalServerError, lookups: 3,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useRoles(t, tc.roles)

			var seen string

			h := tc.auth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = RoleFromContext(r.Context()).Spec.Name + "/" + UserFromContext(r.Context())
			}))

			for range 3 {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

				if rec.Code != tc.status {
					t.Errorf("GET / = %d, want %d", rec.Code, tc.status)
				}
			}

			if seen != tc.context {
				t.Errorf("GET / handler saw role/user %q, want %q", seen, tc.context)
			}

			if n := s.lists.Load(); n != tc.lookups {
				t.Errorf("3 requests listed the roles %d times, want %d", n, tc.lookups)
			}
		})
	}
}

// userStore adds users to a roleStore: each holds the sessions given, and
// the role named role.
type userStore struct {
	*roleStore

	users map[string][]string
}

func (s *userStore) Get(c *store.Config) error {
	sessions, ok := s.users[c.Metadata.Name]
	if c.Kind != "User" || !ok {
		return store.ErrNotExist
	}

	tokens := make(map[string]any, len(sessions))
	for _, session := range sessions {
		tokens[base64.StdEncoding.EncodeToString([]byte(session))] = "test"
	}

	c.Spec = map[string]any{
		"username": c.Metadata.Name,
		"tokens":   tokens,
		"rbac":     map[string]any{"roleName": "role of " + c.Metadata.Name},
	}

	return nil
}

// ResolveRole finds a user's role again as each auth mode does.
//
//nolint:paralleltest // replaces the store and the fixed role cache
func TestResolveRole(t *testing.T) {
	s := &userStore{
		roleStore: useRoles(t, map[string]string{"global-admin": "Global Admin", "global-viewer": "Global Viewer"}),
		users:     map[string][]string{"alice": {"alice-session"}},
	}

	store.DefaultStore = s //nolint:reassign // install test double; useRoles restores the original

	tests := map[string]struct {
		jwtKey, user, session string
		role                  string // empty for an error
	}{
		"no auth":              {"", "global-admin", "", "Global Admin"},
		"dev auth":             {"dev|alice|global-viewer", "alice", "", "Global Viewer"},
		"dev auth, other user": {"dev|alice|global-viewer", "bob", "", ""},
		"jwt":                  {"secret", "alice", "alice-session", "role of alice"},
		"proxy jwt":            {"proxy-jwt", "alice", "alice-session", "role of alice"},
		"signed out":           {"secret", "alice", "old-session", ""},
		"no session":           {"secret", "alice", "", ""},
		"deleted user":         {"secret", "bob", "bob-session", ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			role, err := ResolveRole(tc.jwtKey, tc.user, tc.session)

			switch {
			case tc.role == "" && err == nil:
				t.Errorf("ResolveRole(%q, %q, %q) = role %q, want an error", tc.jwtKey, tc.user, tc.session, role.Spec.Name)
			case tc.role != "" && err != nil:
				t.Errorf("ResolveRole(%q, %q, %q) error = %v, want role %q", tc.jwtKey, tc.user, tc.session, err, tc.role)
			case tc.role != "" && role.Spec.Name != tc.role:
				t.Errorf("ResolveRole(%q, %q, %q) = role %q, want %q", tc.jwtKey, tc.user, tc.session, role.Spec.Name, tc.role)
			}
		})
	}
}
