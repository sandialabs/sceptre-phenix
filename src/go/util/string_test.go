package util_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	"phenix/util"
)

const randomAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString returns n characters, each a letter or a digit.
func TestRandomStringProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 64).Draw(t, "n")
		got := util.RandomString(n)

		if utf8.RuneCountInString(got) != n || len(got) != n {
			t.Fatalf("RandomString(%d) = %q, want %d characters", n, got, n)
		}

		if i := strings.IndexFunc(got, func(r rune) bool { return !strings.ContainsRune(randomAlphabet, r) }); i >= 0 {
			t.Fatalf("RandomString(%d) = %q, has %q, which is not a letter or a digit", n, got, got[i])
		}
	})
}

// Two long random strings are almost never the same.
func TestRandomStringVaries(t *testing.T) {
	t.Parallel()

	if a, b := util.RandomString(32), util.RandomString(32); a == b {
		t.Errorf("RandomString(32) = %q two times, want different strings", a)
	}
}
