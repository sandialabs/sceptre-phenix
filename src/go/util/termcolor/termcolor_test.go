package termcolor_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/creack/pty"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/termcolor"
	"phenix/util/testenv"
)

// ParseMode takes the three modes and "" as auto, and refuses any other value.
func TestParseMode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      string
		want    termcolor.Mode
		wantErr bool
	}{
		"empty": {in: "", want: termcolor.Auto},
		"auto":  {in: "auto", want: termcolor.Auto},
		"on":    {in: "on", want: termcolor.On},
		"off":   {in: "off", want: termcolor.Off},
		"upper": {in: "ON", want: termcolor.Auto, wantErr: true},
		"other": {in: "yes", want: termcolor.Auto, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := termcolor.ParseMode(tc.in)
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, termcolor.ErrUnknownMode)) {
				t.Fatalf("ParseMode(%q) error = %v, want error %v", tc.in, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// on and off ignore the writer and the variables; auto colors no
// writer that is not a terminal, such as a buffer or a regular file.
func TestEnabled(t *testing.T) {
	t.Parallel()

	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = file.Close() })

	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}

	tests := map[string]struct {
		mode termcolor.Mode
		w    io.Writer
		vars map[string]string
		want bool
	}{
		"on, buffer":               {mode: termcolor.On, w: &bytes.Buffer{}, want: true},
		"on, NO_COLOR":             {mode: termcolor.On, w: &bytes.Buffer{}, vars: map[string]string{"NO_COLOR": "1"}, want: true},
		"off, buffer":              {mode: termcolor.Off, w: &bytes.Buffer{}, want: false},
		"auto, buffer":             {mode: termcolor.Auto, w: &bytes.Buffer{}, want: false},
		"auto, regular file":       {mode: termcolor.Auto, w: file, want: false},
		"auto, file and NO_COLOR":  {mode: termcolor.Auto, w: file, vars: map[string]string{"NO_COLOR": "1"}, want: false},
		"auto, file and dumb TERM": {mode: termcolor.Auto, w: file, vars: map[string]string{"TERM": "dumb"}, want: false},
		"off, file and xterm TERM": {mode: termcolor.Off, w: file, vars: map[string]string{"TERM": "xterm"}, want: false},
		"empty mode, buffer":       {mode: "", w: &bytes.Buffer{}, want: false},
		"unknown mode, buffer":     {mode: "sometimes", w: &bytes.Buffer{}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := termcolor.Enabled(tc.mode, tc.w, env(tc.vars)); got != tc.want {
				t.Errorf("Enabled(%q) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// An on palette wraps text in the codes of its color; an off palette and
// empty text return the text unchanged.
func TestPalette(t *testing.T) {
	t.Parallel()

	on, off := termcolor.New(true), termcolor.New(false)

	got := []string{
		on.Green("a"), on.Yellow("a"), on.Red("a"), on.BoldRed("a"), on.Cyan("a"), on.Faint("a"),
		on.Green(""), off.Green("a"), off.BoldRed("a"), off.Faint("a"),
	}
	want := []string{
		"\x1b[32ma\x1b[0m", "\x1b[33ma\x1b[0m", "\x1b[31ma\x1b[0m", "\x1b[1;31ma\x1b[22;0m", "\x1b[36ma\x1b[0m",
		"\x1b[2ma\x1b[22m", "", "a", "a", "a",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("palette mismatch (-want +got):\n%s", diff)
	}

	if !on.On() || off.On() {
		t.Errorf("On() = %v and %v, want true and false", on.On(), off.On())
	}
}

// Every color of an off palette returns its text unchanged.
func TestPaletteOffKeepsText(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "text")
		p := termcolor.New(false)

		for _, got := range []string{p.Green(s), p.Yellow(s), p.Red(s), p.BoldRed(s), p.Cyan(s), p.Faint(s)} {
			if got != s {
				t.Fatalf("off palette changed %q to %q", s, got)
			}
		}
	})
}

// termcolorTerminal opens a pseudo-terminal and returns its terminal side.
func termcolorTerminal(t *testing.T) *os.File {
	t.Helper()

	ptmx, tty, err := pty.Open()
	if err != nil {
		testenv.Skipf(t, "opening a pseudo-terminal: %v", err)
	}

	t.Cleanup(func() { _ = tty.Close(); _ = ptmx.Close() })

	return tty
}

// IsTerminal is true only for a file that is a terminal. Auto and an empty or
// unknown mode color a terminal unless NO_COLOR is set or TERM is dumb.
func TestIsTerminalAndAutoOnATerminal(t *testing.T) {
	t.Parallel()

	tty := termcolorTerminal(t)

	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = file.Close() })

	for name, tc := range map[string]struct {
		w    io.Writer
		want bool
	}{
		"terminal":     {w: tty, want: true},
		"regular file": {w: file, want: false},
		"buffer":       {w: &bytes.Buffer{}, want: false},
		"nil":          {w: nil, want: false},
	} {
		t.Run("IsTerminal/"+name, func(t *testing.T) {
			t.Parallel()

			if got := termcolor.IsTerminal(tc.w); got != tc.want {
				t.Errorf("IsTerminal(%s) = %t, want %t", name, got, tc.want)
			}
		})
	}

	for name, tc := range map[string]struct {
		mode termcolor.Mode
		vars map[string]string
		want bool
	}{
		"auto":              {mode: termcolor.Auto, want: true},
		"auto, xterm":       {mode: termcolor.Auto, vars: map[string]string{"TERM": "xterm"}, want: true},
		"auto, NO_COLOR":    {mode: termcolor.Auto, vars: map[string]string{"NO_COLOR": "1"}, want: false},
		"auto, dumb TERM":   {mode: termcolor.Auto, vars: map[string]string{"TERM": "dumb"}, want: false},
		"empty mode":        {mode: "", want: true},
		"unknown mode":      {mode: "sometimes", want: true},
		"off":               {mode: termcolor.Off, want: false},
		"on, dumb TERM":     {mode: termcolor.On, vars: map[string]string{"TERM": "dumb"}, want: true},
		"off, terminal set": {mode: termcolor.Off, vars: map[string]string{"TERM": "xterm"}, want: false},
	} {
		t.Run("Enabled/"+name, func(t *testing.T) {
			t.Parallel()

			getenv := func(name string) string { return tc.vars[name] }

			if got := termcolor.Enabled(tc.mode, tty, getenv); got != tc.want {
				t.Errorf("Enabled(%q, terminal) with %s = %t, want %t", tc.mode, name, got, tc.want)
			}
		})
	}
}

