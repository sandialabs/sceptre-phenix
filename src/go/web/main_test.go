package web

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"phenix/web/rbac"
)

// TestMain hashes the tests' passwords at bcrypt's lowest cost, which keeps
// user creation fast under -race.
func TestMain(m *testing.M) {
	rbac.SetPasswordCostForTesting(bcrypt.MinCost)
	os.Exit(m.Run())
}
