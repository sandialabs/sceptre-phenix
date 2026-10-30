package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// A dev key names a user and a role in exactly three fields; Auth refuses
// every request for any other dev| key, and Authenticator accepts none.
func TestDevAuthKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		key        string
		user, role string
		ok         bool
	}{
		"user and role": {key: "dev|alice|global-viewer", user: "alice", role: "global-viewer", ok: true},
		"no role":       {key: "dev|alice"},
		"empty role":    {key: "dev|alice|"},
		"empty user":    {key: "dev||global-viewer"},
		"bare prefix":   {key: "dev|"},
		"extra field":   {key: "dev|alice|global-viewer|x"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			user, role, ok := DevAuthCredentials(tc.key)
			if user != tc.user || role != tc.role || ok != tc.ok {
				t.Errorf("DevAuthCredentials(%q) = %q, %q, %t, want %q, %q, %t", tc.key, user, role, ok, tc.user, tc.role, tc.ok)
			}

			if got := Authenticator(tc.key, "")(httptest.NewRequest(http.MethodGet, "/", nil)); got != tc.ok {
				t.Errorf("Authenticator(%q)(GET /) = %t, want %t", tc.key, got, tc.ok)
			}

			if tc.ok {
				return
			}

			reached := false
			h := Auth(tc.key, "")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil))

			if rec.Code != http.StatusInternalServerError || reached {
				t.Errorf("Auth(%q) GET = %d, reached handler %t; want 500 and not reached", tc.key, rec.Code, reached)
			}

			if _, err := ResolveRole(tc.key, "alice", ""); err == nil {
				t.Errorf("ResolveRole(%q, alice) = nil error, want one", tc.key)
			}
		})
	}
}

// DevAuthCredentials gives back the user and role of any key built from
// them, and any key it accepts is dev|<user>|<role>.
func TestDevAuthCredentialsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		field := rapid.StringMatching(`[^|]{1,12}`)
		user, role := field.Draw(t, "user"), field.Draw(t, "role")

		if u, r, ok := DevAuthCredentials("dev|" + user + "|" + role); !ok || u != user || r != role {
			t.Fatalf("DevAuthCredentials(dev|%s|%s) = %q, %q, %t", user, role, u, r, ok)
		}

		key := rapid.OneOf(rapid.String(), rapid.StringMatching(`dev(\|[a-z]{0,3}){0,4}`)).Draw(t, "key")
		if u, r, ok := DevAuthCredentials(key); ok && (key != "dev|"+u+"|"+r || u == "" || r == "" || strings.Contains(u+r, "|")) {
			t.Fatalf("DevAuthCredentials(%q) = %q, %q, true", key, u, r)
		}
	})
}
