package envsubst

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// envName matches a variable name that an env file or a flag can set.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Entry is one NAME=value line of an env file. Line counts from 1.
type Entry struct {
	Name, Value string
	Line        int
}

// ParseError is the error for a line of an env file that is not valid. Path
// is empty until the caller that knows the file name sets it.
type ParseError struct {
	Path   string
	Line   int
	Reason string
}

func (e *ParseError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
	}

	return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Reason)
}

// ValidName reports whether name is a variable name that can be set: a letter
// or "_", then letters, digits and "_".
func ValidName(name string) bool {
	return envName.MatchString(name)
}

// ParseEnvFile returns the entries of an env file, in order. Each line is
// NAME=value, with an optional "export " before it and optional quotes around
// the value. Blank lines and lines that start with "#" are skipped. Nothing in
// the file is expanded. The error is a [*ParseError] for a line that is not valid.
func ParseEnvFile(r io.Reader) ([]Entry, error) {
	text, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading env file: %w", err)
	}

	var entries []Entry

	lines := strings.Split(strings.TrimPrefix(string(text), "\ufeff"), "\n")

	for i, line := range lines {
		entry, reason := parseEnvLine(line)
		if reason != "" {
			return nil, &ParseError{Path: "", Line: i + 1, Reason: reason}
		}

		if entry.Name == "" {
			continue
		}

		entry.Line = i + 1
		entries = append(entries, entry)
	}

	return entries, nil
}

// parseEnvLine returns the entry of line, or an entry with no name for a line
// to skip. A line that is not valid gives the reason.
func parseEnvLine(line string) (Entry, string) {
	line = strings.Trim(line, " \t\r")

	if line == "" || strings.HasPrefix(line, "#") {
		return Entry{}, ""
	}

	if !utf8.ValidString(line) {
		return Entry{}, "the line is not valid UTF-8"
	}

	if rest, ok := strings.CutPrefix(line, "export"); ok && strings.TrimLeft(rest, " \t") != rest {
		line = strings.TrimLeft(rest, " \t")
	}

	name, value, found := strings.Cut(line, "=")
	if !found {
		return Entry{}, "expected NAME=value"
	}

	if !ValidName(name) {
		return Entry{}, fmt.Sprintf("%q is not a valid variable name", name)
	}

	value, reason := unquoteEnvValue(value)
	if reason != "" {
		return Entry{}, reason
	}

	if strings.ContainsRune(value, '\r') {
		return Entry{}, "the value holds a carriage return"
	}

	return Entry{Name: name, Value: value, Line: 0}, ""
}

// unquoteEnvValue removes the quotes around a value that starts with one.
func unquoteEnvValue(value string) (string, string) {
	if value == "" || (value[0] != '\'' && value[0] != '"') {
		return value, ""
	}

	if len(value) == 1 || value[len(value)-1] != value[0] {
		return "", "the closing quote must be the last character of the line"
	}

	return value[1 : len(value)-1], ""
}
