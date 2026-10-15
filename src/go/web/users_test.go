package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/api/config"
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
		t.Fatal(err)
	}
}

// roleRequest is a request made by test-user with the role and route variables.
func roleRequest(method, target, body string, vars map[string]string, role rbac.Role) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
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
			t.Errorf("%s: status %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
	}

	if _, err := rbac.GetUser("ghost"); err == nil {
		t.Error("a user with an unknown role was stored")
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatal(err)
	}

	if user.RoleName() != experimentUserRole {
		t.Errorf("role = %q, want %q", user.RoleName(), experimentUserRole)
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
		t.Fatal(err)
	}

	for _, name := range []string{"ghost", "short"} {
		if _, err := rbac.GetUser(name); err == nil {
			t.Errorf("user %s was stored", name)
		}
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatal(err)
	}

	if user.RoleName() != experimentUserRole {
		t.Errorf("role = %q, want %q", user.RoleName(), experimentUserRole)
	}
}
