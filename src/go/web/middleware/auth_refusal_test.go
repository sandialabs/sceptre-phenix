package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// refused is what Auth told its RefusalFunc of a request.
type refused struct {
	Path, Reason, User string
	Verified           bool
}

// recordRefusals returns an option recording the refusals Auth reports, and
// the function listing them.
func recordRefusals() (AuthOption, func() []refused) {
	var got []refused

	return OnRefusal(func(r *http.Request, ref Refusal) {
			got = append(got, refused{Path: r.URL.Path, Reason: ref.Reason, User: ref.User, Verified: ref.Verified})
		}), func() []refused {
			defer func() { got = nil }()

			return got
		}
}

// claimsToken returns a token with claims signed with key.
func claimsToken(t *testing.T, claims jwt.MapClaims, key string) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("signing a token: %v", err)
	}

	return signed
}

// Auth tells its RefusalFunc why it refused each request, and the user the
// token names when it read one, and refuses with the same status as before.
//
//nolint:paralleltest // replaces the store
func TestAuthReportsRefusals(t *testing.T) {
	useBoltStore(t)

	user, err := rbac.NewUser("alice", "password", "", "")
	if err != nil {
		t.Fatalf("rbac.NewUser(alice) error = %v", err)
	}

	if err := user.SetRole(&rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}); err != nil {
		t.Fatalf("SetRole(Global Viewer) error = %v", err)
	}

	valid := signedToken(t, "alice", testJWTKey)
	if err := user.AddToken(valid, "test"); err != nil {
		t.Fatalf("AddToken() error = %v", err)
	}

	var (
		hour     = time.Hour
		expired  = claimsToken(t, jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(-hour).Unix()}, testJWTKey)
		noUser   = claimsToken(t, jwt.MapClaims{"exp": time.Now().Add(hour).Unix()}, testJWTKey)
		mallory  = signedToken(t, "mallory", testJWTKey)
		otherKey = signedToken(t, "alice", "other-key")
		// a token alice never held, which a later expiry tells from valid
		revoked = claimsToken(t, jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(2 * hour).Unix()}, testJWTKey)
	)

	opt, refusals := recordRefusals()
	auth := Auth(testJWTKey, "", opt)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct {
		name, header string
		status       int
		want         []refused
	}{
		{"no token", "", http.StatusForbidden, []refused{{"/api/v1/vms", RefusedMissing, "", true}}},
		{"a header not of a bearer token", "Token " + valid, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedMalformed, "", true}}},
		{"a token of too few segments", "Bearer garbage", http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedMalformed, "", true}}},
		{"a token signed with another key", "Bearer " + otherKey, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedInvalid, "", true}}},
		{"an expired token", "Bearer " + expired, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedExpired, "", true}}},
		{"a token naming no user", "Bearer " + noUser, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedInvalid, "", true}}},
		{"a token of an unknown user", "Bearer " + mallory, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedUnknownUser, "mallory", true}}},
		{"a token its user does not hold", "Bearer " + revoked, http.StatusUnauthorized, []refused{{"/api/v1/vms", RefusedRevoked, "alice", true}}},
		{"a valid token", "Bearer " + valid, http.StatusOK, nil},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/vms", nil)
		if tc.header != "" {
			r.Header.Set("X-Phenix-Auth-Token", tc.header)
		}

		w := httptest.NewRecorder()
		auth.ServeHTTP(w, r)

		if w.Code != tc.status {
			t.Errorf("GET /api/v1/vms with %s = %d, want %d", tc.name, w.Code, tc.status)
		}

		if diff := cmp.Diff(tc.want, refusals()); diff != "" {
			t.Errorf("GET /api/v1/vms with %s refusals mismatch (-want +got):\n%s", tc.name, diff)
		}
	}

	proxyOpt, proxyRefusals := recordRefusals()
	proxied := Auth(testJWTKey, "X-Forwarded-User", proxyOpt)(http.NotFoundHandler())

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/vms", nil)
	r.Header.Set("X-Phenix-Auth-Token", "Bearer "+valid)
	r.Header.Set("X-Forwarded-User", "bob")

	proxied.ServeHTTP(httptest.NewRecorder(), r)

	if diff := cmp.Diff([]refused{{"/api/v1/vms", RefusedProxyUser, "alice", true}}, proxyRefusals()); diff != "" {
		t.Errorf("a proxy user header naming another user: refusals mismatch (-want +got):\n%s", diff)
	}

	// a proxy JWT's signature is not checked, so the user it names is not verified
	forgedOpt, forgedRefusals := recordRefusals()
	forged := Auth("proxy-jwt", "", forgedOpt)(http.NotFoundHandler())

	r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/vms", nil)
	r.Header.Set("X-Phenix-Auth-Token", "Bearer "+otherKey)

	w := httptest.NewRecorder()
	forged.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/vms with a forged proxy JWT = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	if diff := cmp.Diff([]refused{{"/api/v1/vms", RefusedRevoked, "alice", false}}, forgedRefusals()); diff != "" {
		t.Errorf("a forged proxy JWT naming alice: refusals mismatch (-want +got):\n%s", diff)
	}

	// a health probe's refusal names the path probed, not the one Authenticator checks
	probeOpt, probeRefusals := recordRefusals()

	if Authenticator(testJWTKey, "", probeOpt)(request(t, expired, true)) {
		t.Error("Authenticator(key, \"\")(an expired token) = true, want false")
	}

	if diff := cmp.Diff([]refused{{"/readyz", RefusedExpired, "", true}}, probeRefusals()); diff != "" {
		t.Errorf("Authenticator refusals mismatch (-want +got):\n%s", diff)
	}
}

// With proxy JWTs, a request without the proxy's token, or with one that does
// not parse, is reported as refused.
func TestProxyJWTReportsRefusals(t *testing.T) {
	opt, refusals := recordRefusals()
	auth := Auth("proxy-jwt", "", opt)(http.NotFoundHandler())

	for _, tc := range []struct {
		name, header, want string
	}{
		{"no token", "", RefusedMissing},
		{"a header not of a bearer token", "Token abc", RefusedMalformed},
		{"a token that does not parse", "Bearer garbage", RefusedMalformed},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/vms", nil)
		if tc.header != "" {
			r.Header.Set("X-Phenix-Auth-Token", tc.header)
		}

		w := httptest.NewRecorder()
		auth.ServeHTTP(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/vms with %s = %d, want %d", tc.name, w.Code, http.StatusBadRequest)
		}

		if diff := cmp.Diff([]refused{{"/api/v1/vms", tc.want, "", false}}, refusals()); diff != "" {
			t.Errorf("GET /api/v1/vms with %s refusals mismatch (-want +got):\n%s", tc.name, diff)
		}
	}
}
