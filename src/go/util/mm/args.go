package mm

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"phenix/util/mm/mmcli"
)

// ErrUnsafeArg is returned for a value minimega would not take as the one
// literal argument phenix writes it into a command as. It is
// mmcli.ErrUnsafeArg, so [errors.Is] matches the errors of both packages.
var ErrUnsafeArg = mmcli.ErrUnsafeArg

// ValidateArg returns an error wrapping ErrUnsafeArg unless minimega takes s, written bare, as one literal
// argument: s is not empty, has no white space, quote, backslash or "#", and passes ValidateQuotedArg.
func ValidateArg(s string) error {
	if s == "" {
		return fmt.Errorf("%w: empty value", ErrUnsafeArg)
	}

	if i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(`'#`, r) }); i >= 0 {
		return unsafeRune(s, i)
	}

	return ValidateQuotedArg(s)
}

// ValidateQuotedArg returns an error wrapping ErrUnsafeArg unless minimega takes s, double-quoted, as one literal
// argument: UTF-8 with no quote, backslash, "$", control character but \n or \t, or file:/http:/https:/tar: scheme.
func ValidateQuotedArg(s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %q is not UTF-8", ErrUnsafeArg, s)
	}

	unsafe := func(r rune) bool {
		return r == '"' || r == '\\' || r == '$' || (unicode.IsControl(r) && r != '\n' && r != '\t')
	}

	if i := strings.IndexFunc(s, unsafe); i >= 0 {
		return unsafeRune(s, i)
	}

	if scheme, ok := fetchedScheme(s); ok {
		return fmt.Errorf("%w: %q starts with the %s: scheme minimega fetches", ErrUnsafeArg, s, scheme)
	}

	return nil
}

// fetchedScheme returns the URL scheme of s if minimega replaces an argument s
// with a local file it fetches: file:, http:, https: or tar:.
func fetchedScheme(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}

	switch u.Scheme {
	case "file", "http", "https", "tar":
		return u.Scheme, true
	}

	return "", false
}

// ValidateUnexpanded returns an error wrapping ErrUnsafeArg if minimega would
// change s before it runs a command with s in it. minimega expands environment
// variables ($VAR, ${VAR}, $1) in every argument, also in quotes, and replaces
// an argument that starts with a file:, http:, https: or tar: scheme with a
// local file. s can hold several words, such as a whole command.
func ValidateUnexpanded(s string) error {
	if os.Expand(s, func(string) string { return "" }) != s {
		return fmt.Errorf("%w: minimega expands the $ in %q as an environment variable", ErrUnsafeArg, s)
	}

	for _, word := range append([]string{s}, strings.Fields(s)...) {
		if scheme, ok := fetchedScheme(strings.Trim(word, `"'`)); ok {
			return fmt.Errorf("%w: minimega reads %q in %q as a %s URL", ErrUnsafeArg, word, s, scheme)
		}
	}

	return nil
}

// ValidateTags returns the ValidateQuotedArg error of the first VM tag key or
// value, in key order, minimega would not take as written.
func ValidateTags(tags map[string]string) error {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		if err := ValidateQuotedArg(k); err != nil {
			return fmt.Errorf("tag key: %w", err)
		}

		if err := ValidateQuotedArg(tags[k]); err != nil {
			return fmt.Errorf("value of tag %q: %w", k, err)
		}
	}

	return nil
}

func unsafeRune(s string, i int) error {
	r, _ := utf8.DecodeRuneInString(s[i:])

	return fmt.Errorf("%w: %q contains %q", ErrUnsafeArg, s, r)
}
