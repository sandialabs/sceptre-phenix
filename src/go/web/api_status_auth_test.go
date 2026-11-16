package web

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"

	"phenix/api/settings"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// authAnswer is the status of an answer and the code and message of its JSON
// Error body; Code and Message are empty for an answer without an error.
type authAnswer struct {
	Status  int
	Code    weberror.Code
	Message string
}

// authAnswerOf is the answer rec holds.
func authAnswerOf(t *testing.T, rec *httptest.ResponseRecorder) authAnswer {
	t.Helper()

	got := authAnswer{Status: rec.Code}

	if rec.Code >= http.StatusBadRequest {
		got.Message = errorMessage(t, rec.Code, rec.Body.String())
		got.Code = weberror.CodeFor(rec.Code)
	}

	return got
}

// authNotFound is the answer to a request for a user that does not exist.
var authNotFound = authAnswer{http.StatusNotFound, weberror.CodeNotFound, "user not found"} //nolint:gochecknoglobals // constant

// authStoreDown is a store whose Get of a user fails as a store that is down
// does.
type authStoreDown struct {
	store.Store
}

var errAuthStoreDown = errors.New("store down")

func (s authStoreDown) Get(c *store.Config) error {
	if c.Kind == "User" {
		return errAuthStoreDown
	}

	return s.Store.Get(c)
}

// The routes of one user answer 404 for a user that does not exist and 500,
// with a fixed message, when the store fails.
func TestUserRoutesOfAMissingUser(t *testing.T) {
	all := combinedRole(
		testRole(resourceUsers, verbGet, "*"), testRole(resourceUsers, verbPatch, "*"),
		testRole(resourceUsers, "delete", "*"), testRole("users/tokens", "create", "*"),
	)

	routes := map[string]struct {
		handler      func(http.ResponseWriter, *http.Request) error
		method, path string
		body         string
	}{
		"GET":         {GetUser, http.MethodGet, "/api/v1/users/nobody", ""},
		"PATCH":       {UpdateUser, http.MethodPatch, "/api/v1/users/nobody", `{"first_name":"x"}`},
		"DELETE":      {DeleteUser, http.MethodDelete, "/api/v1/users/nobody", ""},
		"POST tokens": {CreateUserToken, http.MethodPost, "/api/v1/users/nobody/tokens", `{"lifetime":"720h"}`},
	}

	for name, route := range routes {
		t.Run(name+" of a missing user", func(t *testing.T) {
			useUsersTestStore(t)

			req := roleRequest(route.method, route.path, route.body, map[string]string{"username": "nobody"}, all)

			_, rec := serveHandler(route.handler, req)
			if diff := cmp.Diff(authNotFound, authAnswerOf(t, rec)); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", route.method, route.path, diff)
			}
		})
	}

	for name, tc := range map[string]struct {
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		body    string
		want    authAnswer
	}{
		"GET":         {GetUser, http.MethodGet, "", authAnswer{http.StatusInternalServerError, weberror.CodeInternal, "unable to get user"}},
		"PATCH":       {UpdateUser, http.MethodPatch, `{}`, authAnswer{http.StatusInternalServerError, weberror.CodeInternal, "unable to get user"}},
		"POST tokens": {CreateUserToken, http.MethodPost, `{"lifetime":"1h"}`, authAnswer{http.StatusInternalServerError, weberror.CodeInternal, "unable to get user"}},
	} {
		t.Run(name+" with the store down", func(t *testing.T) {
			s := useUsersTestStore(t)
			createUserWithToken(t, "bob")
			storetest.Use(t, authStoreDown{Store: s})

			req := roleRequest(tc.method, "/api/v1/users/bob", tc.body, map[string]string{"username": "bob"}, all)

			_, rec := serveHandler(tc.handler, req)
			if diff := cmp.Diff(tc.want, authAnswerOf(t, rec)); diff != "" {
				t.Errorf("%s /users/bob with the store down mismatch (-want +got):\n%s", tc.method, diff)
			}
		})
	}
}

