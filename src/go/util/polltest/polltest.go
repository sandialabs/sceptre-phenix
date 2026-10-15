// Package polltest waits in tests for a condition that other goroutines bring
// about. It is imported only by tests, as net/http/httptest is.
package polltest

import (
	"testing"
	"time"
)

// timeout is how long Until waits for its condition.
const timeout = 5 * time.Second

// Until polls cond until it holds, failing the test, with what naming the
// condition, if it does not hold within a few seconds.
func Until(tb testing.TB, what string, cond func() bool) {
	tb.Helper()

	deadline := time.Now().Add(timeout)

	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out waiting until %s", what)
		}

		time.Sleep(time.Millisecond)
	}
}
