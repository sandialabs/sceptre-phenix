package app_test

import (
	"slices"
	"testing"

	"phenix/app"
)

// BuiltinApps names, in order, the default apps and the apps that register themselves, but not the app that runs a
// phenix-app-<name> program.
func TestBuiltinApps(t *testing.T) {
	t.Parallel()

	got := app.BuiltinApps()

	for _, name := range []string{"ntp", "serial", "startup", "tap", "vrouter"} {
		if !slices.Contains(got, name) {
			t.Errorf("BuiltinApps() = %v, want it to hold %s", got, name)
		}
	}

	if slices.Contains(got, "user-shell") {
		t.Errorf("BuiltinApps() = %v, want no user-shell", got)
	}

	if !slices.IsSorted(got) {
		t.Errorf("BuiltinApps() = %v, want them sorted", got)
	}
}
