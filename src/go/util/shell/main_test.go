package shell

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the run when a goroutine is still alive after the tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