// A second DELETE of a user answers 404, and a store failure answers 500
// without the store's error text.
func TestDeleteUserTwice(t *testing.T) {
	s := useUsersTestStore(t)
	deleteAll := testRole(resourceUsers, "delete", "*")

	del := func() authAnswer {
		req := roleRequest(http.MethodDelete, "/api/v1/users/alice", "", map[string]string{"username": "alice"}, deleteAll)
		_, rec := serveHandler(DeleteUser, req)

		return authAnswerOf(t, rec)
	}

	if diff := cmp.Diff(authAnswer{Status: http.StatusNoContent}, del()); diff != "" {
		t.Fatalf("first DELETE /users/alice mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(authNotFound, del()); diff != "" {
		t.Errorf("second DELETE /users/alice mismatch (-want +got):\n%s", diff)
	}

	createUserWithToken(t, "alice")

	s.deleteErr = errAuthStoreDown

	want := authAnswer{http.StatusInternalServerError, weberror.CodeInternal, "unable to delete user"}
	if diff := cmp.Diff(want, del()); diff != "" {
		t.Errorf("DELETE /users/alice with the store down mismatch (-want +got):\n%s", diff)
	}
}

// A PATCH whose password change is refused changes nothing else either. The
// requester holds what the role it assigns grants.
func TestUpdateUserRefusedPasswordChangesNothing(t *testing.T) {
	role := combinedRole(testRole(resourceUsers, verbPatch, "bob"), testRole("users/roles", verbPatch, "bob"), adminRole())

	tests := map[string]struct {
		body  string
		want  authAnswer
		rules bool // the message ends with the password rules of the test's store
	}{
		"wrong current password": {
			body: `{"first_name":"New","last_name":"Name","role_name":"` + usersTestRole + `",` +
				`"password":"Wrongpass1!","new_password":"Newpass12!"}`,
			want: authAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "unable to update password"},
		},
		"weak new password": {
			body:  `{"first_name":"New","role_name":"` + usersTestRole + `","password":"` + testPassword + `","new_password":"weak"}`,
			want:  authAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "new password does not meet the requirements:\n"},
			rules: true,
		},
		"no current password": {
			body: `{"first_name":"New","role_name":"` + usersTestRole + `","new_password":"Newpass12!"}`,
			want: authAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "cannot change password without the current password"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useUsersTestStore(t)
			createUserWithToken(t, "bob")

			req := roleRequest(http.MethodPatch, "/api/v1/users/bob", tc.body, map[string]string{"username": "bob"}, role)

			_, rec := serveHandler(UpdateUser, req)

			want := tc.want
			if tc.rules {
				want.Message += settings.GetPasswordSettingsHTML()
			}

			if diff := cmp.Diff(want, authAnswerOf(t, rec)); diff != "" {
				t.Errorf("PATCH /users/bob mismatch (-want +got):\n%s", diff)
			}

			bob, err := rbac.GetUser("bob")
			if err != nil {
				t.Fatal(err)
			}

			got := []string{bob.FirstName(), bob.LastName(), bob.RoleName()}
			if diff := cmp.Diff([]string{"", "", "Viewer"}, got); diff != "" {
				t.Errorf("bob's names and role after a refused PATCH mismatch (-want +got):\n%s", diff)
			}

			if err := bob.ValidatePassword(testPassword); err != nil {
				t.Errorf("bob.ValidatePassword(old password) after a refused PATCH = %v, want nil", err)
			}
		})
	}
}

// A token lifetime of zero or less is refused, and no token is stored.
func TestCreateUserTokenRefusesAnExpiredLifetime(t *testing.T) {
	own := testRole(resourceUsers, verbPatch, "test-user")

	for _, lifetime := range []string{"-1h", "0", "-5", "0s"} {
		t.Run(lifetime, func(t *testing.T) {
			useUsersTestStore(t)
			createUserWithToken(t, "test-user")

			body := `{"lifetime":"` + lifetime + `"}`
			req := roleRequest(http.MethodPost, "/api/v1/users/test-user/tokens", body, map[string]string{"username": "test-user"}, own)

			_, rec := serveHandler(CreateUserToken, req)

			want := authAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "invalid token lifetime provided"}
			if diff := cmp.Diff(want, authAnswerOf(t, rec)); diff != "" {
				t.Errorf("POST /users/test-user/tokens with lifetime %s mismatch (-want +got):\n%s", lifetime, diff)
			}

			u, err := rbac.GetUser("test-user")
			if err != nil {
				t.Fatal(err)
			}

			if len(u.Spec.Tokens) != 1 {
				t.Errorf("test-user tokens after a refused token = %d, want 1 (the session's)", len(u.Spec.Tokens))
			}
		})
	}
}

