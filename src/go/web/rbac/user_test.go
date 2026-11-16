package rbac

import (
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

// lowerPasswordCost hashes the test's passwords at the lowest bcrypt cost.
func lowerPasswordCost(t *testing.T) {
	t.Helper()

	original := passwordCost
	passwordCost = bcrypt.MinCost

	t.Cleanup(func() { passwordCost = original })
}

// TestParallelSignInsKeepEveryToken asserts parallel sign-ins, during a sign-out
// and a role change, all leave valid tokens.
func TestParallelSignInsKeepEveryToken(t *testing.T) {
	storetest.UseBolt(t)
	lowerPasswordCost(t)

	if _, err := NewUser("alice", "Testpass1!", "Alice", "Tester"); err != nil {
		t.Fatalf("NewUser(alice) returned error: %v", err)
	}

	earlier, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice) returned error: %v", err)
	}

	if err := earlier.AddToken("earlier", "signed in earlier"); err != nil {
		t.Fatalf("AddToken(earlier) returned error: %v", err)
	}

	const signIns = 16

	// Every request reads the user before any of them saves, as requests
	// that arrive together do, then changes it.
	var (
		read  sync.WaitGroup
		group errgroup.Group
	)

	read.Add(signIns + 2)

	request := func(change func(u *User) error) {
		group.Go(func() error {
			u, err := GetUser("alice")
			read.Done()

			if err != nil {
				return err
			}

			read.Wait()

			return change(u)
		})
	}

	for i := range signIns {
		request(func(u *User) error { return u.AddToken("token-"+strconv.Itoa(i), "signed in") })
	}

	request(func(u *User) error { return u.DeleteToken("earlier") })
	request(func(u *User) error { return u.SetRole(&Role{Spec: &v1.RoleSpec{Name: "Experiment User"}}) })

	if err := group.Wait(); err != nil {
		t.Fatalf("a parallel request returned error: %v", err)
	}

	stored, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice) returned error: %v", err)
	}

	for i := range signIns {
		if err := stored.ValidateToken("token-" + strconv.Itoa(i)); err != nil {
			t.Errorf("ValidateToken(token-%d) = %v, want nil", i, err)
		}
	}

	if stored.ValidateToken("earlier") == nil {
		t.Error("ValidateToken(earlier) after DeleteToken(earlier) = nil, want an error")
	}

	if got := stored.RoleName(); got != "Experiment User" {
		t.Errorf("RoleName() = %q, want Experiment User", got)
	}

	if got, want := []string{stored.FirstName(), stored.LastName()}, []string{"Alice", "Tester"}; !slices.Equal(got, want) {
		t.Errorf("FirstName(), LastName() = %q, want %q", got, want)
	}
}

// TestNewUserRefusesAnExistingName creates a user whose name another user
// has, and asserts the error is ErrUserExists and the user is as it was.
func TestNewUserRefusesAnExistingName(t *testing.T) {
	storetest.UseBolt(t)
	lowerPasswordCost(t)

	if _, err := NewUser("alice", "Testpass1!", "Alice", "Tester"); err != nil {
		t.Fatalf("NewUser(alice) returned error: %v", err)
	}

	if u, err := NewUser("alice", "Otherpass1!", "Other", "Name"); u != nil || !errors.Is(err, ErrUserExists) {
		t.Errorf("NewUser(alice) of an existing name = %v, %v, want nil, %v", u, err, ErrUserExists)
	}

	stored, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice) returned error: %v", err)
	}

	if got := stored.FirstName(); got != "Alice" {
		t.Errorf("FirstName() = %q, want Alice", got)
	}

	if err := stored.ValidatePassword("Testpass1!"); err != nil {
		t.Errorf("ValidatePassword(Testpass1!) = %v, want nil", err)
	}
}

