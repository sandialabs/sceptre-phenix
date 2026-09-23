package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"phenix/api/config"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

func createUserBody(username, role string) string {
	return `{"username":"` + username + `","password":"Testpass1!","first_name":"t","last_name":"t",` +
		`"role_name":"` + role + `","resource_names":["exp-a"]}`
}

// TestCreateUserChecksRoleAndName verifies CreateUser stores nothing for an
// unknown role and rejects existing usernames instead of failing mid-request.
func TestCreateUserChecksRoleAndName(t *testing.T) {
	useTestStore(t)

	if err := config.CreateDefaults("role/experiment-user"); err != nil {
		t.Fatalf("creating default role: %v", err)
	}

	usersCreate := &v1.PolicySpec{Resources: []string{"users"}, ResourceNames: nil, Verbs: []string{"create"}}

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "unknown role", body: createUserBody("ghost", "No Such Role"), want: http.StatusBadRequest},
		{name: "empty role", body: createUserBody("ghost", ""), want: http.StatusBadRequest},
		{name: "known role", body: createUserBody("real", "Experiment User"), want: http.StatusOK},
		{name: "existing user", body: createUserBody("real", "Experiment User"), want: http.StatusConflict},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		CreateUser(rec, roleRequest(http.MethodPost, "/users", test.body, nil, usersCreate))

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d (%s)", test.name, rec.Code, test.want, rec.Body.String())
		}
	}

	if _, err := rbac.GetUser("ghost"); err == nil {
		t.Error("user with an unknown role was stored")
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatalf("getting created user: %v", err)
	}

	if user.RoleName() != "Experiment User" {
		t.Errorf("unexpected role: %q", user.RoleName())
	}
}

// TestConfigureUsersSkipsUnknownRole verifies --users does not store users
// whose role doesn't exist.
func TestConfigureUsersSkipsUnknownRole(t *testing.T) {
	useTestStore(t)

	if err := config.CreateDefaults("role/experiment-user"); err != nil {
		t.Fatalf("creating default role: %v", err)
	}

	err := ConfigureUsers([]string{
		"ghost:Testpass1!:No Such Role",
		"real:Testpass1!:Experiment User:exp-a",
	})
	if err != nil {
		t.Fatalf("configuring users: %v", err)
	}

	if _, err := rbac.GetUser("ghost"); err == nil {
		t.Error("user with an unknown role was stored")
	}

	user, err := rbac.GetUser("real")
	if err != nil {
		t.Fatalf("getting configured user: %v", err)
	}

	if user.RoleName() != "Experiment User" {
		t.Errorf("unexpected role: %q", user.RoleName())
	}
}
