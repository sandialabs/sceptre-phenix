package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/util/common"
	"phenix/util/sigterm"
	"phenix/web"
)

const (
	// logsHeld completes the error of a logs command when phenix ui does not answer.
	logsHeld = "the log entries and log files are read by the running server"

	defaultLogsSince = time.Hour
)

// logFileNames are the log files that phenix logs download takes, as GET /logs/files/{name} does.
var logFileNames = []string{"phenix", "minimega"} //nolint:gochecknoglobals // constant list

var (
	errLogsRange      = errors.New("invalid log time range")
	errUnknownLogFile = errors.New("unknown log file")
)

// logRange returns the start and end of the log entries that phenix logs asks for: start or, when start is "",
// since before now; and end, or the zero time when end is "" (the server then ends 30 seconds after now). start and
// end are RFC 3339 times, and since is a positive duration. start and since cannot both be given, and end must not be
// before start.
func logRange(start, end string, since time.Duration, sinceSet bool, now time.Time) (time.Time, time.Time, error) {
	var from, to time.Time

	switch {
	case start != "" && sinceSet:
		return from, to, fmt.Errorf("%w: give --start or --since, not both", errLogsRange)
	case start != "":
		t, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return from, to, fmt.Errorf("%w: --start %q is not an RFC 3339 time, such as 2026-11-06T15:04:05Z", errLogsRange, start)
		}

		from = t
	case since <= 0:
		return from, to, fmt.Errorf("%w: --since %s must be more than 0", errLogsRange, since)
	default:
		from = now.Add(-since)
	}

	if end != "" {
		t, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return from, to, fmt.Errorf("%w: --end %q is not an RFC 3339 time, such as 2026-11-06T15:04:05Z", errLogsRange, end)
		}

		if t.Before(from) {
			return from, to, fmt.Errorf("%w: --end %s is before the start %s", errLogsRange, end, from.Format(time.RFC3339))
		}

		to = t
	}

	return from, to, nil
}

// logEntry is one log entry as GET /logs answers it.
type logEntry struct {
	Time      int64  `json:"time"      yaml:"time"`
	Timestamp string `json:"timestamp" yaml:"timestamp"`
	Level     string `json:"level"     yaml:"level"`
	Message   string `json:"msg"       yaml:"msg"`
	Type      string `json:"type"      yaml:"type"`
}

// newLogsClient returns the client of the log routes of phenix ui.
func newLogsClient() *serverClient {
	return newServerClient(common.UnixSocket, logsHeld)
}

func newLogsCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the log entries of the phenix server",
		Long: `Show the log entries of the phenix server

  Prints the entries of the phenix ui server's system log in a time range,
  oldest first, one on each line, as the Logs page does: by default those of
  the last hour. The server reads its log files, so the command calls it
  over its unix socket (--unix-socket). -o json and -o yaml print the
  entries with the fields of GET /logs. phenix logs files and phenix logs
  download get the whole phenix and minimega log files.`,
		Example: `
  # The entries of the last 10 minutes
  phenix logs --since 10m

  # The entries of a time range
  phenix logs --start 2026-11-06T08:00:00Z --end 2026-11-06T09:00:00Z`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := logRange(
				mustGet(cmd.Flags().GetString, "start"),
				mustGet(cmd.Flags().GetString, "end"),
				mustGet(cmd.Flags().GetDuration, "since"),
				cmd.Flags().Changed("since"),
				time.Now(),
			)
			if err != nil {
				return err
			}

			query := url.Values{"start": {from.Format(time.RFC3339)}}
			if !to.IsZero() {
				query.Set("end", to.Format(time.RFC3339))
			}

			var entries []logEntry

			if err := newLogsClient().getJSON(commandContext(cmd), "/api/v1/logs", query, &entries); err != nil {
				return fmt.Errorf("getting log entries: %w", err)
			}

			return out.printAPI(cmd, entries, outputViews{
				formatTable: func(w io.Writer) error {
					for _, e := range entries {
						fmt.Fprintf(w, "%s %-5s %-9s %s\n", e.Timestamp, e.Level, e.Type, strings.TrimSpace(e.Message))
					}

					return nil
				},
			})
		},
	}

	cmd.Flags().String("start", "", "first time to show, in RFC 3339 format (default: --since before now)")
	cmd.Flags().String("end", "", "last time to show, in RFC 3339 format (default: 30 seconds after now)")
	cmd.Flags().Duration("since", defaultLogsSince, "show the entries of this time before now")

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	cmd.AddCommand(newLogsFilesCmd(), newLogsDownloadCmd())

	return cmd
}

func newLogsFilesCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Display a table of the log files to download",
		Long: `Display a table of the log files to download

  Shows the phenix system log (log.system.path) and the minimega log
  (ui.logs.minimega-path) of the phenix ui server, and whether the server can
  read each of them now, or why not. -o json and -o yaml print the files
  with the fields of GET /logs/files.`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var body struct {
				Files []web.LogFile `json:"files"`
			}

			if err := newLogsClient().getJSON(commandContext(cmd), "/api/v1/logs/files", nil, &body); err != nil {
				return fmt.Errorf("listing log files: %w", err)
			}

			return out.printAPI(cmd, body.Files, outputViews{
				formatTable: func(w io.Writer) error {
					printLogFiles(w, body.Files)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

// printLogFiles writes the log files as a table.
func printLogFiles(w io.Writer, files []web.LogFile) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"Name", "Available", "Size", "Modified", "Reason"})

	for _, f := range files {
		var size, modified string

		if f.Size != nil {
			size = doctorBytes(*f.Size)
		}

		if f.Modified != nil {
			modified = f.Modified.Format(time.RFC3339)
		}

		table.Append([]string{f.Name, strconv.FormatBool(f.Available), size, modified, f.Reason})
	}

	table.Render()
}

func newLogsDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "download <phenix|minimega>",
		Short:     "Write a log file of the phenix server to stdout or to a local file",
		ValidArgs: logFileNames,
		Example: `
  phenix logs download minimega -O minimega.log`,
		Args: argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if !slices.Contains(logFileNames, name) {
				return fmt.Errorf("%w %q: use %s", errUnknownLogFile, name, strings.Join(logFileNames, " or "))
			}

			ctx := sigterm.CancelContext(commandContext(cmd))

			return writeOutput(cmd, mustGet(cmd.Flags().GetString, outputFileFlag), func(w io.Writer) error {
				if err := newLogsClient().download(ctx, "/api/v1/logs/files/"+url.PathEscape(name), nil, w); err != nil {
					return fmt.Errorf("downloading the %s log file: %w", name, err)
				}

				return nil
			})
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "write the log file to this path instead of stdout")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newLogsCmd(), true)
}
