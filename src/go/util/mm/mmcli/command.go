// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Command represents a command and options to send to minimega.
type Command struct {
	Command   string
	Columns   []string
	Filters   []string
	Namespace string
	Timeout   time.Duration
	// Background commands, such as screenshots, wait for the shared connection
	// behind every other command waiting, so they do not hold up user actions.
	Background bool
}

// NewCommand returns a pointer to a new, initialized command.
func NewCommand() *Command {
	return new(Command)
}

// NewNamespacedCommand returns a pointer to a new command, initialized with the
// given minimega namespace name.
func NewNamespacedCommand(ns string) *Command {
	return &Command{Namespace: ns} //nolint:exhaustruct // partial initialization
}

// String builds the actual command string to send to minimega using the command
// fields.
func (c *Command) String() string {
	cmd := c.Command

	// Apply filters first so we don't need to worry about the columns not
	// including the filtered fields.
	for _, f := range c.Filters {
		cmd = fmt.Sprintf(".filter %v %v", f, cmd)
	}

	// minicli gives the answering node as the response's host, not as a column, so
	// `host` is never asked for: the mappers in tabular.go fill it in.
	columns := make([]string, 0, len(c.Columns))

	for _, column := range c.Columns {
		if column != hostColumn {
			// Quote the column in case it has spaces.
			columns = append(columns, fmt.Sprintf("%q", column))
		}
	}

	if len(columns) > 0 {
		cmd = fmt.Sprintf(".columns %v %v", strings.Join(columns, ","), cmd)
	}

	// If there's a namespace, use it.
	if c.Namespace != "" {
		cmd = fmt.Sprintf("namespace %q %v", c.Namespace, cmd)
	}

	// Don't record command in history.
	cmd = fmt.Sprintf(".record false %v", cmd)

	return cmd
}

// ErrUnsafeArg is returned for a value minimega would not take as the one
// literal argument phenix writes it into a command as.
var ErrUnsafeArg = errors.New("minimega cannot take this value as one argument")

// fetchedSchemes are the URL schemes for which the minimega preprocessor
// replaces an argument with a local file that it fetches or extracts.
//
//nolint:gochecknoglobals // fixed list
var fetchedSchemes = []string{"file", "http", "https", "tar"}

// SafeArg returns an error that wraps ErrUnsafeArg unless minimega reads s,
// written without quotes in a command, as the same literal text. s must not
// have a quote, a backtick, a backslash, "$", "#", white space or a control
// character. It must not have "://" or start with a file:, http:, https: or
// tar: scheme, as the minimega preprocessor fetches such arguments. SafeArg
// accepts ":" (IPv6 addresses), "[" and "]" (host ranges), and "".
func SafeArg(s string) error {
	unsafe := func(r rune) bool {
		return strings.ContainsRune("\"'`\\$#", r) || unicode.IsSpace(r) || unicode.IsControl(r)
	}

	if i := strings.IndexFunc(s, unsafe); i >= 0 {
		return unsafeRune(s, i)
	}

	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %q is not UTF-8", ErrUnsafeArg, s)
	}

	if strings.Contains(s, "://") {
		return fmt.Errorf("%w: %q contains \"://\", which minimega reads as a URL", ErrUnsafeArg, s)
	}

	if scheme, _, ok := strings.Cut(s, ":"); ok {
		for _, fetched := range fetchedSchemes {
			if strings.EqualFold(scheme, fetched) {
				return fmt.Errorf("%w: %q starts with the %s: scheme, which minimega fetches", ErrUnsafeArg, s, fetched)
			}
		}
	}

	return nil
}

func unsafeRune(s string, i int) error {
	r, _ := utf8.DecodeRuneInString(s[i:])

	return fmt.Errorf("%w: %q contains %q", ErrUnsafeArg, s, r)
}
