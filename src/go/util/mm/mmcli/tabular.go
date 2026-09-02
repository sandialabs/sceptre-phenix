// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"github.com/hashicorp/go-multierror"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/plog"
)

type tabularToMapper func(*minicli.Response, []string) map[string]string

func tabularToMap(resp *minicli.Response, row []string) map[string]string {
	res := map[string]string{
		"host": resp.Host,
	}

	for i, header := range resp.Header {
		res[header] = row[i]
	}

	return res
}

func tabularToMapCols(columns []string) tabularToMapper {
	// create local copy of columns in case they get changed
	cols := make([]string, len(columns))
	copy(cols, columns)

	return func(resp *minicli.Response, row []string) map[string]string {
		res := map[string]string{}

		for _, column := range cols {
			// `host` is the responding minimega node, not a header; a substring
			// match here would also swallow the `hostname` column of `cc clients`
			if column == "host" {
				res["host"] = resp.Host

				continue
			}

			for i, header := range resp.Header {
				if column == header {
					res[header] = row[i]
				}
			}
		}

		return res
	}
}

// RunTabular is used to run the given command when the response is expected to
// be in tabular form. A slice of maps is returned, with each map representing a
// row in the tabular response and each map key representing the column. Errors
// are logged; use RunTabularErr to act on them.
func RunTabular(cmd *Command) []map[string]string {
	rows, err := RunTabularErr(cmd)
	if err != nil {
		plog.Error(plog.TypeSystem, "error running mm cmd", "cmd", cmd.Command, "error", err)
	}

	return rows
}

// RunTabularErr is RunTabular with the errors returned alongside the rows. On a
// mesh some hosts legitimately answer with an error (eg. a `cc` query on a host
// that does not own the VM), so callers decide whether the rows the other hosts
// returned are sufficient.
func RunTabularErr(cmd *Command) ([]map[string]string, error) {
	// copy all fields in header order
	mapper := tabularToMap

	if len(cmd.Columns) > 0 {
		// replace mapper to only copy fields in column order
		mapper = tabularToMapCols(cmd.Columns)
	}

	var (
		rows = []map[string]string{}
		errs error
	)

	for resps := range Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				errs = multierror.Append(errs, reconstructErr(resp.Error))

				continue
			}

			for _, row := range resp.Tabular {
				rows = append(rows, mapper(resp, row))
			}
		}
	}

	return rows, errs
}
