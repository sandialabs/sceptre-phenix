// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"errors"
	"strings"

	"github.com/activeshadow/libminimega/minicli"

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
			if strings.Contains(column, "host") {
				res["host"] = resp.Host

				continue
			}

			for i, header := range resp.Header {
				if strings.Contains(column, header) {
					res[header] = row[i]
				}
			}
		}

		return res
	}
}

// RunTabularErr is used to run the given command when the response is expected
// to be in tabular form. It returns a slice of maps (each map a row, keyed by
// column) along with the FIRST error encountered in any response. Rows that
// were parsed before the error are still returned so the caller can decide how
// to treat partial data.
//
// Callers that need to distinguish a transient failure (e.g. a mesh node
// hiccup) from a genuinely empty result should use this instead of RunTabular,
// which discards errors.
func RunTabularErr(cmd *Command) ([]map[string]string, error) {
	// copy all fields in header order
	mapper := tabularToMap

	if len(cmd.Columns) > 0 {
		// replace mapper to only copy fields in column order
		mapper = tabularToMapCols(cmd.Columns)
	}

	res := []map[string]string{}

	var firstErr error

	for resps := range Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				if firstErr == nil {
					firstErr = errors.New(resp.Error)
				}

				continue
			}

			for _, row := range resp.Tabular {
				res = append(res, mapper(resp, row))
			}
		}
	}

	return res, firstErr
}

// RunTabular is used to run the given command when the response is expected to
// be in tabular form. A slice of maps is returned, with each map representing a
// row in the tabular response and each map key representing the column. Any
// error encountered is logged and discarded; callers that need to act on the
// error should use RunTabularErr.
func RunTabular(cmd *Command) []map[string]string {
	rows, err := RunTabularErr(cmd)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"error running mm cmd",
			"cmd",
			cmd.Command,
			"error",
			err.Error(),
		)
	}

	return rows
}
