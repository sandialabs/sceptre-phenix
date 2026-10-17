package rbac

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"phenix/store"
	"phenix/store/storetest"
)

// Creating a user that exists reports store.ErrExist and keeps the stored one.
//
//nolint:paralleltest // replaces the store
func TestCreateUserExisting(t *testing.T) {
	storetest.Use(t)

	original := passwordCost
	passwordCost = bcrypt.MinCost

	t.Cleanup(func() { passwordCost = original })

	if _, err := CreateUser("dave", "first"); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateUser("dave", "second"); !errors.Is(err, store.ErrExist) {
		t.Fatalf("second create: %v, want %v", err, store.ErrExist)
	}

	user, err := GetUser("dave")
	if err != nil {
		t.Fatal(err)
	}

	if err := user.ValidatePassword("first"); err != nil {
		t.Errorf("stored password changed: %v", err)
	}
}
