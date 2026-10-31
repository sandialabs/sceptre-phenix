package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Each built-in role is found by its metadata name and by its role name, and
// no other kind of built-in config is listed.
func TestBuiltinRoles(t *testing.T) {
	t.Parallel()

	got, err := BuiltinRoles()
	if err != nil {
		t.Fatalf("BuiltinRoles() error = %v", err)
	}

	want := []string{
		"Disabled", "Experiment Admin", "Experiment User", "Experiment Viewer", "Global Admin", "Global Viewer",
		"VM Admin", "VM Viewer", "disabled", "experiment-admin", "experiment-user", "experiment-viewer",
		"global-admin", "global-viewer", "vm-admin", "vm-viewer",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BuiltinRoles() mismatch (-want +got):\n%s", diff)
	}
}
