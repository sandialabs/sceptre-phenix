package mm

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrUnsafeArg is returned for a value minimega would not take as the one
// literal argument phenix writes it into a command as.
var ErrUnsafeArg = errors.New("minimega cannot take this value as one argument")

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

	if u, err := url.Parse(s); err == nil {
		switch u.Scheme {
		case "file", "http", "https", "tar":
			return fmt.Errorf("%w: %q starts with the %s: scheme minimega fetches", ErrUnsafeArg, s, u.Scheme)
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
