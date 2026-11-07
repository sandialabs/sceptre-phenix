package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/types"
	"phenix/util/mm"
)

// sohMaxProblems is the most failing checks and down VMs that phenix experiment soh lists for each experiment, as
// GET /soh does.
const sohMaxProblems = 50

// sohUnknown says that minimega could not tell the VM counts.
const sohUnknown = "unknown"

// sohVMStates returns minimega's VM states for a running experiment, or nil for a stopped one or a dry run: asking
// minimega about a stopped experiment would make its namespace again.
func sohVMStates(ctx context.Context, exp *types.Experiment) (map[string]string, error) {
	if !exp.Running() || exp.DryRun() {
		return nil, nil //nolint:nilnil // a stopped experiment has no VM states, as soh.Summarize takes them
	}

	states, err := mm.GetVMStates(mm.NS(exp.Metadata.Name), mm.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("getting VM states: %w", err)
	}

	return states, nil
}

// sohSummary summarizes the experiment's state of health as GET /soh does. A problem that leaves part of it out is
// in the summary's Error or VMsError.
func sohSummary(ctx context.Context, exp *types.Experiment) soh.Summary {
	states, vmErr := sohVMStates(ctx, exp)

	summary, err := soh.Summarize(exp, states, sohMaxProblems)
	if err != nil {
		summary.Error = err.Error()
	}

	if vmErr != nil {
		summary.VMsError = vmErr.Error()
	}

	return summary
}

// sohState is the state of an experiment's state of health app, in a few words.
func sohState(s soh.Summary) string {
	switch {
	case !s.Configured:
		return "not configured"
	case s.SOHDisabled:
		return "disabled"
	case !s.Running:
		return "experiment stopped"
	case s.SOHRunning:
		return "checks running"
	case s.SOHInitialized:
		return "initialized"
	default:
		return "not initialized"
	}
}

// sohTally is a tally of checks as passing/total, or "" when there are none.
func sohTally(t soh.Tally) string {
	if t.Total == 0 {
		return ""
	}

	return fmt.Sprintf("%d/%d", t.Passing, t.Total)
}

// sohVMs is the running and total VMs of a summary, or why minimega was not asked.
func sohVMs(s soh.Summary) string {
	switch {
	case s.VMsError != "":
		return sohUnknown + ": " + s.VMsError
	case !s.Running:
		return strconv.Itoa(s.VMs.Total)
	case !s.VMs.Known:
		return sohUnknown
	default:
		return fmt.Sprintf("%d/%d running", s.VMs.Running, s.VMs.Total)
	}
}

// printSoHSummaries writes the summaries as a table of one experiment on each row.
func printSoHSummaries(w io.Writer, summaries []soh.Summary) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"Experiment", "State of Health", "VMs", "Down VMs", "Checks", "Reachability", "Last Run", "Error"})

	for _, s := range summaries {
		table.Append([]string{
			s.Name, sohState(s), sohVMs(s), strconv.Itoa(len(s.DownVMs)), sohTally(s.Checks), sohTally(s.Reachability),
			s.LastRun, s.Error,
		})
	}

	table.Render()
}

// sohCheckErrors counts the checks of a host that failed in the latest run.
func sohCheckErrors(h *soh.HostState) int {
	if h == nil {
		return 0
	}

	var n int

	for _, s := range h.AllStates() {
		if s.Error != "" {
			n++
		}
	}

	return n
}

// printSoHNetwork writes the VMs of an experiment's state of health network, then its failing checks.
func printSoHNetwork(w io.Writer, summary soh.Summary, network *soh.Network) {
	fmt.Fprintf(w, "Experiment %s: state of health %s, VMs %s, checks %s\n",
		summary.Name, sohState(summary), sohVMs(summary), cmpOrNone(sohTally(summary.Checks)))

	if summary.Error != "" {
		fmt.Fprintf(w, "phenix cannot read all of the results: %s\n", summary.Error)
	}

	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"VM", "Status", "Image", "CPU Load", "Failing Checks"})

	for _, node := range network.Nodes {
		// the switch nodes stand for networks, not VMs
		if node.Status == "ignore" {
			continue
		}

		var load string
		if node.SOH != nil {
			load = node.SOH.CPULoad
		}

		table.Append([]string{node.Label, node.Status, node.Image, load, strconv.Itoa(sohCheckErrors(node.SOH))})
	}

	table.Render()

	if len(summary.FailingChecks) == 0 {
		return
	}

	checks := tablewriter.NewWriter(w)
	checks.SetAutoWrapText(false)
	checks.SetHeader([]string{"Host", "Check", "Target", "Error", "Time"})

	for _, c := range summary.FailingChecks {
		checks.Append([]string{c.Host, c.Check, c.Target, c.Error, c.Time})
	}

	checks.Render()
}

