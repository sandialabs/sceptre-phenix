package middleware

import (
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

// The no-auth and dev-auth modes give every request the same stored role:
// they look it up once and reuse it, and answer 500 without caching the
// failure when it cannot be found.
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
					t.Fatalf("status = %d, want %d", rec.Code, tc.status)
				}
			}

			if seen != tc.context {
				t.Errorf("handler saw %q, want %q", seen, tc.context)
			}

			if n := s.lists.Load(); n != tc.lookups {
				t.Errorf("roles listed %d times, want %d", n, tc.lookups)
			}
		})
	}
}
