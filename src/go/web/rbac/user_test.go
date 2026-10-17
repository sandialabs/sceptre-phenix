package rbac

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"

	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

// TestParallelSignInsKeepEveryToken asserts parallel sign-ins, during a sign-out
// and a role change, all leave valid tokens.
func TestParallelSignInsKeepEveryToken(t *testing.T) {
	storetest.UseBolt(t)

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
