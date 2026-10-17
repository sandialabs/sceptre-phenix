// Package polltest waits in tests for a condition that other goroutines bring
// about. It is imported only by tests, as net/http/httptest is.
package polltest

import (
	"testing"
	"time"
)

const (
	// timeout is how long Until waits for its condition.
	timeout = 5 * time.Second

	// withinTimeout is how long Within waits for its work.
	withinTimeout = 2 * time.Second
)

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

// Within fails the test, with what naming the work, if fn does not return
// within withinTimeout.
func Within(tb testing.TB, what string, fn func()) {
	tb.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(withinTimeout):
		tb.Fatalf("%s took longer than %v", what, withinTimeout)
	}
}