// With dev auth, as with auth disabled, the session has no stored user, and
// signing out is a no-op that answers 204.
func TestLogoutWithoutAStoredUser(t *testing.T) {
	server := serveAs(t, "nobody", testRole(resourceUsers, verbGet, "nobody"))

	resp := do(t, http.MethodPost, server.URL+"/api/v1/logout", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
		t.Errorf("POST /api/v1/logout without a stored user = %d %q, want 204 with no body", resp.StatusCode, body)
	}
}

// A client that sends a stale token along may still sign in and sign up.
func TestSignInWithAStaleToken(t *testing.T) {
	useUsersTestStore(t)
	createUserWithToken(t, "bob")

	server := serveRouter(t, testPublic(), testJWTKey)

	sign := func(key string, exp time.Time) string {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "bob", "exp": exp.Unix()})

		signed, err := token.SignedString([]byte(key))
		if err != nil {
			t.Fatal(err)
		}

		return signed
	}

	stale := map[string]string{
		"expired":        sign(testJWTKey, time.Now().Add(-time.Hour)),
		"badly signed":   sign("another key", time.Now().Add(time.Hour)),
		"not even a JWT": "abc",
	}

	login := `{"user":"bob","pass":"` + testPassword + `"}`

	for name, token := range stale {
		t.Run(name+" in the header", func(t *testing.T) {
			resp := do(t, http.MethodPost, server.URL+"/api/v1/login", login, "X-Phenix-Auth-Token", "Bearer "+token)
			if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
				t.Errorf("POST /api/v1/login with a stale token = %d %s, want 200", resp.StatusCode, body)
			}
		})

		t.Run(name+" in the query", func(t *testing.T) {
			resp := do(t, http.MethodPost, server.URL+"/api/v1/login?token="+token, login)
			if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
				t.Errorf("POST /api/v1/login?token= with a stale token = %d %s, want 200", resp.StatusCode, body)
			}
		})

		t.Run(name+" on sign-up", func(t *testing.T) {
			// the handler runs, and refuses the taken name
			resp := do(t, http.MethodPost, server.URL+"/api/v1/signup",
				`{"username":"bob","password":"`+testPassword+`"}`, "X-Phenix-Auth-Token", "Bearer "+token)

			got := authAnswer{Status: resp.StatusCode}
			got.Message = errorMessage(t, resp.StatusCode, readBody(t, resp))
			got.Code = weberror.CodeFor(resp.StatusCode)

			want := authAnswer{http.StatusConflict, weberror.CodeConflict, "user bob already exists"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("POST /api/v1/signup with a stale token mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// another route still refuses the stale token
	resp := do(t, http.MethodGet, server.URL+"/api/v1/users", "", "X-Phenix-Auth-Token", "Bearer "+stale["expired"])
	if body := readBody(t, resp); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/users with an expired token = %d %s, want 401", resp.StatusCode, body)
	}
}

// GET /users answers an empty list, not null, when the role may list no
// stored user.
func TestGetUsersListsNoUserAsAnEmptyList(t *testing.T) {
	useUsersTestStore(t)

	req := roleRequest(http.MethodGet, "/api/v1/users", "", nil, testRole(resourceUsers, "list", "team-*"))

	_, rec := serveHandler(GetUsers, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /users = %d %s, want 200", rec.Code, rec.Body)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding GET /users %q: %v", rec.Body, err)
	}

	if diff := cmp.Diff(map[string]any{"users": []any{}}, got); diff != "" {
		t.Errorf("GET /users mismatch (-want +got):\n%s", diff)
	}
}

// methodAnswer is the status, Allow header and error code of an answer to a
// method a path may not take, or to OPTIONS.
type methodAnswer struct {
	Status int
	Allow  string
	Code   weberror.Code
}

// methodAnswerOf is the answer resp holds.
func methodAnswerOf(t *testing.T, resp *http.Response) methodAnswer {
	t.Helper()

	got := methodAnswer{Status: resp.StatusCode, Allow: resp.Header.Get("Allow")}

	if body := readBody(t, resp); resp.StatusCode >= http.StatusBadRequest {
		var e struct {
			Code weberror.Code `json:"code"`
		}

		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("%d body %q is not a JSON Error: %v", resp.StatusCode, body, err)
		}

		got.Code = e.Code
	}

	return got
}

