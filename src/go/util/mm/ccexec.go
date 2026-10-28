package mm

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

// ErrUnsafeC2Arg tells that a value cannot reach the VM unchanged through
// `cc exec`, so the command is not sent.
var ErrUnsafeC2Arg = errors.New("value cannot be sent unchanged through cc exec")

// CCExecCommand joins args into the command text for `cc exec`. minimega splits
// the text into words with the minicli rules and expands environment variables
// and URLs in each word. miniccc runs the words without a shell. Each word is
// quoted so that it reaches the VM unchanged. A word that minimega changes even
// when it is quoted gives an error that wraps [ErrUnsafeC2Arg].
func CCExecCommand(args ...string) (string, error) {
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
		return "", fmt.Errorf("%w: %q is not valid UTF-8", ErrUnsafeC2Arg, arg)
	case strings.ContainsRune(arg, 0):
		return "", fmt.Errorf("%w: %q contains a NUL character", ErrUnsafeC2Arg, arg)
	case os.Expand(arg, func(string) string { return "" }) != arg:
		return "", fmt.Errorf("%w: minimega expands the $ in %q as an environment variable", ErrUnsafeC2Arg, arg)
	}

	// The minimega preprocessor replaces these URLs with a local file path.
	if u, err := url.Parse(arg); err == nil {
		switch u.Scheme {
		case "file", "http", "https", "tar":
			return "", fmt.Errorf("%w: minimega reads %q as a %s URL", ErrUnsafeC2Arg, arg, u.Scheme)
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

// ccExecBareChars are the characters that minicli keeps as they are in a word
// that has no quotes.
const ccExecBareChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:,+@%="

// PowerShellQuote quotes s as a PowerShell single-quoted string. PowerShell
// reads the four typographic single quotes as quotes too, so each of them is
// doubled.
func PowerShellQuote(s string) string {
	double := strings.NewReplacer(
		"'", "''", "‘", "‘‘", "’", "’’",
		"‚", "‚‚", "‛", "‛‛",
	)

	return "'" + double.Replace(s) + "'"
}
