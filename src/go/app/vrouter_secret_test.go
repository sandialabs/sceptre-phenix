package app

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const secretChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// TestGenerateSecretDiffers checks that back-to-back IPsec preshared keys
// differ, which a generator seeded from the clock did not promise.
func TestGenerateSecretDiffers(t *testing.T) {
	a, b := generateSecret(ipsecSecretLength), generateSecret(ipsecSecretLength)
	if a == b {
		t.Errorf("generateSecret(%d) returned %q twice", ipsecSecretLength, a)
	}
}

// TestGenerateSecretProperties checks that a secret has the length asked for
// and holds only letters and digits.
func TestGenerateSecretProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 128).Draw(t, "n")

		got := generateSecret(n)
		if len(got) != n {
			t.Fatalf("len(generateSecret(%d)) = %d, want %d", n, len(got), n)
		}

		if i := strings.IndexFunc(got, func(r rune) bool { return !strings.ContainsRune(secretChars, r) }); i >= 0 {
			t.Fatalf("generateSecret(%d) = %q, has %q at %d outside letters and digits", n, got, got[i], i)
		}
	})
}
