package rbac

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// usernameModel is a byte-by-byte reading of the rule that ValidateUsername
// documents.
func usernameModel(u string) bool {
	if u == "" {
		return false
	}

	for i := range len(u) {
		c := u[i]
		alnum := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'

		if !alnum && (i == 0 || !strings.ContainsRune("._@+-", rune(c))) {
			return false
		}
	}

	return true
}

// FuzzValidateUsername checks ValidateUsername against usernameModel, and
// checks each name it accepts with usernameCheck.
func FuzzValidateUsername(f *testing.F) {
	for _, name := range []string{
		"", "alice#tag", "alice?x", "a/b", "a%2Fb", ".", "..", ".alice", "-alice", "a*", "a[b]", `a\b`, "a b", "élan",
		"alice", "user@example.org", "j.doe+lab_1-x", "0", "alice\n", "a\x00",
	} {
		f.Add(name)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if err := usernameCheck(name); err != nil {
			t.Fatal(err)
		}
	})
}

// usernameCheck checks ValidateUsername(name) against usernameModel, and that
// a name it accepts is one URL path segment as it is, not a dot segment, and a
// resource name pattern that matches only itself.
func usernameCheck(name string) error {
	text := func(err error) string { return fmt.Sprint(err) }

	err := ValidateUsername(name)

	if want := usernameModel(name); (err == nil) != want {
		return fmt.Errorf("ValidateUsername(%q) = %s, want accepted: %t", name, text(err), want)
	}

	if err != nil {
		if !errors.Is(err, ErrInvalidUsername) {
			return fmt.Errorf("ValidateUsername(%q) = %w, want an error that wraps %w", name, err, ErrInvalidUsername)
		}

		return nil
	}

	if got := url.PathEscape(name); got != name {
		return fmt.Errorf("url.PathEscape(%q) = %q, want it unchanged", name, got)
	}

	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `*?[]\/!`) {
		return fmt.Errorf("ValidateUsername(%q) accepted a dot segment or a pattern character", name)
	}

	if matched, err := filepath.Match(name, name); !matched || err != nil {
		return fmt.Errorf("filepath.Match(%q, %q) = %t, %s, want true, nil", name, name, matched, text(err))
	}

	if matched, err := filepath.Match(name, name+"x"); matched || err != nil {
		return fmt.Errorf("filepath.Match(%q, %q) = %t, %s, want false, nil", name, name+"x", matched, text(err))
	}

	return nil
}