// cmpOrNone is s, or "none" when s is "".
func cmpOrNone(s string) string {
	if s == "" {
		return "none"
	}

	return s
}

// sohExperimentOutput is what phenix experiment soh <experiment name> -o json
// or -o yaml shows: the summary of the experiment, and its state of health
// network as GET /experiments/{name}/soh gives it.
type sohExperimentOutput struct {
	Summary soh.Summary  `json:"summary"`
	Network *soh.Network `json:"network"`
}

func newExperimentSoHCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "soh [<experiment name>]",
		Short: "Show the state of health of experiments",
		Long: `Show the state of health of experiments

  Without an experiment, shows a summary of each experiment on one row, as
  the State of Health page does: the state of the soh app, the running VMs,
  and the checks that passed in its latest run. With an experiment, shows
  each of its VMs with its status and failing checks, then the failing
  checks of the latest run. --status shows only the VMs with that status.`,
		Example: `
  # The VMs of exp1 that do not run
  phenix experiment soh exp1 --status notrunning

  # The summary of each experiment as JSON
  phenix experiment soh -o json`,
		Args:              argsWithUsage(cobra.MaximumNArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				ctx    = commandContext(cmd)
				status = mustGet(cmd.Flags().GetString, "status")
			)

			if err := soh.ValidateStatusFilter(status); err != nil {
				return fmt.Errorf("--status: %w", err)
			}

			if len(args) == 0 {
				if status != "" {
					return errors.New("--status needs an experiment name")
				}

				return printAllSoH(ctx, cmd, out)
			}

			exp, err := experiment.Get(args[0])
			if err != nil {
				return fmt.Errorf("unable to get experiment %s: %w", args[0], err)
			}

			network, err := soh.GetFor(ctx, exp, status)
			if err != nil {
				return fmt.Errorf("getting state of health of experiment %s: %w", args[0], err)
			}

			data := sohExperimentOutput{Summary: sohSummary(ctx, exp), Network: network}

			return out.printAPI(cmd, data, outputViews{
				formatTable: func(w io.Writer) error {
					printSoHNetwork(w, data.Summary, data.Network)

					return nil
				},
			})
		},
	}

	cmd.Flags().String("status", "", "only the VMs with this status ("+strings.Join(soh.StatusFilters(), ", ")+")")
	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	_ = cmd.RegisterFlagCompletionFunc("status", completeValues(soh.StatusFilters()...))

	return cmd
}

// printAllSoH writes the state of health summary of every experiment, sorted
// by name, in the output format of out.
func printAllSoH(ctx context.Context, cmd *cobra.Command, out *outputFlag) error {
	exps, err := experiment.List()
	if err != nil && len(exps) == 0 {
		return fmt.Errorf("listing experiments: %w", err)
	}

	summaries := make([]soh.Summary, 0, len(exps))

	for i := range exps {
		summaries = append(summaries, sohSummary(ctx, &exps[i]))
	}

	slices.SortFunc(summaries, func(a, b soh.Summary) int { return strings.Compare(a.Name, b.Name) })

	printErr := out.printAPI(cmd, summaries, outputViews{
		formatTable: func(w io.Writer) error {
			if len(summaries) == 0 {
				fmt.Fprintln(w, "No experiments")

				return nil
			}

			printSoHSummaries(w, summaries)

			return nil
		},
	})
	if printErr != nil {
		return printErr
	}

	// List still returns the experiments it could decode
	if err != nil {
		return fmt.Errorf("listing experiments: %w", err)
	}

	return nil
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("experiment").AddCommand(newExperimentSoHCmd())
}