// TestNewUserRefusesAnInvalidName asserts NewUser refuses each name that is
// not one plain URL path segment or that is a pattern, and stores no user.
func TestNewUserRefusesAnInvalidName(t *testing.T) {
	storetest.UseBolt(t)
	lowerPasswordCost(t)

	for _, name := range []string{"", "alice#tag", "alice?x", "a/b", "a%2Fb", ".", "..", ".alice", "-alice", "a*", "a[b]", `a\b`, "a b", "élan"} {
		if u, err := NewUser(name, "Testpass1!", "", ""); u != nil || !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("NewUser(%q) = %v, %v, want nil, %v", name, u, err, ErrInvalidUsername)
		}
	}

	users, err := GetUsers()
	if err != nil {
		t.Fatalf("GetUsers() returned error: %v", err)
	}

	if len(users) != 0 {
		t.Errorf("GetUsers() returned %d users after only refused names, want 0", len(users))
	}

	for _, name := range []string{"alice", "user@example.org", "j.doe+lab_1-x", "0"} {
		if _, err := NewUser(name, "Testpass1!", "", ""); err != nil {
			t.Errorf("NewUser(%q) returned error: %v", name, err)
		}
	}
}

// TestValidateUsernameKeepsOnePathSegment checks with usernameCheck that
// ValidateUsername follows its rule, and that each name it accepts is one URL
// path segment as it is, not a dot segment, and matches only itself.
func TestValidateUsernameKeepsOnePathSegment(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.OneOf(
			rapid.String(),
			rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9._@+-]{0,15}`),
		).Draw(t, "name")

		if err := usernameCheck(name); err != nil {
			t.Fatal(err)
		}
	})
}

// errUserStore is the error userFailingStore returns.
var errUserStore = errors.New("store unavailable")

// userFailingStore is a store that fails List for the kinds in list, and
// every Create and Update when create and update are set.
type userFailingStore struct {
	store.Store

	list           map[string]bool
	create, update bool
}

func (s userFailingStore) List(kinds ...string) (store.Configs, error) {
	if slices.ContainsFunc(kinds, func(k string) bool { return s.list[k] }) {
		return nil, errUserStore
	}

	return s.Store.List(kinds...) //nolint:wrapcheck // the store's own error
}

func (s userFailingStore) Create(c *store.Config) error {
	if s.create {
		return errUserStore
	}

	return s.Store.Create(c) //nolint:wrapcheck // the store's own error
}

func (s userFailingStore) Update(c *store.Config) error {
	if s.update {
		return errUserStore
	}

	return s.Store.Update(c) //nolint:wrapcheck // the store's own error
}

// userStoreRaw stores a config of kind and name with spec as it is, such as a
// spec that does not decode.
func userStoreRaw(t *testing.T, name string, spec map[string]any) {
	t.Helper()

	c, err := store.NewConfig(name)
	if err != nil {
		t.Fatalf("NewConfig(%s) error = %v", name, err)
	}

	c.Spec = spec

	if err := store.Create(c); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
}

// Updating a user's names, password, tokens and role changes the user and its
// stored record; a wrong old password changes nothing.
//
//nolint:paralleltest // replaces the store
func TestUserUpdates(t *testing.T) {
	storetest.UseBolt(t)
	lowerPasswordCost(t)

	u, err := NewUser("bob", "Oldpass1!", "Bob", "Builder")
	if err != nil {
		t.Fatalf("NewUser(bob) error = %v", err)
	}

	steps := []struct {
		name   string
		change func() error
	}{
		{"UpdateFirstName", func() error { return u.UpdateFirstName("Robert") }},
		{"UpdateLastName", func() error { return u.UpdateLastName("Smith") }},
		{"UpdatePassword", func() error { return u.UpdatePassword("Oldpass1!", "Newpass1!") }},
		{"AddToken signed in", func() error { return u.AddToken("plain", "signed in") }},
		{"AddToken proxied", func() error { return u.AddToken("proxy-1", "proxied") }},
		{"AddToken proxied again", func() error { return u.AddToken("proxy-2", "proxied") }},
		{"SetRole", func() error { return u.SetRole(&Role{Spec: &v1.RoleSpec{Name: "VM Viewer"}}) }},
	}

	for _, step := range steps {
		if err := step.change(); err != nil {
			t.Fatalf("%s error = %v", step.name, err)
		}
	}

	if err := u.UpdatePassword("Wrongpass1!", "Otherpass1!"); !errors.Is(err, ErrPasswordInvalid) {
		t.Errorf("UpdatePassword(wrong old) error = %v, want %v", err, ErrPasswordInvalid)
	}

	stored, err := GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser(bob) error = %v", err)
	}

	for _, got := range []*User{u, stored} {
		want := []string{"Robert", "Smith", "VM Viewer", base64.StdEncoding.EncodeToString([]byte("proxy-2"))}
		if diff := cmp.Diff(want, []string{got.FirstName(), got.LastName(), got.RoleName(), got.GetProxyToken()}); diff != "" {
			t.Errorf("names, role and proxy token mismatch (-want +got):\n%s", diff)
		}

		wantTokens := map[string]string{
			base64.StdEncoding.EncodeToString([]byte("plain")):   "signed in",
			base64.StdEncoding.EncodeToString([]byte("proxy-2")): "proxied",
		}
		if diff := cmp.Diff(wantTokens, got.Spec.Tokens); diff != "" {
			t.Errorf("tokens after a second proxied token mismatch (-want +got):\n%s", diff)
		}

		if err := got.ValidatePassword("Newpass1!"); err != nil {
			t.Errorf("ValidatePassword(new) error = %v, want nil", err)
		}

		if err := got.ValidatePassword("Oldpass1!"); !errors.Is(err, ErrPasswordInvalid) {
			t.Errorf("ValidatePassword(old) error = %v, want %v", err, ErrPasswordInvalid)
		}
	}

	role, err := stored.Role()
	if err != nil || role.Spec.Name != "VM Viewer" {
		t.Errorf("Role() = %v, %v, want VM Viewer, nil", role.Spec, err)
	}
}

// A user without a role has the stored disabled role, or no role name and an
// error when the store has none.
//
//nolint:paralleltest // replaces the store
func TestUserWithoutARole(t *testing.T) {
	for name, tc := range map[string]struct {
		disabled bool
		wantName string
	}{
		"with the disabled role stored": {disabled: true, wantName: "Disabled"},
		"without it":                    {disabled: false, wantName: ""},
	} {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)

			if tc.disabled {
				userStoreRaw(t, "role/disabled", map[string]any{"roleName": "Disabled", "policies": []any{}})
			}

			u := User{Spec: &v1.UserSpec{Username: "carol"}}

			if got := u.RoleName(); got != tc.wantName {
				t.Errorf("RoleName() = %q, want %q", got, tc.wantName)
			}

			role, err := u.Role()
			if (err != nil) == tc.disabled {
				t.Fatalf("Role() error = %v, want an error %t", err, !tc.disabled)
			}

			if tc.disabled && (role.Spec.Name != "Disabled" || role.Allowed("vms", "list")) {
				t.Errorf("Role() = %+v, want the Disabled role that allows nothing", role.Spec)
			}
		})
	}
}

// A user with no proxied token has no proxy token, and a password hash that
// bcrypt cannot read is an error but not a wrong password.
func TestUserWithoutStore(t *testing.T) {
	t.Parallel()

	u := User{Spec: &v1.UserSpec{Password: "not a hash", Tokens: map[string]string{"dG9r": "signed in"}}}

	if got := u.GetProxyToken(); got != "" {
		t.Errorf("GetProxyToken() = %q, want none", got)
	}

	if err := u.ValidateToken("tok"); err != nil {
		t.Errorf("ValidateToken(tok) error = %v, want nil", err)
	}

	if err := u.ValidateToken("other"); err == nil {
		t.Error("ValidateToken(other) error = nil, want an error")
	}

	err := u.ValidatePassword("pass")
	if err == nil || errors.Is(err, ErrPasswordInvalid) || !errors.Is(err, bcrypt.ErrHashTooShort) {
		t.Errorf("ValidatePassword() of a bad hash error = %v, want %v", err, bcrypt.ErrHashTooShort)
	}

	if err := u.UpdatePassword("pass", "new"); !errors.Is(err, bcrypt.ErrHashTooShort) {
		t.Errorf("UpdatePassword() of a bad hash error = %v, want %v", err, bcrypt.ErrHashTooShort)
	}
}

// Each change of a user that the store fails to read or save returns an
// error naming the change, and NewUser refuses a password bcrypt cannot hash.
//
//nolint:paralleltest // replaces the store
func TestUserStoreErrors(t *testing.T) {
	lowerPasswordCost(t)

	long := strings.Repeat("p", 73)

	for name, tc := range map[string]struct {
		store   userFailingStore
		deleted bool // the user is removed from the store before the call
		call    func(u *User) error
		want    string
	}{
		"UpdateFirstName of a deleted user": {
			deleted: true, call: func(u *User) error { return u.UpdateFirstName("x") },
			want: "updating user first name: getting user config",
		},
		"UpdateLastName that the store fails to save": {
			store: userFailingStore{update: true}, call: func(u *User) error { return u.UpdateLastName("x") },
			want: "updating user last name: updating user in store: store unavailable",
		},
		"UpdatePassword that the store fails to save": {
			store: userFailingStore{update: true}, call: func(u *User) error { return u.UpdatePassword("Testpass1!", "x") },
			want: "updating user password: updating user in store: store unavailable",
		},
		"UpdatePassword to a password too long to hash": {
			call: func(u *User) error { return u.UpdatePassword("Testpass1!", long) },
			want: "generating password hash",
		},
		"AddToken that the store fails to save": {
			store: userFailingStore{update: true}, call: func(u *User) error { return u.AddToken("t", "signed in") },
			want: "persisting new user token: updating user in store: store unavailable",
		},
		"DeleteToken that the store fails to save": {
			store: userFailingStore{update: true}, call: func(u *User) error { return u.DeleteToken("t") },
			want: "deleting user token: updating user in store: store unavailable",
		},
		"SetRole of a deleted user": {
			deleted: true, call: func(u *User) error { return u.SetRole(&Role{Spec: &v1.RoleSpec{Name: "VM Viewer"}}) },
			want: "setting user role: getting user config",
		},
		"Save that the store fails": {
			store: userFailingStore{update: true}, call: func(u *User) error { return u.Save() },
			want: "updating user in store: store unavailable",
		},
		"NewUser that the store fails to create": {
			store: userFailingStore{create: true},
			call:  func(*User) error { _, err := NewUser("dave", "Testpass1!", "", ""); return err },
			want:  "creating user dave: store unavailable",
		},
		"NewUser of a password too long to hash": {
			call: func(*User) error { _, err := NewUser("erin", long, "", ""); return err },
			want: "hashing password",
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := storetest.NewBolt(t)
			storetest.Use(t, db)

			u, err := NewUser("alice", "Testpass1!", "Alice", "Tester")
			if err != nil {
				t.Fatalf("NewUser(alice) error = %v", err)
			}

			if tc.deleted {
				if err := store.Delete(u.config); err != nil {
					t.Fatalf("deleting alice: %v", err)
				}
			}

			tc.store.Store = db
			storetest.Use(t, tc.store)

			err = tc.call(u)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one starting %q", err, tc.want)
			}

			if tc.store.update || tc.store.create {
				if !errors.Is(err, errUserStore) {
					t.Errorf("error = %v, want one that wraps %v", err, errUserStore)
				}
			}
		})
	}
}

// GetUsers and GetUser fail when the store fails to list users or a stored
// user does not decode.
//
//nolint:paralleltest // replaces the store
func TestGetUsersErrors(t *testing.T) {
	t.Run("a failing store", func(t *testing.T) {
		storetest.Use(t, userFailingStore{Store: storetest.NewBolt(t), list: map[string]bool{"User": true}})

		if users, err := GetUsers(); users != nil || !errors.Is(err, errUserStore) {
			t.Errorf("GetUsers() = %v, %v, want nil, %v", users, err, errUserStore)
		}
	})

	t.Run("a user that does not decode", func(t *testing.T) {
		storetest.UseBolt(t)
		userStoreRaw(t, "user/frank", map[string]any{"username": "frank", "password": []any{1, 2}})

		if users, err := GetUsers(); users != nil || err == nil || !strings.HasPrefix(err.Error(), "decoding user config") {
			t.Errorf("GetUsers() = %v, %v, want nil and a decoding error", users, err)
		}

		if u, err := GetUser("frank"); u != nil || err == nil || !strings.HasPrefix(err.Error(), "decoding user config") {
			t.Errorf("GetUser(frank) = %v, %v, want nil and a decoding error", u, err)
		}
	})

	t.Run("a missing user", func(t *testing.T) {
		storetest.UseBolt(t)

		if u, err := GetUser("nobody"); u != nil || !errors.Is(err, store.ErrNotExist) {
			t.Errorf("GetUser(nobody) = %v, %v, want nil, %v", u, err, store.ErrNotExist)
		}
	})
}
