package web

import "testing"

// cutUnquoted splits a header only at a separator outside quotes, losing no
// byte, and forwardedHTTPS reads only the first proxy's element.
func FuzzForwardedHTTPS(f *testing.F) {
	for _, seed := range []string{
		`for=192.0.2.1;proto=https;host=a`, `For="[2001:db8::1]"; Proto="HTTPS"`, "proto=http, proto=https",
		"for=192.0.2.1", `for=192.0.2.1;host="a;proto=https"`, `host="a,b";proto=https`, `host="a\";proto=https`,
		`"`, `\`, "proto=", "=https", ";;,,",
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

		first, _ := cutUnquoted(forwarded, ',')
		if got, want := forwardedHTTPS(forwarded), forwardedHTTPS(first); got != want {
			t.Fatalf("forwardedHTTPS(%q) = %t, want %t as for its first element %q", forwarded, got, want, first)
		}

		if !forwardedHTTPS("proto=https;" + forwarded) {
			t.Fatalf("forwardedHTTPS(%q) = false, want true", "proto=https;"+forwarded)
		}
	})
}
