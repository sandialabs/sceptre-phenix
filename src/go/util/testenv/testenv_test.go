package testenv_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/testenv"
)

// recorder is a [testing.TB] that records how a test would have ended.
type recorder struct {
	testing.TB

	ended string
}

func (r *recorder) Helper() {}

func (r *recorder) Skipf(format string, args ...any) {
	r.ended = "skip: " + fmt.Sprintf(format, args...)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.ended = "fail: " + fmt.Sprintf(format, args...)
}

// A missing command skips the test, or fails it when CI is set; commands that
// exist do neither.
func TestRequireCommands(t *testing.T) {
	const missing = "phenix-testenv-no-such-command"

	tests := map[string]struct {
		ci       string
		commands []string
		want     string
	}{
		"local, missing": {ci: "", commands: []string{missing}, want: "skip: " + missing + " is not installed"},
		"CI, missing": {
			ci: "true", commands: []string{missing},
			want: "fail: " + missing + " is not installed (CI is set, so the test may not skip)",
		},
		"CI, present": {ci: "true", commands: []string{"go"}, want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)

			r := &recorder{TB: t, ended: ""}
			testenv.RequireCommands(r, tc.commands...)

			if diff := cmp.Diff(tc.want, r.ended); diff != "" {
				t.Errorf("RequireCommands(%q) with CI=%q mismatch (-want +got):\n%s", tc.commands, tc.ci, diff)
			}
		})
	}
}
