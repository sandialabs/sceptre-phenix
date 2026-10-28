package soh

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

// errUnsafeC2Arg tells that a check value cannot reach the VM unchanged
// through `cc exec`, so the check is not sent.
var errUnsafeC2Arg = errors.New("value cannot be sent unchanged through cc exec")

// ccExecCommand joins args into the command text for `cc exec`. minimega splits
// the text into words with the minicli rules and expands environment variables
// and URLs in each word. miniccc runs the words without a shell. Each word is
// quoted so that it reaches the VM unchanged. A word that minimega changes
// even when it is quoted gives an error.
func ccExecCommand(args ...string) (string, error) {
	words := make([]string, len(args))

	for i, arg := range args {
		word, err := ccExecWord(arg)
		if err != nil {
			return "", err
		}

		words[i] = word
	}

	return strings.Join(words, " "), nil
}

func ccExecWord(arg string) (string, error) {
	switch {
	case !utf8.ValidString(arg):
		return "", fmt.Errorf("%w: %q is not valid UTF-8", errUnsafeC2Arg, arg)
	case strings.ContainsRune(arg, 0):
		return "", fmt.Errorf("%w: %q contains a NUL character", errUnsafeC2Arg, arg)
	case os.Expand(arg, func(string) string { return "" }) != arg:
		return "", fmt.Errorf("%w: minimega expands the $ in %q as an environment variable", errUnsafeC2Arg, arg)
	}

	// The minimega preprocessor replaces these URLs with a local file path.
	if u, err := url.Parse(arg); err == nil {
		switch u.Scheme {
		case "file", "http", "https", "tar":
			return "", fmt.Errorf("%w: minimega reads %q as a %s URL", errUnsafeC2Arg, arg, u.Scheme)
		}
	}

	if arg != "" && strings.Trim(arg, ccExecBareChars) == "" {
		return arg, nil
	}

	quote := `'`
	if strings.Contains(arg, `'`) && !strings.Contains(arg, `"`) {
		quote = `"`
	}

	escape := strings.NewReplacer(`\`, `\\`, quote, `\`+quote, "\n", `\n`, "\r", `\r`, "\t", `\t`)

	return quote + escape.Replace(arg) + quote, nil
}

const alnumChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// ccExecBareChars are the characters that minicli keeps as they are in a word
// that has no quotes.
const ccExecBareChars = alnumChars + "-_./:,+@%="

// psQuote quotes s as a PowerShell single-quoted string. PowerShell reads the
// four typographic single quotes as quotes too, so each of them is doubled.
func psQuote(s string) string {
	double := strings.NewReplacer(
		"'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019",
		"\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b",
	)

	return "'" + double.Replace(s) + "'"
}

// listenerPunct are the characters other than letters and digits allowed in
// each part of a listener value. The ss filter language has no quotes, so
// other characters are refused.
const listenerPunct = "-_.%*/"
