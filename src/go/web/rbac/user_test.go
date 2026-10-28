package rbac

import (
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"
	"pgregory.net/rapid"

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

// TestValidateUsernameKeepsOnePathSegment asserts each name ValidateUsername
// accepts is one URL path segment as it is, not a dot segment, and matches
// only itself as a resource name pattern.
func TestValidateUsernameKeepsOnePathSegment(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.OneOf(
			rapid.String(),
			rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9._@+-]{0,15}`),
		).Draw(t, "name")

		if ValidateUsername(name) != nil {
			return
		}

		if got := url.PathEscape(name); got != name {
			t.Errorf("url.PathEscape(%q) = %q, want it unchanged", name, got)
		}

		if name == "." || name == ".." || strings.HasPrefix(name, ".") {
			t.Errorf("ValidateUsername(%q) accepted a dot segment", name)
		}

		if strings.ContainsAny(name, `*?[]\`) {
			t.Errorf("ValidateUsername(%q) accepted a pattern character", name)
		}

		if matched, err := filepath.Match(name, name+"x"); matched || err != nil {
			t.Errorf("filepath.Match(%q, %q) = %t, %v, want false, nil", name, name+"x", matched, err)
		}
	})
}
