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
		t.Fatalf("CreateUser(dave, first) error = %v", err)
	}

	if _, err := CreateUser("dave", "second"); !errors.Is(err, store.ErrExist) {
		t.Errorf("CreateUser(dave, second) error = %v, want %v", err, store.ErrExist)
	}

	user, err := GetUser("dave")
	if err != nil {
		t.Fatalf("GetUser(dave) error = %v", err)
	}

	if err := user.ValidatePassword("first"); err != nil {
		t.Errorf("ValidatePassword(first) of the stored user error = %v, want the first password kept", err)
	}
}
