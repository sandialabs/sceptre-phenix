package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// usersAnswer is the status and body of an answer to a request to create a user.
type usersAnswer struct {
	Status int
	Body   string
}

func newUsersAnswer(rec *httptest.ResponseRecorder) usersAnswer {
	return usersAnswer{Status: rec.Code, Body: rec.Body.String()}
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

			want := usersAnswer{Status: http.StatusConflict, Body: "user alice already exists\n"}
			if diff := cmp.Diff(want, newUsersAnswer(postUsers(signup, body))); diff != "" {
				t.Errorf("POST /%s of alice mismatch (-want +got):\n%s", name, diff)
			}

			user, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatalf("GetUser(alice) returned error: %v", err)
			}

			if got := user.FirstName(); got != "Alice" {
				t.Errorf("GetUser(alice).FirstName() = %q, want Alice", got)
			}

			s.createErr = errors.New("store is read-only")

			want = usersAnswer{Status: http.StatusInternalServerError, Body: "error creating user\n"}
			if diff := cmp.Diff(want, newUsersAnswer(postUsers(signup, strings.ReplaceAll(body, "alice", "bob")))); diff != "" {
				t.Errorf("POST /%s of bob to a read-only store mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// A role that does not exist is refused before the user is created.
func TestCreateUserWithAnUnknownRoleCreatesNoUser(t *testing.T) {
	useUsersTestStore(t)

	rec := postUsers(false, `{"username":"bob","password":"Testpass1!","role_name":"No Such Role"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /users of bob with role No Such Role = %d %s, want 400", rec.Code, rec.Body)
	}

	if _, err := rbac.GetUser("bob"); err == nil {
		t.Error("GetUser(bob) found the user the refused request named, want an error")
	}
}

// TestConfigureUsersWithoutCreatingAUser leaves a user that exists as it is,
// and names each user the store refuses to create in the error.
func TestConfigureUsersWithoutCreatingAUser(t *testing.T) {
	s := useUsersTestStore(t)
	logs := plogtest.Capture(t)

	s.hideUsers = true

	if err := ConfigureUsers([]string{"alice:Otherpass1!:" + usersTestRole}); err != nil {
		t.Errorf("ConfigureUsers(alice) returned error: %v", err)
	}

	if got := len(logs.Take(t, plogtest.Message("default user already exists"))); got != 1 {
		t.Errorf("ConfigureUsers(alice) logged that alice exists %d times, want 1", got)
	}

	s.hideUsers = false

	alice, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice) returned error: %v", err)
	}

	if diff := cmp.Diff(&v1.UserSpec{Username: "alice", FirstName: "Alice"}, alice.Spec); diff != "" {
		t.Errorf("ConfigureUsers(alice) changed the user that exists (-want +got):\n%s", diff)
	}

	refused := errors.New("store is read-only")
	s.createErr = refused

	err = ConfigureUsers([]string{"bob:Testpass1!:" + usersTestRole, "carol:Testpass1!:" + usersTestRole})
	if !errors.Is(err, refused) {
		t.Errorf("ConfigureUsers(bob, carol) = %v, want the store's error", err)
	}

	if err != nil {
		for _, name := range []string{"bob", "carol"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("ConfigureUsers(bob, carol) = %v, want it to name %s", err, name)
			}
		}
	}
}

// TestConfigureUsersSkipsAUserWithoutARole skips a default user that names
// no role, and neither logs nor returns its password.
func TestConfigureUsersSkipsAUserWithoutARole(t *testing.T) {
	useUsersTestStore(t)
	logs := plogtest.Capture(t)

	err := ConfigureUsers([]string{"dave:Davepass1!", "Nonamepass1!", "erin:Testpass1!:" + usersTestRole})
	if err == nil {
		t.Fatal("ConfigureUsers(dave, unnamed, erin) = nil, want errors for default users 1 and 2")
	}

	for _, want := range []string{`default user 1 ("dave")`, `default user 2 ("")`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ConfigureUsers(dave, unnamed, erin) = %v, want it to name %s", err, want)
		}
	}

	logged := logs.String()

	for _, password := range []string{"Davepass1!", "Nonamepass1!"} {
		if strings.Contains(logged, password) || strings.Contains(err.Error(), password) {
			t.Errorf("ConfigureUsers(dave, unnamed, erin) logged or returned the password %s", password)
		}
	}

	records := logs.Take(t, plogtest.Message("skipping default user without a role"))
	skipped := make([]any, 0, len(records))

	for _, record := range records {
		skipped = append(skipped, record["user"])
	}

	if diff := cmp.Diff([]any{"dave", ""}, skipped); diff != "" {
		t.Errorf("ConfigureUsers(dave, unnamed, erin) skipped users mismatch (-want +got):\n%s", diff)
	}

	if _, err := rbac.GetUser("dave"); err == nil {
		t.Error("GetUser(dave) found dave, who has no role, want an error")
	}

	if _, err := rbac.GetUser("erin"); err != nil {
		t.Errorf("GetUser(erin) returned error: %v, want erin created after the users without a role", err)
	}
}
