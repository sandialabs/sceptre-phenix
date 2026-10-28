package mmcli

import (
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
