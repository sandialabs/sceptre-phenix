package web

import (
	"slices"
	"strings"
	"testing"
)

// FuzzPreferredEncoding checks preferredEncoding on any Accept-Encoding
// header against rules that decide it: it picks "" or an offered coding,
// never one a first entry weighs 0, the first offered on a tie, the same
// coding once that coding weighs 1, and the same coding in any ASCII case.
func FuzzPreferredEncoding(f *testing.F) {
	for _, seed := range []string{
		"", "br", "BR", "gzip", "gzip, deflate, br, zstd", "br;q=0.5, gzip", "br; q=0.001, gzip; Q=0.0",
		"br;q=0, gzip", "*", "*;q=0", "br;q=0, *", "gzip;q=0, *;q=0.5", "br;q=x, gzip", "br;q=2, gzip",
		"br;q=NaN", "br;level=1;q=0, gzip", "br;q=0, br", " , br , ", "gzip;q=1e-3", "br;q=0x1p-1", ";q=0",
	} {
		f.Add(seed)
	}

	offerings := [][]string{builderCodings(), {"gzip", "br"}, {"gzip"}}

	f.Fuzz(func(t *testing.T, header string) {
		for _, offered := range offerings {
			got := preferredEncoding(header, offered)

			if got != "" && !slices.Contains(offered, got) {
				t.Fatalf("preferredEncoding(%q, %q) = %q, want \"\" or an offered coding", header, offered, got)
			}

			for _, coding := range offered {
				if refused := preferredEncoding(coding+";q=0,"+header, offered); refused == coding {
					t.Fatalf("preferredEncoding(%q, %q) = %q, a coding its first entry weighs 0",
						coding+";q=0,"+header, offered, refused)
				}
			}

			tied := make([]string, len(offered))
			for i, coding := range offered {
				tied[i] = coding + ";q=0.5"
			}

			if tie := strings.Join(tied, ",") + "," + header; preferredEncoding(tie, offered) != offered[0] {
				t.Fatalf("preferredEncoding(%q, %q) = %q, want the first offered %q on a tie",
					tie, offered, preferredEncoding(tie, offered), offered[0])
			}

			if got == "" {
				continue
			}

			if raised := got + ";q=1," + header; preferredEncoding(raised, offered) != got {
				t.Fatalf("preferredEncoding(%q, %q) = %q, want %q as for %q", raised, offered,
					preferredEncoding(raised, offered), got, header)
			}

			if isASCII(header) && preferredEncoding(strings.ToUpper(header), offered) != got {
				t.Fatalf("preferredEncoding(%q, %q) = %q, want %q as for %q", strings.ToUpper(header), offered,
					preferredEncoding(strings.ToUpper(header), offered), got, header)
			}
		}
	})
}

// isASCII reports whether s holds only ASCII bytes, whose case
// [strings.ToUpper] and [strings.ToLower] change back and forth.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}

	return true
}
