package mmcli

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// String never asks minicli for a `host` column: no header has that name, so
// minicli fails (`cc commands`) or takes a header that starts with it
// (`hostname` in `vm info` and `cc clients`).
func TestCommandStringLeavesOutHostColumn(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		columns []string
		want    string
	}{
		"no columns": {
			want: `.record false namespace "ns" .filter id=7 cc commands`,
		},
		"host among others": {
			columns: []string{"host", "id", "responses"},
			want:    `.record false namespace "ns" .columns "id","responses" .filter id=7 cc commands`,
		},
		"host alone": {
			columns: []string{"host"},
			want:    `.record false namespace "ns" .filter id=7 cc commands`,
		},
		"columns that start with host": {
			columns: []string{"hostname", "host"},
			want:    `.record false namespace "ns" .columns "hostname" .filter id=7 cc commands`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := NewNamespacedCommand("ns")
			cmd.Command = "cc commands"
			cmd.Columns = tc.columns
			cmd.Filters = []string{"id=7"}

			if got := cmd.String(); got != tc.want {
				t.Errorf("Command{Columns: %q}.String() = %q, want %q", tc.columns, got, tc.want)
			}
		})
	}
}

// String asks for each column but host, quoted and in order, and leaves out
// `.columns` when nothing is left to ask for.
func TestCommandStringColumnsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		columns := rapid.SliceOf(rapid.SampledFrom([]string{"host", "id", "name", "hostname", "a b"})).Draw(t, "columns")

		cmd := NewCommand()
		cmd.Command = "vm info"
		cmd.Columns = columns

		var want []string

		for _, column := range columns {
			if column != "host" {
				want = append(want, strconv.Quote(column))
			}
		}

		got := cmd.String()

		if len(want) == 0 {
			if strings.Contains(got, ".columns") {
				t.Fatalf("Command{Columns: %q}.String() = %q, want no .columns", columns, got)
			}

			return
		}

		if wantCmd := ".record false .columns " + strings.Join(want, ",") + " vm info"; got != wantCmd {
			t.Fatalf("Command{Columns: %q}.String() = %q, want %q", columns, got, wantCmd)
		}
	})
}

// safeArgCases are the table of TestSafeArg and the seeds of FuzzSafeArg.
//
//nolint:gochecknoglobals // shared test table
var safeArgCases = []struct {
	name string
	arg  string
	safe bool
}{
	{name: "plain name", arg: "vm1", safe: true},
	{name: "empty", arg: "", safe: true},
	{name: "path", arg: "/phenix/images/a_b-c.qc2", safe: true},
	{name: "IPv6 address", arg: "fe80::1", safe: true},
	{name: "host range", arg: "node[1-3]", safe: true},
	{name: "colon in a name", arg: "exp:a", safe: true},
	{name: "quote that ends a quoted value", arg: `a\" help x`},
	{name: "double quote", arg: `a"b`},
	{name: "single quote", arg: "a'b"},
	{name: "backtick", arg: "a`id`"},
	{name: "backslash", arg: `a\b`},
	{name: "newline", arg: "vm1\nvm info"},
	{name: "tab", arg: "a\tb"},
	{name: "space", arg: "a b"},
	{name: "no-break space", arg: "a\u00a0b"},
	{name: "control character", arg: "a\x00b"},
	{name: "comment leader", arg: "a#b"},
	{name: "environment variable", arg: "$HOME"},
	{name: "braced environment variable", arg: "/a/${HOME}"},
	{name: "HTTP URL", arg: "http://x/y"},
	{name: "URL in a path", arg: "/a/b://c"},
	{name: "tar scheme", arg: "tar:/x.tar"},
	{name: "file scheme", arg: "file:foo"},
	{name: "scheme in capitals", arg: "FILE:foo"},
	{name: "HTTPS scheme with no slashes", arg: "https:x"},
	{name: "invalid UTF-8", arg: "a\xffb"},
}

// SafeArg accepts only values that minimega reads as the same literal text.
func TestSafeArg(t *testing.T) {
	t.Parallel()

	for _, tc := range safeArgCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := SafeArg(tc.arg)

			switch {
			case tc.safe && err != nil:
				t.Errorf("SafeArg(%q) = %v, want nil", tc.arg, err)
			case !tc.safe && !errors.Is(err, ErrUnsafeArg):
				t.Errorf("SafeArg(%q) = %v, want %v", tc.arg, err, ErrUnsafeArg)
			}
		})
	}
}
