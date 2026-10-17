package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/plog/plogtest"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// usersTestRole is the role the tests give the users they create.
const usersTestRole = "Global Admin"

// usersTestStore is a BoltDB that refuses to create a user with createErr,
// when it is set, and finds no user while hideUsers is set.
type usersTestStore struct {
	store.Store

	createErr error
	hideUsers bool
}

func (s *usersTestStore) Create(c *store.Config) error {
	if s.createErr != nil && c.Kind == "User" {
		return s.createErr
	}

	return s.Store.Create(c)
}

func (s *usersTestStore) Get(c *store.Config) error {
	if s.hideUsers && c.Kind == "User" {
		return store.ErrNotExist
	}

	return s.Store.Get(c)
}

// useUsersTestStore makes the config store a new BoltDB holding the roles
// usersTestRole and Disabled, and alice (first name Alice, no role).
func useUsersTestStore(t *testing.T) *usersTestStore {
	t.Helper()

	db := storetest.NewBolt(t)

	policies := []any{map[string]any{"resources": []any{"*"}, "resourceNames": []any{"*"}, "verbs": []any{"*"}}}
	configs := map[string]map[string]any{
		"Role/global-admin": {"roleName": usersTestRole, "policies": policies},
		"Role/disabled":     {"roleName": "Disabled", "policies": policies},
		"User/alice":        {"username": "alice", "first_name": "Alice"},
	}

	for name, spec := range configs {
		kind, name, _ := strings.Cut(name, "/")

		c := &store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     kind,
			Metadata: store.ConfigMetadata{Name: name, Created: "", Updated: "", Annotations: nil},
			Spec:     spec,
			Status:   nil,
		}
		if err := db.Create(c); err != nil {
			t.Fatalf("creating %s %s returned error: %v", kind, name, err)
		}
	}

	s := &usersTestStore{Store: db, createErr: nil, hideUsers: false}
	storetest.Use(t, s)

	return s
}

// postUsers answers body, a request to create a user, as POST /users
// answers an administrator, or as POST /signup answers when signup is set.
func postUsers(signup bool, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()

	if signup {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/signup", strings.NewReader(body))
		Signup(rec, req)

		return rec
	}

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{resourceUsers}, ResourceNames: []string{"*"}, Verbs: []string{"create"}},
	}}}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "admin")

	CreateUser(rec, req.WithContext(ctx))

	return rec
}

// TestCreateUserRefusesAnExistingName refuses an existing name with 409, as
// an administrator and at signup, and other store errors with 500.
func TestCreateUserRefusesAnExistingName(t *testing.T) {
	for _, signup := range []bool{false, true} {
		name := "users"
		if signup {
			name = "signup"
		}

		t.Run(name, func(t *testing.T) {
			s := useUsersTestStore(t)

			body := `{"username":"alice","password":"Testpass1!","first_name":"Other","last_name":"Name",` +
				`"role_name":"` + usersTestRole + `"}`

			rec := postUsers(signup, body)
			if rec.Code != http.StatusConflict || rec.Body.String() != "user alice already exists\n" {
				t.Fatalf("create = %d %q, want 409 user alice already exists", rec.Code, rec.Body.String())
			}

			user, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatalf("GetUser returned error: %v", err)
			}

			if user.FirstName() != "Alice" {
				t.Fatalf("first name = %q, want Alice", user.FirstName())
			}

			s.createErr = errors.New("store is read-only")

			rec = postUsers(signup, strings.ReplaceAll(body, "alice", "bob"))
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != "error creating user\n" {
				t.Fatalf("create the store refuses = %d %q, want 500 error creating user", rec.Code, rec.Body.String())
			}
		})
	}
}

// A role that does not exist is refused before the user is created.
func TestCreateUserWithAnUnknownRoleCreatesNoUser(t *testing.T) {
	useUsersTestStore(t)

	rec := postUsers(false, `{"username":"bob","password":"Testpass1!","role_name":"No Such Role"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d %s, want 400", rec.Code, rec.Body.String())
	}

	if _, err := rbac.GetUser("bob"); err == nil {
		t.Fatal("the refused request created the user")
	}
}

// TestConfigureUsersWithoutCreatingAUser leaves a user that exists as it is,
// and names each user the store refuses to create in the error.
func TestConfigureUsersWithoutCreatingAUser(t *testing.T) {
	s := useUsersTestStore(t)
	logs := plogtest.Capture(t)

	s.hideUsers = true

	if err := ConfigureUsers([]string{"alice:Otherpass1!:" + usersTestRole}); err != nil {
		t.Fatalf("ConfigureUsers returned error: %v", err)
	}

	if len(logs.Take(t, plogtest.Message("default user already exists"))) != 1 {
		t.Fatal("ConfigureUsers did not log that alice exists")
	}

	s.hideUsers = false

	alice, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}

	if alice.FirstName() != "Alice" || alice.Spec.Role != nil {
		t.Fatal("ConfigureUsers changed the user that exists")
	}

	refused := errors.New("store is read-only")
	s.createErr = refused

	err = ConfigureUsers([]string{"bob:Testpass1!:" + usersTestRole, "carol:Testpass1!:" + usersTestRole})
	if !errors.Is(err, refused) || !strings.Contains(err.Error(), "bob") || !strings.Contains(err.Error(), "carol") {
		t.Fatalf("ConfigureUsers returned %v, want the store's error for bob and carol", err)
	}
}

// TestConfigureUsersSkipsAUserWithoutARole skips a default user that names
// no role, and neither logs nor returns its password.
func TestConfigureUsersSkipsAUserWithoutARole(t *testing.T) {
	useUsersTestStore(t)
	logs := plogtest.Capture(t)

	err := ConfigureUsers([]string{"dave:Davepass1!", "Nonamepass1!", "erin:Testpass1!:" + usersTestRole})
	if err == nil || !strings.Contains(err.Error(), `default user 1 ("dave")`) ||
		!strings.Contains(err.Error(), `default user 2 ("")`) {
		t.Fatalf("ConfigureUsers returned %v, want errors for default users 1 and 2", err)
	}

	logged := logs.String()

	for _, password := range []string{"Davepass1!", "Nonamepass1!"} {
		if strings.Contains(logged, password) || strings.Contains(err.Error(), password) {
			t.Fatalf("ConfigureUsers logged or returned the password %s", password)
		}
	}

	if skipped := logs.Take(t, plogtest.Message("skipping default user without a role")); len(skipped) != 2 ||
		skipped[0]["user"] != "dave" || skipped[1]["user"] != "" {
		t.Fatalf("skipped = %v, want dave and an unnamed user", skipped)
	}

	if _, err := rbac.GetUser("dave"); err == nil {
		t.Fatal("ConfigureUsers created dave, who has no role")
	}

	if _, err := rbac.GetUser("erin"); err != nil {
		t.Fatalf("ConfigureUsers did not create erin after the users without a role: %v", err)
	}
}
