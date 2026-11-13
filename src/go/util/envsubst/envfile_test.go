package envsubst_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/envsubst"
)

// envfileCases are env files and the entries that ParseEnvFile gives for them.
// The fuzz target takes its seeds from them.
var envfileCases = []struct { //nolint:gochecknoglobals // shared with the fuzz target
	name string
	text string
	want []envsubst.Entry
}{
	{name: "empty", text: "", want: nil},
	{name: "only blank lines and comments", text: "\n  \n# a comment\n\t# another\n", want: nil},
	{
		name: "plain values",
		text: "A=1\nB=two words\n",
		want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}, {Name: "B", Value: "two words", Line: 2}},
	},
	{name: "no newline at the end", text: "A=1", want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}}},
	{
		name: "CRLF line ends",
		text: "A=1\r\n\r\nB=2\r\n",
		want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}, {Name: "B", Value: "2", Line: 3}},
	},
	{name: "a byte order mark", text: "\ufeffA=1\n", want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}}},
	{name: "space around the line", text: "  \tA=1 \t\n", want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}}},
	{
		name: "export",
		text: "export A=1\nexport\tB=2\nexport=3\n",
		want: []envsubst.Entry{
			{Name: "A", Value: "1", Line: 1},
			{Name: "B", Value: "2", Line: 2},
			{Name: "export", Value: "3", Line: 3},
		},
	},
	{name: "an empty value", text: "A=\n", want: []envsubst.Entry{{Name: "A", Value: "", Line: 1}}},
	{name: "an equals sign in the value", text: "A==b=c\n", want: []envsubst.Entry{{Name: "A", Value: "=b=c", Line: 1}}},
	{name: "space after the equals sign", text: "A= b\n", want: []envsubst.Entry{{Name: "A", Value: " b", Line: 1}}},
	{name: "a number sign in the value", text: "A=b # c\n", want: []envsubst.Entry{{Name: "A", Value: "b # c", Line: 1}}},
	{
		name: "quotes",
		text: "A=\"  b  \"\nB='c d'\nC=\"\"\nD=''\n",
		want: []envsubst.Entry{
			{Name: "A", Value: "  b  ", Line: 1},
			{Name: "B", Value: "c d", Line: 2},
			{Name: "C", Value: "", Line: 3},
			{Name: "D", Value: "", Line: 4},
		},
	},
	{
		name: "quotes in the value",
		text: "A=\"b\"c\"\nB='it's'\nC=\"'\"\nD=b\"c\n",
		want: []envsubst.Entry{
			{Name: "A", Value: "b\"c", Line: 1},
			{Name: "B", Value: "it's", Line: 2},
			{Name: "C", Value: "'", Line: 3},
			{Name: "D", Value: "b\"c", Line: 4},
		},
	},
	{
		name: "a backslash stays",
		text: "A=\"b\\n\"\nB=c\\d\n",
		want: []envsubst.Entry{{Name: "A", Value: "b\\n", Line: 1}, {Name: "B", Value: "c\\d", Line: 2}},
	},
	{
		name: "nothing is expanded",
		text: "A=1\nB=${A} $A\n",
		want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}, {Name: "B", Value: "${A} $A", Line: 2}},
	},
	{
		name: "a name given twice",
		text: "A=1\nA=2\n",
		want: []envsubst.Entry{{Name: "A", Value: "1", Line: 1}, {Name: "A", Value: "2", Line: 2}},
	},
	{name: "names", text: "_a1=x\nA_B=y\n", want: []envsubst.Entry{{Name: "_a1", Value: "x", Line: 1}, {Name: "A_B", Value: "y", Line: 2}}},
}

func TestParseEnvFile(t *testing.T) {
	t.Parallel()

	for _, tt := range envfileCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := envsubst.ParseEnvFile(strings.NewReader(tt.text))
			if err != nil {
				t.Fatalf("ParseEnvFile(%q) error = %v, want nil", tt.text, err)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ParseEnvFile(%q) mismatch (-want +got):\n%s", tt.text, diff)
			}
		})
	}
}

