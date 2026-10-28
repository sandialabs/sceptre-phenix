package web

import (
	"strings"
	"testing"
)

// webSharkFuzzForwarded is a model of forwardedHTTPS, read in one pass as RFC 7239 has it: the first element ends at
// the first comma outside a quoted string, its pairs end at semicolons outside one, and a backslash in a quoted string
// quotes the next byte. It reports whether the element's first proto pair is https.
func webSharkFuzzForwarded(header string) bool {
	var (
		pairs  []string
		pair   strings.Builder
		quoted bool
	)

scan:
	for i := 0; i < len(header); i++ {
		switch c := header[i]; {
		case quoted && c == '\\':
			pair.WriteByte(c)

			if i+1 < len(header) {
				i++
				pair.WriteByte(header[i])
			}
		case c == '"':
			quoted = !quoted
			pair.WriteByte(c)
		case !quoted && c == ',':
			break scan
		case !quoted && c == ';':
			pairs = append(pairs, pair.String())
			pair.Reset()
		default:
			pair.WriteByte(c)
		}
	}

	pairs = append(pairs, pair.String())

	for _, p := range pairs {
		if k, v, _ := strings.Cut(strings.TrimSpace(p), "="); strings.EqualFold(k, "proto") {
			return strings.EqualFold(strings.Trim(v, `"`), "https")
		}
	}

	return false
}

// cutUnquoted splits a header only at a separator outside quotes, losing no byte, and forwardedHTTPS reads only
// the first proxy's element, as the model does.
func FuzzForwardedHTTPS(f *testing.F) {
	for _, seed := range []string{
		`for=192.0.2.1;proto=https;host=a`, `For="[2001:db8::1]"; Proto="HTTPS"`, "proto=http, proto=https",
		"for=192.0.2.1", `for=192.0.2.1;host="a;proto=https"`, `host="a,b";proto=https`, `host="a\";proto=https`,
		`"`, `\`, "proto=", "=https", ";;,,", `proto="https`, `host="a\`, ", proto=https", "proto=https;proto=http",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, forwarded string) {
		for _, sep := range []byte{',', ';'} {
			before, after := cutUnquoted(forwarded, sep)
			if before != forwarded && before+string(sep)+after != forwarded {
				t.Fatalf("cutUnquoted(%q, %q) = %q, %q, want the header split at one separator", forwarded, sep, before, after)
			}

			if again, rest := cutUnquoted(before, sep); again != before || rest != "" {
				t.Fatalf("cutUnquoted(%q, %q) = %q, which splits again at %q", forwarded, sep, before, rest)
			}
		}

		if got, want := forwardedHTTPS(forwarded), webSharkFuzzForwarded(forwarded); got != want {
			t.Fatalf("forwardedHTTPS(%q) = %t, want %t as the model reads it", forwarded, got, want)
		}

		first, _ := cutUnquoted(forwarded, ',')
		if got, want := forwardedHTTPS(forwarded), forwardedHTTPS(first); got != want {
			t.Fatalf("forwardedHTTPS(%q) = %t, want %t as for its first element %q", forwarded, got, want, first)
		}

		if !forwardedHTTPS("proto=https;" + forwarded) {
			t.Fatalf("forwardedHTTPS(%q) = false, want true", "proto=https;"+forwarded)
		}
	})
}
