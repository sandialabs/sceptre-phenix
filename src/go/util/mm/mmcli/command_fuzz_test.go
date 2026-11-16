package mmcli

import (
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// FuzzSafeArg checks SafeArg against minimega's own rules: a value it accepts
// lexes as one unquoted item (minicli's needsQuote in pkg/minicli/input.go),
// has nothing that [os.ExpandEnv] changes, and has no URL scheme that the
// preprocessor (cliPreprocess in cmd/minimega/cli.go) fetches.
func FuzzSafeArg(f *testing.F) {
	for _, tc := range safeArgCases {
		f.Add(tc.arg)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if SafeArg(s) != nil || s == "" {
			return
		}

		needsQuote := strings.IndexFunc(s, unicode.IsSpace) > -1 ||
			strings.ContainsAny(s, `'#`) ||
			strconv.Quote(s) != `"`+s+`"`
		if needsQuote {
			t.Fatalf("SafeArg(%q) = nil, but minicli needs to quote it", s)
		}

		if expanded := os.Expand(s, func(string) string { return "X" }); expanded != s {
			t.Fatalf("SafeArg(%q) = nil, but minimega expands it to %q", s, expanded)
		}

		if u, err := url.Parse(s); err == nil && slices.Contains(fetchedSchemes, u.Scheme) {
			t.Fatalf("SafeArg(%q) = nil, but minimega fetches its %s: URL", s, u.Scheme)
		}
	})
}