// envfileBadLines are env files that ParseEnvFile refuses, with the line of
// the error.
var envfileBadLines = []struct { //nolint:gochecknoglobals // shared with the fuzz target
	name string
	text string
	line int
}{
	{name: "no equals sign", text: "A=1\nB\n", line: 2},
	{name: "space in the name", text: "A B=1\n", line: 1},
	{name: "space before the equals sign", text: "A =1\n", line: 1},
	{name: "a name that starts with a digit", text: "\n\n1A=x\n", line: 3},
	{name: "a dash in the name", text: "A-B=x\n", line: 1},
	{name: "no name", text: "=x\n", line: 1},
	{name: "export with no assignment", text: "export A\n", line: 1},
	{name: "a quote that does not close", text: "A=\"b\n", line: 1},
	{name: "a lone quote", text: "A='\n", line: 1},
	{name: "text after the closing quote", text: "A=\"b\" # c\n", line: 1},
	{name: "quotes that do not match", text: "A=\"b'\n", line: 1},
	{name: "not UTF-8", text: "A=\xff\n", line: 1},
	{name: "a carriage return in the value", text: "A=b\rc\n", line: 1},
}

func TestParseEnvFileErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range envfileBadLines {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := envsubst.ParseEnvFile(strings.NewReader(tt.text))

			var parseErr *envsubst.ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("ParseEnvFile(%q) = %v, %v, want a *ParseError", tt.text, got, err)
			}

			if parseErr.Line != tt.line || parseErr.Reason == "" {
				t.Errorf(
					"ParseEnvFile(%q) error = line %d, reason %q, want line %d and a reason",
					tt.text, parseErr.Line, parseErr.Reason, tt.line,
				)
			}
		})
	}
}

func TestParseErrorText(t *testing.T) {
	t.Parallel()

	err := &envsubst.ParseError{Path: "", Line: 3, Reason: "expected NAME=value"}
	if got, want := err.Error(), "line 3: expected NAME=value"; got != want {
		t.Errorf("ParseError.Error() = %q, want %q", got, want)
	}

	err.Path = "site/.env"
	if got, want := err.Error(), "site/.env:3: expected NAME=value"; got != want {
		t.Errorf("ParseError.Error() with a path = %q, want %q", got, want)
	}
}

func TestValidName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"A": true, "_": true, "a_1": true, "NODES": true,
		"": false, "1A": false, "A-B": false, "A B": false, "A=": false, "é": false,
	} {
		if got := envsubst.ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %t, want %t", name, got, want)
		}
	}
}

// envfileFormat writes entries as an env file that ParseEnvFile reads back:
// each value in double quotes, which keep every character but a line break.
func envfileFormat(entries []envsubst.Entry) string {
	var b strings.Builder

	for _, entry := range entries {
		b.WriteString(entry.Name + "=\"" + entry.Value + "\"\n")
	}

	return b.String()
}

// genEnvfileEntries draws entries whose values hold quotes, blanks, "#", "$"
// and "=", but no line break.
func genEnvfileEntries() *rapid.Generator[[]envsubst.Entry] {
	return rapid.Custom(func(t *rapid.T) []envsubst.Entry {
		count := rapid.IntRange(0, 5).Draw(t, "entries")
		entries := make([]envsubst.Entry, 0, count)

		for i := range count {
			entries = append(entries, envsubst.Entry{
				Name:  rapid.StringMatching(`[A-Za-z_][A-Za-z0-9_]{0,5}`).Draw(t, "name"),
				Value: rapid.StringOfN(rapid.RuneFrom([]rune("ab \t\"'#$={}\\é")), 0, 8, -1).Draw(t, "value"),
				Line:  i + 1,
			})
		}

		return entries
	})
}

// TestParseEnvFileProperties checks that a written env file reads back as the
// same entries, and that an unquoted value with no blank at its ends, and no
// quote at its start, reads back as written.
func TestParseEnvFileProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		want := genEnvfileEntries().Draw(t, "entries")
		text := envfileFormat(want)

		got, err := envsubst.ParseEnvFile(strings.NewReader(text))
		if err != nil {
			t.Fatalf("ParseEnvFile(%q) error = %v, want nil", text, err)
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("ParseEnvFile(%q) mismatch (-want +got):\n%s", text, diff)
		}
	})

	rapid.Check(t, func(t *rapid.T) {
		value := rapid.StringMatching(`[a-z#$=][a-z #$="']{0,6}[a-z#$=]`).Draw(t, "value")
		text := "export NAME=" + value + "\r\n"

		got, err := envsubst.ParseEnvFile(strings.NewReader(text))
		if err != nil {
			t.Fatalf("ParseEnvFile(%q) error = %v, want nil", text, err)
		}

		if diff := cmp.Diff([]envsubst.Entry{{Name: "NAME", Value: value, Line: 1}}, got); diff != "" {
			t.Fatalf("ParseEnvFile(%q) mismatch (-want +got):\n%s", text, diff)
		}
	})
}
