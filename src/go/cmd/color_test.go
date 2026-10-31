package cmd

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// colorFlagInArgs finds the last --color value before "--", in both forms,
// and nothing when the flag has no value.
func TestColorFlagInArgs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args      []string
		want      string
		wantFound bool
	}{
		"none":            {args: []string{"exp", "list"}},
		"equals":          {args: []string{"exp", "list", "--color=off"}, want: "off", wantFound: true},
		"separate":        {args: []string{"--color", "on", "exp", "list"}, want: "on", wantFound: true},
		"last wins":       {args: []string{"--color=off", "doctor", "--color", "on"}, want: "on", wantFound: true},
		"after --":        {args: []string{"mm", "--", "--color=off"}},
		"no value":        {args: []string{"doctor", "--color"}},
		"other flag":      {args: []string{"--colors=off", "--colorx", "off"}},
		"empty value":     {args: []string{"--color="}, want: "", wantFound: true},
		"before and past": {args: []string{"--color=on", "--", "--color=off"}, want: "on", wantFound: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, found := colorFlagInArgs(tc.args)
			if got != tc.want || found != tc.wantFound {
				t.Errorf("colorFlagInArgs(%q) = %q, %v, want %q, %v", tc.args, got, found, tc.want, tc.wantFound)
			}
		})
	}
}

// A --color=<mode> argument is found whatever comes before it, unless a "--"
// comes first.
func TestColorFlagInArgsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		before := rapid.SliceOf(rapid.StringMatching(`[a-z-]{0,8}`)).Draw(t, "before")
		mode := rapid.SampledFrom([]string{"auto", "on", "off"}).Draw(t, "mode")
		args := append(slices.Clone(before), "--color="+mode)

		cut := slices.Index(before, "--")
		hasValueFlag := slices.Contains(before, "--color")

		got, found := colorFlagInArgs(args)

		switch {
		case cut >= 0:
			if found && slices.Index(before, "--color") > cut {
				t.Fatalf("colorFlagInArgs(%q) found %q past --", args, got)
			}
		case !hasValueFlag && (got != mode || !found):
			t.Fatalf("colorFlagInArgs(%q) = %q, %v, want %q", args, got, found, mode)
		}
	})
}

// The command error shows "Error:" in red only on a colored output; a buffer
// is no terminal, so auto leaves it plain. A --color in args wins over the
// setting, because cobra does not parse the flags of an unknown command.
func TestPrintCommandError(t *testing.T) { //nolint:paralleltest // sets PHENIX_COLOR
	tests := map[string]struct {
		color string
		args  []string
		want  string
	}{
		"auto, not a terminal": {color: "", want: "Error: bad flag\n"},
		"off":                  {color: "off", want: "Error: bad flag\n"},
		"on":                   {color: "on", want: "\x1b[31mError:\x1b[0m bad flag\n"},
		"refused mode as auto": {color: "sometimes", want: "Error: bad flag\n"},
		"flag on, setting off": {color: "off", args: []string{"--color", "on", "nosuchcmd"}, want: "\x1b[31mError:\x1b[0m bad flag\n"},
		"flag off, setting on": {color: "on", args: []string{"--color=off", "nosuchcmd"}, want: "Error: bad flag\n"},
		"refused flag":         {color: "on", args: []string{"--color=yes"}, want: "\x1b[31mError:\x1b[0m bad flag\n"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PHENIX_COLOR", tc.color)

			var out bytes.Buffer

			printCommandError(&out, tc.args, errors.New("bad flag"))

			if got := out.String(); got != tc.want {
				t.Errorf("printCommandError() wrote %q, want %q", got, tc.want)
			}
		})
	}
}

// colorMode refuses a value other than the three modes, naming the options.
func TestColorModeRefusesUnknown(t *testing.T) { //nolint:paralleltest // sets PHENIX_COLOR
	t.Setenv("PHENIX_COLOR", "yes")

	_, err := colorMode()
	if err == nil || !strings.Contains(err.Error(), colorOptions) {
		t.Errorf("colorMode() error = %v, want one that lists %q", err, colorOptions)
	}
}

// A config file warning has "warning:" in yellow only on a colored output.
func TestConfigWarningf(t *testing.T) { //nolint:paralleltest // sets PHENIX_COLOR
	for color, want := range map[string]string{
		"off": "phenix: warning: ignoring config file a.yaml\n",
		"on":  "phenix: \x1b[33mwarning:\x1b[0m ignoring config file a.yaml\n",
	} {
		t.Run(color, func(t *testing.T) {
			t.Setenv("PHENIX_COLOR", color)

			var out bytes.Buffer

			configWarningf(&out, "ignoring config file %s", "a.yaml")

			if got := out.String(); got != want {
				t.Errorf("configWarningf() wrote %q, want %q", got, want)
			}
		})
	}
}
