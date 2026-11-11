// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"context"
	"errors"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/plog"
)

// hostColumn is the key of the answering minimega node in a mapped row.
const hostColumn = "host"

type tabularToMapper func(*minicli.Response, []string) map[string]string

func tabularToMap(resp *minicli.Response, row []string) map[string]string {
	res := map[string]string{
		hostColumn: resp.Host,
	}

	// a short row (from a misbehaving minimega) leaves its missing cells out
	for i, header := range resp.Header[:min(len(resp.Header), len(row))] {
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
			if column == hostColumn {
				res[hostColumn] = resp.Host

				continue
			}

			for i, header := range resp.Header[:min(len(resp.Header), len(row))] {
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

	logTabularErr(ctx, cmd, err)

	return res
}

// RunTabularCheckedContext is RunTabularStartedContext, also returning the response errors, joined;
// the rows, from the responses without an error, may then be partial.
func RunTabularCheckedContext(
	ctx context.Context,
	cmd *Command,
	started func(),
) ([]map[string]string, error) {
	return tabular(cmd, RunStartedContext(ctx, cmd, started))
}

// RunTabularReadOnly is RunTabularContext on RunReadOnly's connections, in
// the wait line named by line.
func RunTabularReadOnly(ctx context.Context, line ReadLine, cmd *Command) []map[string]string {
	return RunTabularReadOnlyStarted(ctx, line, cmd, nil)
}

// RunTabularReadOnlyStarted is RunTabularStartedContext on RunReadOnly's
// connections, in the wait line named by line.
func RunTabularReadOnlyStarted(
	ctx context.Context,
	line ReadLine,
	cmd *Command,
	started func(),
) []map[string]string {
	res, err := RunTabularReadOnlyChecked(ctx, line, cmd, started)

	logTabularErr(ctx, cmd, err)

	return res
}

// RunTabularReadOnlyChecked is RunTabularCheckedContext on RunReadOnly's
// connections, in the wait line named by line.
func RunTabularReadOnlyChecked(
	ctx context.Context,
	line ReadLine,
	cmd *Command,
	started func(),
) ([]map[string]string, error) {
	return tabular(cmd, readOnlyPool.run(ctx, line, cmd, started))
}

// logTabularErr logs the response errors of a tabular command, at debug level
// once ctx is done.
func logTabularErr(ctx context.Context, cmd *Command, err error) {
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
}

// tabular drains responses, the answer to cmd, into rows, joining the
// response errors.
func tabular(cmd *Command, responses chan *miniclient.Response) ([]map[string]string, error) {
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

	for resps := range responses {
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
