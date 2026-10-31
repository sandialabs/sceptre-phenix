// Package testenv skips a test locally when its environment, such as a command on PATH, is
// missing, but fails it in CI so coverage is never silently dropped. Only tests import it.
package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// InCI reports whether the tests run in CI, which sets CI to a non-empty value.
func InCI() bool {
	return os.Getenv("CI") != ""
}

// Skipf skips tb with the reason, or fails it in CI.
func Skipf(tb testing.TB, format string, args ...any) {
	tb.Helper()

	if InCI() {
		tb.Fatalf("%s (CI is set, so the test may not skip)", fmt.Sprintf(format, args...))

		return
	}

	tb.Skipf(format, args...)
}

// RequireCommands skips tb, or fails it in CI, when any named command is not
// on PATH.
func RequireCommands(tb testing.TB, names ...string) {
	tb.Helper()

	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			Skipf(tb, "%s is not installed", name)
		}
	}
}
