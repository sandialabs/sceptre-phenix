package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// usersTestRole is the role the tests give the users they create.
const usersTestRole = "Global Admin"

// usersTestStore is a BoltDB that refuses to create a user with createErr,
// when it is set, and finds no user while hideUsers is set, as a store
// does whose user another phenix creates after it is looked up.
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

// useUsersTestStore points the config store at a new BoltDB holding the
// role usersTestRole, the disabled role a user who signs up has, and the
// user alice, whose first name is Alice and who has no role.
func useUsersTestStore(t *testing.T) *usersTestStore {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

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

	previous := store.DefaultStore
	store.DefaultStore = s //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

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

// TestCreateUserRefusesAnExistingName creates a user whose name another user
// has, by an administrator and by signing up, and asserts it is refused with
// 409 and the user is as it was, and that the store's other errors are 500
// with a plain message.
func TestCreateUserRefusesAnExistingName(t *testing.T) { //nolint:paralleltest // replaces the config store
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
func TestCreateUserWithAnUnknownRoleCreatesNoUser(t *testing.T) { //nolint:paralleltest // replaces the config store
	useUsersTestStore(t)

	rec := postUsers(false, `{"username":"bob","password":"Testpass1!","role_name":"No Such Role"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d %s, want 400", rec.Code, rec.Body.String())
	}

	if _, err := rbac.GetUser("bob"); err == nil {
		t.Fatal("the refused request created the user")
	}
}

// TestConfigureUsersWithoutCreatingAUser configures default users the store
// will not create, one because it holds a user of that name the lookup did
// not find, and asserts that user is left as it is and the error names each
// of the others.
func TestConfigureUsersWithoutCreatingAUser(t *testing.T) { //nolint:paralleltest // replaces the config store and logs
	s := useUsersTestStore(t)
	logs := captureBuilderShareLogs(t)

	s.hideUsers = true

	if err := ConfigureUsers([]string{"alice:Otherpass1!:" + usersTestRole}); err != nil {
		t.Fatalf("ConfigureUsers returned error: %v", err)
	}

	if len(logs.records(t, "default user already exists")) != 1 {
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
