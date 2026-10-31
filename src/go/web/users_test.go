package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/plog/plogtest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
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
		weberror.ErrorHandler(Signup).ServeHTTP(rec, req)

		return rec
	}

	req := roleRequest(http.MethodPost, "/api/v1/users", body, nil, testRole(resourceUsers, "create"))
	weberror.ErrorHandler(CreateUser).ServeHTTP(rec, req)

	return rec
}

// usersAnswer is the status and error message of an answer to a request to
// create a user.
type usersAnswer struct {
	Status  int
	Message string
}

func newUsersAnswer(rec *httptest.ResponseRecorder) usersAnswer {
	var body weberror.WebError

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	return usersAnswer{Status: rec.Code, Message: body.Message}
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

			want := usersAnswer{Status: http.StatusConflict, Message: "user alice already exists"}
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

			want = usersAnswer{Status: http.StatusInternalServerError, Message: "error creating user"}
			if diff := cmp.Diff(want, newUsersAnswer(postUsers(signup, strings.ReplaceAll(body, "alice", "bob")))); diff != "" {
				t.Errorf("POST /%s of bob to a read-only store mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// TestCreateUserRefusesAnInvalidName refuses, with 400, a name that a URL
// path does not keep as one segment, as an administrator and at signup.
func TestCreateUserRefusesAnInvalidName(t *testing.T) {
	for _, signup := range []bool{false, true} {
		endpoint := "users"
		if signup {
			endpoint = "signup"
		}

		// A "#" in a subtest name ends the BoltDB endpoint URL, so names omit usernames.
		for i, username := range []string{"alice#tag", "alice?x", "a/b", ".."} {
			t.Run(endpoint+strconv.Itoa(i), func(t *testing.T) {
				useUsersTestStore(t)

				body := `{"username":"` + username + `","password":"Testpass1!","role_name":"` + usersTestRole + `"}`

				got := newUsersAnswer(postUsers(signup, body))
				if got.Status != http.StatusBadRequest || !strings.HasPrefix(got.Message, "invalid username") {
					t.Errorf("POST /%s of %q = %+v, want 400 invalid username", endpoint, username, got)
				}

				user, err := rbac.GetUser("alice")
				if err != nil {
					t.Fatalf("GetUser(alice) returned error: %v", err)
				}

				if got := user.FirstName(); got != "Alice" {
					t.Errorf("GetUser(alice).FirstName() = %q, want Alice", got)
				}

				users, err := rbac.GetUsers()
				if err != nil {
					t.Fatalf("GetUsers() returned error: %v", err)
				}

				if len(users) != 1 {
					t.Errorf("GetUsers() returned %d users, want only alice", len(users))
				}
			})
		}
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

const (
	testPassword       = "Testpass1!"
	experimentUserRole = "Experiment User"
)

// useDefaultRoles gives the test a store of its own holding the default roles.
func useDefaultRoles(t *testing.T) {
	t.Helper()

	storetest.UseBolt(t)

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
		weberror.ErrorHandler(CreateUser).ServeHTTP(w, roleRequest(http.MethodPost, "/api/v1/users", tc.body, nil, usersCreate))

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
// malformed entry, naming it in the error.
//
//nolint:paralleltest // replaces the store
func TestConfigureUsersSkipsUnknownRole(t *testing.T) {
	useDefaultRoles(t)

	err := ConfigureUsers([]string{
		"ghost:" + testPassword + ":No Such Role",
		"short",
		"real:" + testPassword + ":" + experimentUserRole + ":exp-a",
	})
	if err == nil || !strings.Contains(err.Error(), "default user 2") || strings.Contains(err.Error(), "ghost") {
		t.Errorf("ConfigureUsers() error = %v, want one naming only default user 2", err)
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
	storetest.UseBolt(t)
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
		weberror.ErrorHandler(CreateUserToken).
			ServeHTTP(w, roleRequest(http.MethodPost, "/api/v1/users/"+tc.target+"/tokens", `{"lifetime":"1h"}`, vars, tc.role))

		if w.Code != tc.want {
			t.Errorf("CreateUserToken(%s) for %s = %d, want %d: %s", tc.name, tc.target, w.Code, tc.want, w.Body.String())
		}
	}
}
