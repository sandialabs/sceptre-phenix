package rbac

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain hashes the tests' passwords at bcrypt's lowest cost: the default
// cost makes every user a test creates take a noticeable fraction of a second,
// most of all under -race.
func TestMain(m *testing.M) {
	SetPasswordCostForTesting(bcrypt.MinCost)
	os.Exit(m.Run())
}
