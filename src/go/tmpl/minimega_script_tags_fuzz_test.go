package tmpl_test

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

// FuzzMinimegaScriptTagRoundTrip checks that minicli reads every label key and
// value back from the minimega script exactly, on one line.
func FuzzMinimegaScriptTagRoundTrip(f *testing.F) {
	minicli.Reset()
	f.Cleanup(minicli.Reset)
	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"vm config tags <key> <value>"},
	})

	// Fuzzing seeds testing a variety of potential breakage areas
	f.Add(`a "quoted" \ key`, `C:\temp\`)
	f.Add("label", "First line\r\nSecond line\t#")
	f.Add("key\nshell id", "x")
	f.Add("empty", "")
	f.Add("région", "naïve café ✓")
	f.Add("emoji 🚀", "🧪 lab 👩‍💻")
	f.Add("  padded  ", "   leading and trailing   ")
	f.Add("it's", `don't 'quote' me`)
	f.Add("json", `{"role":"web","ports":[80,443]}`)
	f.Add("shell", "$(id); `whoami` | tee /tmp/x && echo $HOME")
	f.Add("<key>", "[optional] {a,b} <value>")
	f.Add("control", "nul\x00 esc\x1b[31m cr\r bell\a")
	f.Add("unicode breaks", "line para next\u0085end")

	f.Fuzz(func(t *testing.T, key, value string) {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Skip("labels come from YAML or JSON, which are UTF-8")
		}

		lines := minimegaScript(t, map[string]string{key: value})

		i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "vm config tags ") })
		if i < 0 || i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "vm launch ") {
			t.Fatalf("tag line not followed by the launch:\n%s", strings.Join(lines, "\n"))
		}

		cmd, err := minicli.Compile(lines[i])
		if err != nil {
			t.Fatalf("minicli.Compile(%q) error = %v", lines[i], err)
		}

		if got, want := [2]string{cmd.StringArgs["key"], cmd.StringArgs["value"]}, [2]string{key, value}; got != want {
			t.Errorf("round trip of %q = %q, want %q", lines[i], got, want)
		}
	})
}
