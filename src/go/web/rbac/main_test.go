package rbac

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain hashes the tests' passwords at bcrypt's lowest cost, which keeps
// user creation fast under -race.
func TestMain(m *testing.M) {
	SetPasswordCostForTesting(bcrypt.MinCost)
	os.Exit(m.Run())
}
