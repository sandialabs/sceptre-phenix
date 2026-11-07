package mm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestValidateShellWord(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"t1", "abcdefghi-tapapp", "10.0.0.1/24", "fd00::1/64", "a_b.c"} {
		if err := ValidateShellWord(s); err != nil {
			t.Errorf("ValidateShellWord(%q) error = %v, want nil", s, err)
		}
	}

	for _, s := range []string{"", "a;b", "a b", "a|b", "a&b", "`a`", "a>b", "a(b)", "a*", "a'b", "a$b", "a!b"} {
		if err := ValidateShellWord(s); !errors.Is(err, ErrUnsafeArg) {
			t.Errorf("ValidateShellWord(%q) error = %v, want %v", s, err, ErrUnsafeArg)
		}
	}
}

func TestShellSteps(t *testing.T) {
	t.Parallel()

	steps := []ShellStep{{Action: "making a", Command: "mk a"}, {Action: "making b", Command: "mk b"}}

	for name, tc := range map[string]struct {
		steps   []ShellStep
		runErr  error
		wantCmd string // "" for no command run
		wantMsg string // a part of the error, "" for none
	}{
		"runs the steps as one script": {steps: steps, wantCmd: `sh -c "mk a || exit 100; mk b || exit 101"`},
		"names the failed step":        {steps: steps, runErr: errors.New("exit status 101"), wantCmd: "x", wantMsg: "making b: exit status 101"},
		"keeps an unknown status":      {steps: steps, runErr: errors.New("exit status 2"), wantCmd: "x", wantMsg: "exit status 2"},
		"keeps another error":          {steps: steps, runErr: errors.New("boom"), wantCmd: "x", wantMsg: "boom"},
		"runs nothing for no steps":    {},
		"refuses a quote":              {steps: []ShellStep{{Command: `echo "x"`}}, wantMsg: "shell script"},
		"refuses too many steps":       {steps: make([]ShellStep, maxShellSteps+1), wantMsg: "more than the limit"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var ran []string

			err := shellSteps(func(host, cmd string) error {
				ran = append(ran, host+" "+cmd)

				return tc.runErr
			}, "compute1", tc.steps)

			switch {
			case tc.wantMsg == "" && err != nil:
				t.Errorf("shellSteps() error = %v, want nil", err)
			case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
				t.Errorf("shellSteps() error = %v, want one holding %q", err, tc.wantMsg)
			}

			switch {
			case tc.wantCmd == "" && len(ran) > 0:
				t.Errorf("shellSteps() ran %q, want nothing", ran)
			case tc.wantCmd == "x" && len(ran) != 1:
				t.Errorf("shellSteps() ran %q, want one command", ran)
			case tc.wantCmd != "" && tc.wantCmd != "x" && !cmp.Equal(ran, []string{"compute1 " + tc.wantCmd}):
				t.Errorf("shellSteps() ran %q, want %q", ran, "compute1 "+tc.wantCmd)
			}
		})
	}
}

func TestShellEach(t *testing.T) {
	t.Parallel()

	steps := []ShellStep{{Action: "removing a", Command: "rm a"}, {Action: "removing b", Command: "rm b"}}

	for name, tc := range map[string]struct {
		out     string
		runErr  error
		wantMsg []string // parts of the error, none for no error
	}{
		"reports nothing when every step passes": {},
		"names each failed step":                 {out: "0\n1\n", wantMsg: []string{"removing a", "removing b", `"rm b"`}},
		"reports output that is not a step":      {out: "7 x", wantMsg: []string{`unexpected shell output "7"`, `"x"`}},
		"reports a failed script":                {runErr: errors.New("boom"), wantMsg: []string{"running 2 shell commands on host compute1: boom"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var ran string

			err := shellEach(t.Context(), func(_ context.Context, host, cmd string) (string, error) {
				ran = host + " " + cmd

				return tc.out, tc.runErr
			}, "compute1", steps)

			if want := `compute1 sh -c "rm a 2>/dev/null || echo 0; rm b 2>/dev/null || echo 1"`; ran != want {
				t.Errorf("shellEach() ran %q, want %q", ran, want)
			}

			if len(tc.wantMsg) == 0 && err != nil {
				t.Errorf("shellEach() error = %v, want nil", err)
			}

			for _, part := range tc.wantMsg {
				if err == nil || !strings.Contains(err.Error(), part) {
					t.Errorf("shellEach() error = %v, want one holding %q", err, part)
				}
			}
		})
	}
}
