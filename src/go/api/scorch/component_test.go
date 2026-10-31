package scorch

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// BuiltinComponents names, in order, the component types that SCORCH runs itself, but not the one that runs a
// phenix-scorch-component-<type> program.
func TestBuiltinComponents(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff([]string{"break", "pause", "soh", "tap"}, BuiltinComponents()); diff != "" {
		t.Errorf("BuiltinComponents() mismatch (-want +got):\n%s", diff)
	}
}