// For reads NO_COLOR and TERM from the process environment.
//
//nolint:paralleltest // sets environment variables
func TestFor(t *testing.T) {
	tty := termcolorTerminal(t)

	tests := map[string]struct {
		mode          termcolor.Mode
		w             io.Writer
		noColor, term string
		want          bool
	}{
		"on, buffer":              {mode: termcolor.On, w: &bytes.Buffer{}, want: true},
		"off, terminal":           {mode: termcolor.Off, w: tty, term: "xterm", want: false},
		"auto, buffer":            {mode: termcolor.Auto, w: &bytes.Buffer{}, term: "xterm", want: false},
		"auto, terminal":          {mode: termcolor.Auto, w: tty, term: "xterm", want: true},
		"auto, terminal NO_COLOR": {mode: termcolor.Auto, w: tty, noColor: "1", term: "xterm", want: false},
		"auto, terminal dumb":     {mode: termcolor.Auto, w: tty, term: "dumb", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("TERM", tc.term)

			p := termcolor.For(tc.mode, tc.w)
			if p.On() != tc.want {
				t.Errorf("For(%q).On() = %t, want %t", tc.mode, p.On(), tc.want)
			}

			want := "a"
			if tc.want {
				want = "\x1b[32ma\x1b[0m"
			}

			if got := p.Green("a"); got != want {
				t.Errorf("For(%q).Green(a) = %q, want %q", tc.mode, got, want)
			}
		})
	}
}
