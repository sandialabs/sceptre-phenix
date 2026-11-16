package web

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/web/rbac"
)

// testUserProxyToken is the proxy token of test-user as the API gives it:
// base64, which the UI decodes before it copies it.
var testUserProxyToken = base64.StdEncoding.EncodeToString([]byte("test-user-proxy-jwt")) //nolint:gochecknoglobals // constant

// usersListed are the user names and proxy tokens of a GET /users answer.
func usersListed(t *testing.T, body []byte) map[string]string {
	t.Helper()

	var resp struct {
		Users []User `json:"users"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding users %q: %v", body, err)
	}

	got := make(map[string]string, len(resp.Users))
	for _, u := range resp.Users {
		got[u.Username] = u.ProxyToken
	}

	return got
}

// useProxiedUsers stores test-user and bob, each with a proxy session, in a
// store that can be made to fail.
func useProxiedUsers(t *testing.T) *usersTestStore {
	t.Helper()

	s := useUsersTestStore(t)

	for _, name := range []string{"test-user", "bob"} {
		createUserWithToken(t, name)

		u, err := rbac.GetUser(name)
		if err != nil {
			t.Fatal(err)
		}

		if err := u.AddToken(name+"-proxy-jwt", "proxied"); err != nil {
			t.Fatal(err)
		}
	}

	return s
}

// GET /users lists the users the role may list, or only the requester, and
// gives the proxy token of the requester alone.
func TestGetUsersBranches(t *testing.T) {
	tests := map[string]struct {
		role       rbac.Role
		storeFails func(*usersTestStore)
		want       handlerAnswer
		users      map[string]string
	}{
		"list all": {
			role: testRole(resourceUsers, "list", "*"), want: handlerAnswer{Status: http.StatusOK},
			users: map[string]string{"alice": "", "bob": "", "test-user": testUserProxyToken},
		},
		"list some": {
			role: testRole(resourceUsers, "list", "bob"), want: handlerAnswer{Status: http.StatusOK},
			users: map[string]string{"bob": ""},
		},
		"only self": {
			role: testRole(resourceUsers, verbGet, "test-user"), want: handlerAnswer{Status: http.StatusOK},
			users: map[string]string{"test-user": testUserProxyToken},
		},
		"forbidden": {
			role: testRole(resourceUsers, verbGet, "bob"), want: handlerAnswer{http.StatusForbidden, "forbidden"},
		},
		"list fails": {
			role:       testRole(resourceUsers, "list"),
			storeFails: func(s *usersTestStore) { s.listErr, s.listKind = errStoreDown, "User" },
			want:       handlerAnswer{http.StatusInternalServerError, "unable to list users"},
		},
		"self not found": {
			role:       testRole(resourceUsers, verbGet, "test-user"),
			storeFails: func(s *usersTestStore) { s.hideUsers = true },
			want:       handlerAnswer{Status: http.StatusOK},
			users:      map[string]string{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useProxiedUsers(t)
			if tc.storeFails != nil {
				tc.storeFails(s)
			}

			got, rec := serveHandler(GetUsers, roleRequest(http.MethodGet, "/api/v1/users", "", nil, tc.role))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("GET /users mismatch (-want +got):\n%s", diff)
			}

			if tc.users == nil {
				return
			}

			if diff := cmp.Diff(tc.users, usersListed(t, rec.Body.Bytes())); diff != "" {
				t.Errorf("GET /users users and proxy tokens mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// GET /users/{username} gives a user the role may get, with the proxy token
// only when it is the requester.
func TestGetUserBranches(t *testing.T) {
	getAll := testRole(resourceUsers, verbGet, "*")

	tests := map[string]struct {
		user  string
		role  rbac.Role
		want  handlerAnswer
		proxy string
	}{
		"self":  {user: "test-user", role: getAll, want: handlerAnswer{Status: http.StatusOK}, proxy: testUserProxyToken},
		"other": {user: "bob", role: getAll, want: handlerAnswer{Status: http.StatusOK}},
		"forbidden": {
			user: "bob",
			role: testRole(resourceUsers, verbGet, "test-user"),
			want: handlerAnswer{http.StatusForbidden, "forbidden"},
		},
		"not found": {user: "nobody", role: getAll, want: handlerAnswer{http.StatusNotFound, "user not found"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useProxiedUsers(t)

			req := roleRequest(http.MethodGet, "/api/v1/users/"+tc.user, "", map[string]string{"username": tc.user}, tc.role)

			got, rec := serveHandler(GetUser, req)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("GET /users/%s mismatch (-want +got):\n%s", tc.user, diff)
			}

			if tc.want.Status != http.StatusOK {
				return
			}

			var u User
			if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
				t.Fatal(err)
			}

			if u.Username != tc.user || u.ProxyToken != tc.proxy {
				t.Errorf(
					"GET /users/%s = user %q with proxy token %q, want %q with %q",
					tc.user,
					u.Username,
					u.ProxyToken,
					tc.user,
					tc.proxy,
				)
			}
		})
	}
}

// PATCH /users/{username} changes names, the role (only with users/roles
// patch) and the password (only with the current one).
func TestUpdateUserBranches(t *testing.T) {
	patch := testRole(resourceUsers, verbPatch, "bob")
	assign := combinedRole(patch, testRole("users/roles", verbPatch, "bob"))
	patchRoles := combinedRole(assign, adminRole())

	type result struct {
		First, Role string
		Password    string // the password bob then signs in with
	}

	unchanged := result{First: "", Role: "Viewer", Password: testPassword}

	tests := map[string]struct {
		role       rbac.Role
		body       string
		storeFails bool
		want       handlerAnswer
		result     result
		denied     bool // a role change that is refused or fails is audited as a failure
	}{
		"names": {
			role: patch, body: `{"first_name":"Bobby","last_name":"Tables"}`,
			want: handlerAnswer{Status: http.StatusOK}, result: result{First: "Bobby", Role: "Viewer", Password: testPassword},
		},
		"names not saved": {
			role: patch, body: `{"last_name":"Tables"}`, storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, "unable to update user"}, result: unchanged,
		},
		"first name not saved": {
			role: patch, body: `{"first_name":"Bobby"}`, storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, "unable to update user"}, result: unchanged,
		},
		"role without users/roles": {
			role: patch, body: `{"role_name":"` + usersTestRole + `"}`,
			want: handlerAnswer{Status: http.StatusOK}, result: unchanged, denied: true,
		},
		"role": {
			role: patchRoles, body: `{"role_name":"` + usersTestRole + `"}`,
			want: handlerAnswer{Status: http.StatusOK}, result: result{Role: usersTestRole, Password: testPassword},
		},
		"a role the requester does not hold": {
			role: assign, body: `{"first_name":"Bobby","role_name":"` + usersTestRole + `"}`,
			want:   handlerAnswer{http.StatusForbidden, "the role would grant permissions that you do not hold: * on *, * on * for *"},
			result: unchanged, denied: true,
		},
		"unknown role": {
			role: patchRoles, body: `{"role_name":"No Such Role"}`,
			want: handlerAnswer{http.StatusBadRequest, "role not found"}, result: unchanged,
		},
		"role not saved": {
			role: patchRoles, body: `{"role_name":"` + usersTestRole + `"}`, storeFails: true,
			want: handlerAnswer{http.StatusInternalServerError, "unable to update user role"}, result: unchanged, denied: true,
		},
		"password": {
			role: patch, body: `{"password":"` + testPassword + `","new_password":"Newpass12!"}`,
			want: handlerAnswer{Status: http.StatusOK}, result: result{Role: "Viewer", Password: "Newpass12!"},
		},
		"password without the current one": {
			role: patch, body: `{"new_password":"Newpass12!"}`,
			want: handlerAnswer{http.StatusBadRequest, "cannot change password without the current password"}, result: unchanged,
		},
		"weak new password": {
			role: patch, body: `{"password":"` + testPassword + `","new_password":"x"}`,
			want: handlerAnswer{Status: http.StatusBadRequest}, result: unchanged,
		},
		"wrong current password": {
			role: patch, body: `{"password":"Wrongpass1!","new_password":"Newpass12!"}`,
			want: handlerAnswer{http.StatusBadRequest, "unable to update password"}, result: unchanged,
		},
		"bad JSON": {
			role: patch, body: `{`, want: handlerAnswer{Status: http.StatusBadRequest}, result: unchanged,
		},
		"forbidden": {
			role: testRole(resourceUsers, verbPatch, "alice"), body: `{"first_name":"Bobby"}`,
			want: handlerAnswer{http.StatusForbidden, "forbidden"}, result: unchanged,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)
			createUserWithToken(t, "bob")
			events := captureAudit(t)

			if tc.storeFails {
				s.updateErr = errStoreDown
			}

			req := roleRequest(http.MethodPatch, "/api/v1/users/bob", tc.body, map[string]string{"username": "bob"}, tc.role)

			got, rec := serveHandler(UpdateUser, req)
			if tc.want.Status == http.StatusBadRequest && tc.want.Message == "" {
				got.Message = "" // the password rules or the JSON decoder's error
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("PATCH /users/bob mismatch (-want +got):\n%s\n%s", diff, rec.Body)
			}

			s.updateErr = nil

			bob, err := rbac.GetUser("bob")
			if err != nil {
				t.Fatal(err)
			}

			gotResult := result{First: bob.FirstName(), Role: bob.RoleName(), Password: tc.result.Password}
			if err := bob.ValidatePassword(tc.result.Password); err != nil {
				gotResult.Password = "not " + tc.result.Password
			}

			if diff := cmp.Diff(tc.result, gotResult); diff != "" {
				t.Errorf("bob after PATCH /users/bob mismatch (-want +got):\n%s", diff)
			}

			if tc.denied {
				wantAudit(t, events(), "user-role-change", "failure", map[string]any{"user.target.name": "bob"})
			}
		})
	}

	t.Run("unknown user", func(t *testing.T) {
		useUsersTestStore(t)

		req := roleRequest(
			http.MethodPatch,
			"/api/v1/users/nobody",
			`{}`,
			map[string]string{"username": "nobody"},
			testRole(resourceUsers, verbPatch, "*"),
		)

		want := handlerAnswer{http.StatusNotFound, "user not found"}
		if got, _ := serveHandler(UpdateUser, req); got != want {
			t.Errorf("PATCH /users/nobody = %+v, want %+v", got, want)
		}
	})

	t.Run("unreadable body", func(t *testing.T) {
		useUsersTestStore(t)

		req := roleRequest(http.MethodPatch, "/api/v1/users/bob", "", map[string]string{"username": "bob"}, patch)
		req.Body = io.NopCloser(failingReader{})

		if got, _ := serveHandler(UpdateUser, req); got.Status != http.StatusInternalServerError {
			t.Errorf("PATCH /users/bob with an unreadable body = %+v, want 500", got)
		}
	})
}

// POST /users refuses a request it cannot read, a weak password, and removes
// the user again when its role cannot be saved.
func TestCreateUserBranches(t *testing.T) {
	create := combinedRole(testRole(resourceUsers, "create"), adminRole())

	for name, body := range map[string]string{"bad JSON": `{`, "not JSON": `username=carol`} {
		t.Run(name, func(t *testing.T) {
			useUsersTestStore(t)

			got, _ := serveHandler(CreateUser, roleRequest(http.MethodPost, "/api/v1/users", body, nil, create))
			if got.Status != http.StatusBadRequest {
				t.Errorf("POST /users of %q = %+v, want status %d", body, got, http.StatusBadRequest)
			}

			if _, err := rbac.GetUser("carol"); err == nil {
				t.Errorf("POST /users of %q stored carol", body)
			}
		})
	}

	t.Run("unreadable body", func(t *testing.T) {
		useUsersTestStore(t)

		req := roleRequest(http.MethodPost, "/api/v1/users", "", nil, create)
		req.Body = io.NopCloser(failingReader{})

		if got, _ := serveHandler(CreateUser, req); got.Status != http.StatusInternalServerError {
			t.Errorf("POST /users with an unreadable body = %+v, want 500", got)
		}
	})

	t.Run("weak password", func(t *testing.T) {
		useUsersTestStore(t)

		body := `{"username":"carol","password":"x","role_name":"` + usersTestRole + `"}`
		if got, _ := serveHandler(
			CreateUser,
			roleRequest(http.MethodPost, "/api/v1/users", body, nil, create),
		); got.Status != http.StatusBadRequest {
			t.Errorf("POST /users with a weak password = %+v, want 400", got)
		}

		if _, err := rbac.GetUser("carol"); err == nil {
			t.Error("POST /users with a weak password created carol")
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		useUsersTestStore(t)

		body := createUserBody("carol", usersTestRole)

		want := handlerAnswer{http.StatusForbidden, "forbidden"}
		if got, _ := serveHandler(
			CreateUser,
			roleRequest(http.MethodPost, "/api/v1/users", body, nil, testRole(resourceUsers, "list")),
		); got != want {
			t.Errorf("POST /users without users create = %+v, want %+v", got, want)
		}
	})

	t.Run("role not saved", func(t *testing.T) {
		s := useUsersTestStore(t)
		s.updateErr = errStoreDown

		body := createUserBody("carol", usersTestRole)

		want := handlerAnswer{http.StatusInternalServerError, "error setting user role"}
		if got, _ := serveHandler(CreateUser, roleRequest(http.MethodPost, "/api/v1/users", body, nil, create)); got != want {
			t.Errorf("POST /users with the store down = %+v, want %+v", got, want)
		}

		if _, err := rbac.GetUser("carol"); err == nil {
			t.Error("POST /users left carol, without a role, behind")
		}
	})
}

// DELETE /users/{username} refuses the requester's own user and reports a
// user it cannot delete.
func TestDeleteUserBranches(t *testing.T) {
	deleteAll := testRole(resourceUsers, "delete", "*")

	tests := map[string]struct {
		user       string
		storeFails bool
		want       handlerAnswer
	}{
		"own user":     {user: "test-user", want: handlerAnswer{http.StatusForbidden, "you cannot delete your own user"}},
		"unknown user": {user: "nobody", want: handlerAnswer{http.StatusNotFound, "user not found"}},
		"store down":   {user: "alice", storeFails: true, want: handlerAnswer{Status: http.StatusInternalServerError}},
		"deleted":      {user: "alice", want: handlerAnswer{Status: http.StatusNoContent}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)
			if tc.storeFails {
				s.deleteErr = errStoreDown
			}

			req := roleRequest(http.MethodDelete, "/api/v1/users/"+tc.user, "", map[string]string{"username": tc.user}, deleteAll)

			got, _ := serveHandler(DeleteUser, req)
			if tc.want.Status == http.StatusInternalServerError {
				got.Message = "" // the store's error
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("DELETE /users/%s mismatch (-want +got):\n%s", tc.user, diff)
			}

			_, err := rbac.GetUser("alice")
			if deleted := err != nil; deleted != (name == "deleted") {
				t.Errorf("after DELETE /users/%s alice deleted = %t, want %t", tc.user, deleted, name == "deleted")
			}
		})
	}
}