// A method a path does not take answers 405 with the JSON Error and Allow,
// and OPTIONS answers 204 with Allow, under /api/v1, on the Unix socket and
// at the root.
func TestMethodsAllowed(t *testing.T) {
	useUsersTestStore(t)

	var (
		server = serveRouter(t, testPublic(), testJWTKey)
		socket = httptest.NewServer(newSocketRouter())
	)

	t.Cleanup(socket.Close)

	refused := func(allow string) methodAnswer {
		return methodAnswer{http.StatusMethodNotAllowed, allow, weberror.CodeInvalidRequest}
	}

	options := func(allow string) methodAnswer {
		return methodAnswer{Status: http.StatusNoContent, Allow: allow}
	}

	tests := map[string]struct {
		base, method, path string
		want               methodAnswer
	}{
		"DELETE /configs":        {server.URL, http.MethodDelete, "/api/v1/configs", refused("GET, POST, OPTIONS")},
		"PUT /configs":           {server.URL, http.MethodPut, "/api/v1/configs", refused("GET, POST, OPTIONS")},
		"DELETE /settings":       {server.URL, http.MethodDelete, "/api/v1/settings", refused("GET, POST, OPTIONS")},
		"PUT /users/x":           {server.URL, http.MethodPut, "/api/v1/users/x", refused("GET, PATCH, DELETE, OPTIONS")},
		"POST /jobs":             {server.URL, http.MethodPost, "/api/v1/jobs", refused("GET, OPTIONS")},
		"OPTIONS /configs":       {server.URL, http.MethodOptions, "/api/v1/configs", options("GET, POST, OPTIONS")},
		"OPTIONS /users/x":       {server.URL, http.MethodOptions, "/api/v1/users/x", options("GET, PATCH, DELETE, OPTIONS")},
		"OPTIONS builder drafts": {server.URL, http.MethodOptions, "/api/v1/builder/drafts", options("GET, POST, OPTIONS")},
		"POST /version":          {server.URL, http.MethodPost, "/version", refused("GET")},
		"POST /features":         {server.URL, http.MethodPost, "/features", refused("GET")},
		"DELETE /healthz":        {server.URL, http.MethodDelete, "/healthz", refused("GET, HEAD")},
		"PUT /readyz":            {server.URL, http.MethodPut, "/readyz", refused("GET, HEAD")},
		"socket DELETE /jobs/x":  {socket.URL, http.MethodDelete, "/api/v1/jobs/x", refused("GET")},
		"socket OPTIONS /jobs":   {socket.URL, http.MethodOptions, "/api/v1/jobs", refused("GET")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, tc.method, tc.base+tc.path, "")
			if diff := cmp.Diff(tc.want, methodAnswerOf(t, resp)); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
		})
	}
}

// FuzzTokenLifetime checks that a lifetime that TokenLifetime takes is more
// than zero, and is the duration or the number of days the text gives. A day
// count whose lifetime a [time.Duration] cannot hold is refused.
func FuzzTokenLifetime(f *testing.F) {
	for _, seed := range []string{
		"720h", "30", "0", "-1h", "-5", "0s", "1ns", "forever", "", "9999999999999",
		"106751", "106752", "213600", "106751992",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		got, ok := TokenLifetime(s)
		if ok && got <= 0 {
			t.Fatalf("TokenLifetime(%q) = %v, true, want a lifetime more than zero", s, got)
		}

		if d, err := time.ParseDuration(s); err == nil && d > 0 && (!ok || got != d) {
			t.Errorf("TokenLifetime(%q) = %v, %t, want %v, true", s, got, ok, d)
		}

		if days, err := strconv.ParseInt(s, 10, 64); err == nil {
			const day = hoursInDay * time.Hour

			// the days as a float, which cannot wrap, tell whether the lifetime fits
			fits := days > 0 && float64(days)*float64(day) < math.MaxInt64

			if fits && (!ok || got%day != 0 || int64(got/day) != days) {
				t.Errorf("TokenLifetime(%q) = %v, %t, want %d days, true", s, got, ok, days)
			}

			if !fits && ok {
				t.Errorf("TokenLifetime(%q) = %v, true, want false for a lifetime a time.Duration cannot hold", s, got)
			}
		}
	})
}
