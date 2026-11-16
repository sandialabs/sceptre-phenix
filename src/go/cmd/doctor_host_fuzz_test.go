package cmd

import (
	"strings"
	"testing"
)

// unescapeMount reads back any field that /proc/mounts escapes, never makes a field longer, and leaves a field
// without a backslash as it is.
func FuzzUnescapeMount(f *testing.F) {
	for _, seed := range []string{
		"", "/tmp/minimega/dstImg123\\040x", "/dev/nbd2p1", `\`, `\0`, `\04`, `\040`, `\400`, `\777`, `\08a`, `a\\b`,
		"/mnt/a b\tc\nd",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, field string) {
		if got := unescapeMount(escapeMount(field)); got != field {
			t.Fatalf("unescapeMount(escapeMount(%q)) = %q", field, got)
		}

		got := unescapeMount(field)
		if len(got) > len(field) {
			t.Fatalf("unescapeMount(%q) = %q, longer than the field", field, got)
		}

		if !strings.Contains(field, `\`) && got != field {
			t.Fatalf("unescapeMount(%q) = %q, want the field without a backslash as it is", field, got)
		}
	})
}
