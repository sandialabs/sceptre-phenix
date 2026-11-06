package mmcli

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

var columnsPrefix = regexp.MustCompile(`\.columns ((?:"[^"]*",)*"[^"]*") `)

// vmTable is a `vm info` answer from two hosts and an error from a third. As
// minimega does, it leaves out the columns a `.columns` prefix does not name.
func vmTable(cmd mmtest.Command) []*minicli.Response {
	header := []string{"name", "state", "hostname"}
	rows := [][]string{{"a", "RUNNING", "web"}, {"b", "QUIT", "db"}, {"c", "ERROR", "c"}}

	if m := columnsPrefix.FindStringSubmatch(cmd.Raw); m != nil {
		var keep []int

		for i, h := range header {
			if strings.Contains(m[1], strconv.Quote(h)) {
				keep = append(keep, i)
			}
		}

		project := func(row []string) []string {
			out := make([]string, 0, len(keep))
			for _, i := range keep {
				out = append(out, row[i])
			}

			return out
		}

		header = project(header)
		for i := range rows {
			rows[i] = project(rows[i])
		}
	}

	return []*minicli.Response{
		mmtest.Tabular("head", header, rows[0]),
		mmtest.Tabular("compute1", header, rows[1], rows[2]),
		mmtest.Error("compute2", "vm not found: x"),
	}
}

func TestRunTabular(t *testing.T) {
	for name, tc := range map[string]struct {
		columns []string
		want    []map[string]string
	}{
		"every column with the host": {
			want: []map[string]string{
				{"host": "head", "name": "a", "state": "RUNNING", "hostname": "web"},
				{"host": "compute1", "name": "b", "state": "QUIT", "hostname": "db"},
				{"host": "compute1", "name": "c", "state": "ERROR", "hostname": "c"},
			},
		},
		"only the columns asked for": {
			columns: []string{"name", "host"},
			want: []map[string]string{
				{"host": "head", "name": "a"},
				{"host": "compute1", "name": "b"},
				{"host": "compute1", "name": "c"},
			},
		},
		"a column with host in its name is not the host": {
			columns: []string{"hostname"},
			want:    []map[string]string{{"hostname": "web"}, {"hostname": "db"}, {"hostname": "c"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, vmTable)

			cmd := NewNamespacedCommand("exp")
			cmd.Command = "vm info"
			cmd.Columns = tc.columns

			if diff := cmp.Diff(tc.want, RunTabular(cmd)); diff != "" {
				t.Errorf("RunTabular(vm info, columns %q) mismatch (-want +got):\n%s", tc.columns, diff)
			}

			if diff := cmp.Diff([]string{"vm info"}, mmtest.Bases(received())); diff != "" {
				t.Errorf("RunTabular(vm info) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// RunTabularCheckedContext returns the rows of the hosts that answered, and
// the error of each host that did not, matching the sentinels.
func TestRunTabularCheckedContextReportsErrors(t *testing.T) {
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		return append(vmTable(cmd), mmtest.Error("compute3", ErrTimeout.Error()))
	})

	cmd := NewCommand()
	cmd.Command = "vm info"
	cmd.Columns = []string{"name"}

	rows, err := RunTabularCheckedContext(context.Background(), cmd, nil)

	if diff := cmp.Diff([]map[string]string{{"name": "a"}, {"name": "b"}, {"name": "c"}}, rows); diff != "" {
		t.Errorf("RunTabularCheckedContext(vm info) rows mismatch (-want +got):\n%s", diff)
	}

	if !errors.Is(err, ErrTimeout) {
		t.Errorf("RunTabularCheckedContext(vm info) error = %v, want %v among the errors", err, ErrTimeout)
	}

	if err == nil || err.Error() != "vm not found: x\n"+ErrTimeout.Error() {
		t.Errorf("RunTabularCheckedContext(vm info) error = %v, want both hosts' errors", err)
	}
}

// An answer without rows is an empty list, not nil.
func TestRunTabularWithoutRows(t *testing.T) {
	useFakeMinimega(t, nil)

	cmd := NewCommand()
	cmd.Command = "vm info"

	if got := RunTabular(cmd); got == nil || len(got) != 0 {
		t.Errorf("RunTabular(vm info) without rows = %#v, want an empty list", got)
	}
}

// genTabularResponse draws a one-row tabular response, whose row can be
// shorter or longer than its header as a misbehaving minimega could send, and
// columns to ask for. minimega gives the host as the response's Host, never in
// the header.
func genTabularResponse(t *rapid.T) (*minicli.Response, []string) {
	names := []string{"name", "state", "hostname", "id", "uuid"}

	header := rapid.SliceOfDistinct(rapid.SampledFrom(names), rapid.ID[string]).Draw(t, "header")
	row := rapid.SliceOfN(rapid.StringMatching(`[a-z]{0,3}`), 0, len(header)+2).Draw(t, "row")
	columns := rapid.SliceOfDistinct(rapid.SampledFrom(append(names, "host")), rapid.ID[string]).Draw(t, "columns")

	return mmtest.Tabular(rapid.StringMatching(`[a-z]{1,4}`).Draw(t, "host"), header, row), columns
}

// Each mapped cell is the row's cell under that header, a short row never
// panics, and "host" is the response's host, set for the columns only when
// asked for.
func TestTabularMappersProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		resp, columns := genTabularResponse(t)
		row := resp.Tabular[0]

		want := map[string]string{"host": resp.Host}
		for i, header := range resp.Header {
			if i < len(row) {
				want[header] = row[i]
			}
		}

		if diff := cmp.Diff(want, tabularToMap(resp, row)); diff != "" {
			t.Fatalf("tabularToMap(%q, %q) mismatch (-want +got):\n%s", resp.Header, row, diff)
		}

		some := tabularToMapCols(columns)(resp, row)

		for key, value := range some {
			if value != want[key] || (key == "host" && !slices.Contains(columns, "host")) {
				t.Fatalf("tabularToMapCols(%q)(%q, %q)[%s] = %q, want %q", columns, resp.Header, row, key, value, want[key])
			}
		}

		for _, column := range columns {
			if _, ok := want[column]; ok && !hasKey(some, column) {
				t.Fatalf("tabularToMapCols(%q)(%q, %q) = %q, want column %s", columns, resp.Header, row, some, column)
			}
		}
	})
}

func hasKey(m map[string]string, key string) bool {
	_, ok := m[key]

	return ok
}
