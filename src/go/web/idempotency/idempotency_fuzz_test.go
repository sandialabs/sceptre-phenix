package idempotency

import (
	"regexp"
	"strings"
	"testing"
)

// idempotencyKeyModel is the header value ValidKey accepts: 1 to 255 bytes of printable ASCII.
var idempotencyKeyModel = regexp.MustCompile(`^[ -~]{1,255}$`)

// FuzzValidKey checks ValidKey, which gates every Idempotency-Key header,
// against a regular expression of the keys it allows.
func FuzzValidKey(f *testing.F) {
	for _, seed := range []string{
		"", "k1", "a key with spaces", strings.Repeat("k", MaxKeyLength), strings.Repeat("k", MaxKeyLength+1),
		"tab\tkey", "café", "new\nline", "\x7f", "~", " ",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, key string) {
		if got, want := ValidKey(key), idempotencyKeyModel.MatchString(key); got != want {
			t.Fatalf("ValidKey(%q) = %t, want %t", key, got, want)
		}
	})
}
