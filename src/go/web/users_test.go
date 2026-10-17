package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

const (
	testPassword       = "Testpass1!"
	experimentUserRole = "Experiment User"
)

// useDefaultRoles gives the test a store of its own holding the default roles.
func useDefaultRoles(t *testing.T) {
	t.Helper()

	storetest.Use(t)

	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
}

// roleRequest is a JSON request made by test-user with the role and route
// variables.
func roleRequest(method, target, body string, vars map[string]string, role rbac.Role) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", mimeJSON)
	req = mux.SetURLVars(req, vars)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

func createUserBody(username, role string) string {
	return `{"username":"` + username + `","password":"` + testPassword + `","first_name":"t","last_name":"t",` +
		`"role_name":"` + role + `","resource_names":["exp-a"]}`
}

// POST /users stores nothing for an unknown role and answers 409 for a taken
// username instead of failing partway through.
//
//nolint:paralleltest // replaces the store
func TestCreateUserChecksRoleAndName(t *testing.T) {
	useDefaultRoles(t)

	usersCreate := testRole(resourceUsers, "create")

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"unknown role", createUserBody("ghost", "No Such Role"), http.StatusBadRequest},
		{"no role", createUserBody("ghost", ""), http.StatusBadRequest},
		{"known role", createUserBody("real", experimentUserRole), http.StatusOK},
		{"taken username", createUserBody("real", experimentUserRole), http.StatusConflict},
	} {
		w := httptest.NewRecorder()
		CreateUser(w, roleRequest(http.MethodPost, "/api/v1/users", tc.body, nil, usersCreate))

		if w.Code != tc.want {
			t.Errorf("CreateUser(%s) = %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}

	if _, err := rbac.GetUser("ghost"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("rbac.GetUser(ghost) after CreateUser with an unknown role error = %v, want %v", err, store.ErrNotExist)
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatalf("rbac.GetUser(real) error = %v", err)
	}

	if got := user.RoleName(); got != experimentUserRole {
		t.Errorf("rbac.GetUser(real) role = %q, want %q", got, experimentUserRole)
	}
}

// phenix ui --users stores no user whose role does not exist, and skips a
// malformed entry.
//
//nolint:paralleltest // replaces the store
func TestConfigureUsersSkipsUnknownRole(t *testing.T) {
	useDefaultRoles(t)

	err := ConfigureUsers([]string{
		"ghost:" + testPassword + ":No Such Role",
		"short",
		"real:" + testPassword + ":" + experimentUserRole + ":exp-a",
	})
	if err != nil {
		t.Fatalf("ConfigureUsers() error = %v", err)
	}

	for _, name := range []string{"ghost", "short"} {
		if _, err := rbac.GetUser(name); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("rbac.GetUser(%s) after ConfigureUsers error = %v, want %v", name, err, store.ErrNotExist)
		}
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatalf("rbac.GetUser(real) error = %v", err)
	}

	if got := user.RoleName(); got != experimentUserRole {
		t.Errorf("rbac.GetUser(real) role = %q, want %q", got, experimentUserRole)
	}
}

// users patch lets a user create their own tokens; a token for another user
// also needs users/tokens create for that user (the contract tests cover the rest).
//
//nolint:paralleltest // replaces the store
func TestCreateUserTokenForOtherUser(t *testing.T) {
	storetest.Use(t)
	createUserWithToken(t, "test-user")
	createUserWithToken(t, "bob")

	original := o
	t.Cleanup(func() { o = original })

	o.jwtKey = testJWTKey

	patch := func(name string) rbac.Role { return testRole(resourceUsers, verbPatch, name) }
	tokens := func(name string) rbac.Role { return testRole("users/tokens", "create", name) }

	for _, tc := range []struct {
		name, target string
		role         rbac.Role
		want         int
	}{
		{"own token", "test-user", patch("test-user"), http.StatusOK},
		{"users/tokens for someone else", "bob", combinedRole(patch("bob"), tokens("carol")), http.StatusForbidden},
	} {
		w := httptest.NewRecorder()
		vars := map[string]string{"username": tc.target}
		CreateUserToken(w, roleRequest(http.MethodPost, "/api/v1/users/"+tc.target+"/tokens", `{"lifetime":"1h"}`, vars, tc.role))

		if w.Code != tc.want {
			t.Errorf("CreateUserToken(%s) for %s = %d, want %d: %s", tc.name, tc.target, w.Code, tc.want, w.Body.String())
		}
	}
}
