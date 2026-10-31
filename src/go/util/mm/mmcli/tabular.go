// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"context"
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

// RunTabular is used to run the given command when the response is expected to
// be in tabular form. A slice of maps is returned, with each map representing a
// row in the tabular response and each map key representing the column.
func RunTabular(cmd *Command) []map[string]string {
	return RunTabularStartedContext(context.Background(), cmd, nil)
}

// RunTabularContext is RunTabular for a command run on behalf of something
// that may end first; see RunContext.
func RunTabularContext(ctx context.Context, cmd *Command) []map[string]string {
	return RunTabularStartedContext(ctx, cmd, nil)
}

// RunTabularStartedContext is RunTabularContext, calling started as RunStartedContext does. Response
// errors are logged (at debug level once ctx is done) and their rows left out; see RunTabularCheckedContext.
func RunTabularStartedContext(ctx context.Context, cmd *Command, started func()) []map[string]string {
	res, err := RunTabularCheckedContext(ctx, cmd, started)

	switch {
	case err == nil:
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		plog.Debug(plog.TypeSystem, "mm cmd canceled", "cmd", cmd.Command, "error", err)
	default:
		plog.Error(
			plog.TypeSystem,
			"error running mm cmd",
			"cmd",
			cmd.Command,
			"error",
			err,
		)
	}

	return res
}

// RunTabularCheckedContext is RunTabularStartedContext, also returning the response errors, joined;
// the rows, from the responses without an error, may then be partial.
func RunTabularCheckedContext(
	ctx context.Context,
	cmd *Command,
	started func(),
) ([]map[string]string, error) {
	// copy all fields in header order
	mapper := tabularToMap

	if len(cmd.Columns) > 0 {
		// replace mapper to only copy fields in column order
		mapper = tabularToMapCols(cmd.Columns)
	}

	var (
		res  = []map[string]string{}
		errs []error
	)

	for resps := range RunStartedContext(ctx, cmd, started) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				errs = append(errs, reconstructErr(resp.Error))

				continue
			}

			for _, row := range resp.Tabular {
				res = append(res, mapper(resp, row))
			}
		}
	}

	return res, errors.Join(errs...)
}
