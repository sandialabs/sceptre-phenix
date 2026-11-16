package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/util/common"
	"phenix/util/sigterm"
)

const netflowDownloadArgs = 2

// netflowHeld completes the error of a netflow command when phenix ui does not answer.
const netflowHeld = "netflow captures run only in the running server"

// netflowPath is the API path of the experiment's netflow route, with suffix after it.
func netflowPath(exp, suffix string) string {
	return "/api/v1/experiments/" + url.PathEscape(exp) + "/netflow" + suffix
}

// newNetflowClient returns the client of the netflow routes of phenix ui.
func newNetflowClient() *serverClient {
	return newServerClient(common.UnixSocket, netflowHeld)
}

func newExperimentNetflowCmd() *cobra.Command {
	desc := `Netflow capture management

  Starts, stops and checks the netflow capture of a running experiment, and
  lists and downloads its recorded captures, as the Netflow tab of the
  experiment page does. The capture runs in the phenix ui server, so these
  commands call that server over its unix socket (--unix-socket), which acts
  as Global Admin. The server records each capture up to the size that
  ui.netflow.max-size sets.`

	cmd := &cobra.Command{
		Use:   "netflow",
		Short: "Netflow capture management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newNetflowActionCmd("start", "Start the netflow capture of a running experiment", http.MethodPost, "starting", "Started"),
		newNetflowActionCmd("stop", "Stop the netflow capture of an experiment", http.MethodDelete, "stopping", "Stopped"),
		newNetflowStatusCmd(),
		newNetflowCapturesCmd(),
		newNetflowDownloadCmd(),
	)

	return cmd
}

// newNetflowActionCmd returns a command that sends method to the experiment's netflow route, and reports done. verb
// names the action in its error.
func newNetflowActionCmd(use, short, method, verb, done string) *cobra.Command {
	return &cobra.Command{
		Use:               use + " <experiment name>",
		Short:             short,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newNetflowClient().send(commandContext(cmd), method, netflowPath(args[0], "")); err != nil {
				return fmt.Errorf("%s netflow capture of experiment %s: %w", verb, args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s the netflow capture of experiment %s\n", done, args[0])

			return nil
		},
	}
}

// netflowStatus is what phenix experiment netflow status shows.
type netflowStatus struct {
	Experiment string `json:"experiment" yaml:"experiment"`
	Running    bool   `json:"running"    yaml:"running"`
}

func newNetflowStatusCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:               "status <experiment name>",
		Short:             "Tell whether the netflow capture of an experiment runs",
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := newNetflowClient().send(commandContext(cmd), http.MethodGet, netflowPath(args[0], ""))

			status := netflowStatus{Experiment: args[0], Running: err == nil}
			if err != nil && apiStatus(err) != http.StatusNotFound {
				return fmt.Errorf("getting netflow capture of experiment %s: %w", args[0], err)
			}

			return out.print(cmd, status, outputViews{
				formatText: func(w io.Writer) error {
					state := "running"
					if !status.Running {
						state = "not running"
					}

					_, err := fmt.Fprintf(w, "The netflow capture of experiment %s is %s\n", status.Experiment, state)

					return err //nolint:wrapcheck // the writer's own error
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})

	return cmd
}

// listNetflowCaptures returns the recorded netflow captures of experiment
// exp, which phenix ui keeps.
func listNetflowCaptures(ctx context.Context, exp string) ([]experiment.NetflowCapture, error) {
	var body struct {
		Captures []experiment.NetflowCapture `json:"captures"`
	}

	if err := newNetflowClient().getJSON(ctx, netflowPath(exp, "/captures"), nil, &body); err != nil {
		return nil, err
	}

	return body.Captures, nil
}

func newNetflowCapturesCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "captures <experiment name>",
		Short: "Display a table of the recorded netflow captures of an experiment",
		Long: `Display a table of the recorded netflow captures of an experiment

  Give the ID of a capture to phenix experiment netflow download. A capture
  that the server did not finish has the state interrupted.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			captures, err := listNetflowCaptures(commandContext(cmd), args[0])
			if err != nil {
				return fmt.Errorf("listing netflow captures of experiment %s: %w", args[0], err)
			}

			return out.printAPI(cmd, captures, outputViews{
				formatTable: func(w io.Writer) error {
					if len(captures) == 0 {
						fmt.Fprintln(w, "No netflow captures")

						return nil
					}

					printNetflowCaptures(w, captures)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	return cmd
}

// printNetflowCaptures writes the captures as a table.
func printNetflowCaptures(w io.Writer, captures []experiment.NetflowCapture) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"ID", "State", "Started", "Stopped", "Size", "Flows", "Truncated"})

	for _, c := range captures {
		var stopped, flows string

		if c.Stopped != nil {
			stopped = c.Stopped.Format(time.RFC3339)
		}

		if c.Flows != nil {
			flows = strconv.FormatInt(*c.Flows, 10)
		}

		table.Append([]string{
			c.ID, c.State, c.Started.Format(time.RFC3339), stopped, doctorBytes(c.Size), flows, strconv.FormatBool(c.Truncated),
		})
	}

	table.Render()
}

func newNetflowDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download <experiment name> <capture ID>",
		Short: "Write a recorded netflow capture to stdout or to a local file",
		Long: `Write a recorded netflow capture to stdout or to a local file

  --format text writes the lines as minimega sent them. csv and jsonl convert
  each flow, and skip lines that do not parse. A capture that still records
  is written as it was when the download started.`,
		Example: `
  phenix experiment netflow download exp1 20261028T153012.345Z --format csv -O flows.csv`,
		Args:              argsWithUsage(cobra.ExactArgs(netflowDownloadArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expAny, false), completeNetflowCaptureIDs),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				exp, id = args[0], args[1]
				format  = mustGet(cmd.Flags().GetString, "format")
			)

			if err := experiment.ValidateNetflowFormat(format); err != nil {
				return fmt.Errorf("--format: %w", err)
			}

			if err := experiment.ValidateNetflowCaptureID(id); err != nil {
				return err //nolint:wrapcheck // says what is wrong with the argument
			}

			ctx := sigterm.CancelContext(commandContext(cmd))

			return writeOutput(cmd, mustGet(cmd.Flags().GetString, outputFileFlag), func(w io.Writer) error {
				err := newNetflowClient().download(
					ctx, netflowPath(exp, "/captures/"+url.PathEscape(id)), url.Values{"format": {format}}, w,
				)
				if err != nil {
					return fmt.Errorf("downloading netflow capture %s of experiment %s: %w", id, exp, err)
				}

				return nil
			})
		},
	}

	cmd.Flags().String("format", experiment.NetflowFormatText,
		"format to write ("+strings.Join(experiment.NetflowFormats(), ", ")+")")
	cmd.Flags().StringP(outputFileFlag, "O", "", "write the capture to this path instead of stdout")

	_ = cmd.RegisterFlagCompletionFunc("format", completeValues(experiment.NetflowFormats()...))
	_ = cmd.MarkFlagFilename(outputFileFlag)

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("experiment").AddCommand(newExperimentNetflowCmd())
}
