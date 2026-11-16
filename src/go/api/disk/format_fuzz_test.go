package disk

import (
	"strings"
	"testing"
)

// formatFuzzUnquote reads s as one POSIX shell word made only of single-quoted runs, backslash escapes and characters
// shellSafe allows unquoted, and reports whether s is such a word.
func formatFuzzUnquote(s string) (string, bool) {
	var word strings.Builder

	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", false
			}

			word.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
		case shellSafe.MatchString(string(c)):
			word.WriteByte(c)
		default:
			return "", false
		}
	}

	return word.String(), true
}

// A quoted string is one shell word that the shell reads back as the string, whatever bytes it holds, and a string
// with only safe characters is not quoted.
func FuzzShellQuote(f *testing.F) {
	for _, seed := range []string{
		"/f/win10.qcow2", "/f/a_b-c+d:e@f%g=h,i", "", "/f/my kali.qc2", "/f/it's.qc2", "/f/$HOME;rm", "/f/a\nb", "''",
		"/f/\x00\xff", `\'`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		quoted := shellQuote(s)

		got, ok := formatFuzzUnquote(quoted)
		if !ok || got != s {
			t.Fatalf("shellQuote(%q) = %q, which the shell reads as %q, %t; want %q as one word", s, quoted, got, ok, s)
		}

		if shellSafe.MatchString(s) && quoted != s {
			t.Errorf("shellQuote(%q) = %q, want it as it is", s, quoted)
		}
	})
}
