package termcolor_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/termcolor"
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
