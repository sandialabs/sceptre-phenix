package scorch

import (
	"context"
	"errors"
	"strings"
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

// ExecuteComponent runs the stage of the component type it is given, a
// phenix-scorch-component-<type> program for an unknown type, and nothing for
// the done and loop stages; a failing stage names its stage and type.
func TestExecuteComponent(t *testing.T) { //nolint:paralleltest // replaces package state
	for _, stage := range []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup, ActionDone, ActionLoop} {
		t.Run(string(stage), func(t *testing.T) {
			c := appUseComponent(t, "x "+string(stage))

			err := ExecuteComponent(context.Background(), Type("test-app"), Name("x"), Stage(stage))

			var want []string
			if stage != ActionDone && stage != ActionLoop {
				want = []string{"x " + string(stage)}
			}

			if diff := cmp.Diff(want, c.calls); diff != "" {
				t.Errorf("ExecuteComponent(%s) stages mismatch (-want +got):\n%s", stage, diff)
			}

			wantErr := "running " + string(stage) + " stage for component test-app: x " + string(stage) + " failed"
			if want == nil {
				wantErr = ""
			}

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != wantErr {
				t.Errorf("ExecuteComponent(%s) error = %q, want %q", stage, gotErr, wantErr)
			}
		})
	}

	t.Run("an unknown type", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		err := ExecuteComponent(context.Background(), Type("nope"), Name("x"), Stage(ActionConfigure))
		if !errors.Is(err, ErrUserComponentNotFound) || !strings.HasPrefix(err.Error(), "running configure stage for component nope") {
			t.Errorf("ExecuteComponent(an unknown type) error = %v, want one that wraps %v", err, ErrUserComponentNotFound)
		}
	})
}
