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
//
//nolint:paralleltest // sets CI
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
		"CI, present":    {ci: "true", commands: []string{"go"}, want: ""},
		"local, present": {ci: "", commands: []string{"go"}, want: ""},
		"CI, none named": {ci: "true", commands: nil, want: ""},
		"CI, second of two missing": {
			ci: "1", commands: []string{"go", missing},
			want: "fail: " + missing + " is not installed (CI is set, so the test may not skip)",
		},
		"local, first of two missing": {
			ci: "", commands: []string{missing + "-1", missing + "-2"},
			want: "skip: " + missing + "-1 is not installed",
		},
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

// InCI is true when CI holds any value, even "false", and false when it is empty.
//
//nolint:paralleltest // sets CI
func TestInCI(t *testing.T) {
	for ci, want := range map[string]bool{"": false, "true": true, "1": true, "false": true} {
		t.Run(fmt.Sprintf("CI=%q", ci), func(t *testing.T) {
			t.Setenv("CI", ci)

			if got := testenv.InCI(); got != want {
				t.Errorf("InCI() with CI=%q = %t, want %t", ci, got, want)
			}
		})
	}
}

// Skipf formats its reason, and skips the test locally or fails it in CI.
//
//nolint:paralleltest // sets CI
func TestSkipf(t *testing.T) {
	tests := map[string]struct {
		ci     string
		format string
		args   []any
		want   string
	}{
		"local":         {ci: "", format: "no %s on %d", args: []any{"qemu", 2}, want: "skip: no qemu on 2"},
		"CI":            {ci: "true", format: "no %s", args: []any{"qemu"}, want: "fail: no qemu (CI is set, so the test may not skip)"},
		"local, no arg": {ci: "", format: "100%% done", want: "skip: 100% done"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)

			r := &recorder{TB: t, ended: ""}
			testenv.Skipf(r, tc.format, tc.args...)

			if diff := cmp.Diff(tc.want, r.ended); diff != "" {
				t.Errorf("Skipf(%q, %v) with CI=%q mismatch (-want +got):\n%s", tc.format, tc.args, tc.ci, diff)
			}
		})
	}
}
