package web

import (
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"phenix/store"
	"phenix/web/rbac"
)

const testToken = "header.payload.signature"

// testPasswordHash is testPassword hashed once at bcrypt's lowest cost, which checking it keeps.
var testPasswordHash = sync.OnceValue(func() string { //nolint:gochecknoglobals // computed once for every test
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	return string(hash)
})

// createUserWithToken stores the user with a role, testPassword and testToken.
func createUserWithToken(t *testing.T, username string) {
	t.Helper()

	err := store.Create(&store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "User", Metadata: store.ConfigMetadata{Name: username},
		Spec: map[string]any{"username": username, "password": testPasswordHash()},
	})
	if err != nil {
		t.Fatalf("creating user %s: %v", username, err)
	}

	user, err := rbac.GetUser(username)
	if err != nil {
		t.Fatalf("rbac.GetUser(%s) = %v", username, err)
	}

	role := testRole("experiments", "get", "*")
	role.Spec.Name = "Viewer"

	if err := user.SetRole(&role); err != nil {
		t.Fatalf("setting %s's role: %v", username, err)
	}

	if err := user.AddToken(testToken, "test token"); err != nil {
		t.Fatalf("adding %s's token: %v", username, err)
	}
}
