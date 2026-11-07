package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

// cliView sees everything: the CLI acts with the store's and minimega's access.
func cliView() vlan.View {
	return vlan.View{Visible: func(string) bool { return true }, All: true}
}

// errBadVLANID is returned for a VLAN ID argument that is not one.
var errBadVLANID = errors.New("not a VLAN ID")

// parseCLIVLANID parses a VLAN ID argument.
func parseCLIVLANID(arg string) (int, error) {
	id, err := strconv.Atoi(arg)
	if err != nil || !vlan.ValidID(id) {
		return 0, fmt.Errorf("%w: %q is not a whole number in %d–%d", errBadVLANID, arg, vlan.MinID, vlan.MaxID)
	}

	return id, nil
}

// optionalID prints a source's ID, or "-" for none.
func optionalID(id *int) string {
	if id == nil {
		return "-"
	}

	return strconv.Itoa(*id)
}

// printVLANReport writes the rows and issues of a merged VLAN report to w.
func printVLANReport(w io.Writer, rep vlan.Report) {
	if !rep.Minimega.OK {
		fmt.Fprintf(w, "minimega did not answer (%s): the table shows the phēnix side only.\n", rep.Minimega.Error)
	}

	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"VLAN ID", "Alias", "Experiment", "Namespace", "States", "phēnix", "minimega", "VMs", "Issues"})

	for _, row := range rep.VLANs {
		table.Append([]string{
			strconv.Itoa(row.ID), row.Alias, row.Experiment, row.Namespace, strings.Join(row.States, ", "),
			optionalID(row.Sources.Phenix), optionalID(row.Sources.Minimega), strconv.Itoa(row.VMs),
			strings.Join(row.Issues, ", "),
		})
	}

	table.Render()

	if len(rep.Issues) == 0 {
		return
	}

	printVLANIssues(w, rep.Issues)
}

// printVLANIssues writes a table of the VLAN issues to w.
func printVLANIssues(w io.Writer, list []vlan.Issue) {
	issues := tablewriter.NewWriter(w)
	issues.SetAutoWrapText(false)
	issues.SetHeader([]string{"Severity", "Kind", "Subject", "Message"})

	for _, i := range list {
		subject := i.Namespace
		if len(i.Experiments) > 0 {
			subject = i.Experiments[0]
		}

		issues.Append([]string{i.Severity, i.Kind, subject, i.Message})
	}

	issues.Render()
}

// errBadVLANState is returned for a --state value that is not a VLAN state.
var errBadVLANState = errors.New("not a VLAN state")

// parseVLANState checks a --state value of phenix vlan list: empty for any
// state, or pinned, allocated or blacklisted.
func parseVLANState(value string) (string, error) {
	switch value {
	case "", vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted:
		return value, nil
	}

	return "", fmt.Errorf("%w: %q is not %s, %s or %s",
		errBadVLANState, value, vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted)
}

func newVlanListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the VLAN IDs of the cluster",
		Long: `List the VLAN IDs of the cluster

  Merges the VLAN pins and ranges of every experiment with the live aliases
  and the blacklist of minimega, one row for each owner, alias and VLAN ID.
  A second table lists the differences and conflicts.

  -o json and -o yaml print the report of GET /vlans. -o csv prints the rows
  as GET /vlans?format=csv does: one line for each row, then, when no
  --vlan, --state or --issues is given, one line with no ID for each alias
  that has no pin and no live ID.`,
		Example: `
  phenix vlan list
  phenix vlan list --experiment substation-a
  phenix vlan list --vlan 101
  phenix vlan list --state blacklisted
  phenix vlan list --issues
  phenix vlan list -o csv > vlans.csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := vlan.Query{
				Experiment: mustGet(cmd.Flags().GetString, "experiment"), VLAN: 0, State: "",
				IssuesOnly: mustGet(cmd.Flags().GetBool, "issues"),
			}

			if raw := mustGet(cmd.Flags().GetString, "vlan"); raw != "" {
				id, err := parseCLIVLANID(raw)
				if err != nil {
					return err
				}

				q.VLAN = id
			}

			state, err := parseVLANState(mustGet(cmd.Flags().GetString, "state"))
			if err != nil {
				return err
			}

			q.State = state

			in, _, err := vlan.Read(context.Background())
			if err != nil {
				return util.HumanizeError(err, "Unable to read the VLANs").Humanized()
			}

			rep := vlan.Filter(vlan.Merge(in, cliView()), q)

			return out.printAPI(cmd, rep, outputViews{
				formatTable: func(w io.Writer) error {
					printVLANReport(w, rep)

					return nil
				},
				formatCSV: func(w io.Writer) error { return vlan.WriteCSV(w, rep, q) }, //nolint:wrapcheck // names the CSV
			})
		},
	}

	cmd.Flags().StringP("experiment", "e", "", "Show only this experiment or minimega namespace")
	cmd.Flags().Bool("issues", false, "Show only the rows with an issue")
	cmd.Flags().String("vlan", "", "Show only the rows of this VLAN ID")
	cmd.Flags().String("state", "", "Show only the rows in this state: pinned, allocated or blacklisted")

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML, formatCSV})

	_ = cmd.RegisterFlagCompletionFunc("experiment", completeVLANOwners)
	_ = cmd.RegisterFlagCompletionFunc("state", completeValues(vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted))

	return cmd
}

// vlanBlacklistBody is the minimega VLAN blacklist, as GET /vlans/blacklist
// gives it.
type vlanBlacklistBody struct {
	VLANs     []int `json:"vlans"     yaml:"vlans"`
	Removable bool  `json:"removable" yaml:"removable"`
}

func newVlanBlacklistCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "blacklist",
		Short: "View or change the minimega VLAN blacklist",
		Long: `View or change the minimega VLAN blacklist

  With no subcommand, lists the VLAN IDs that minimega does not give to any
  alias, one on each line. -o json and -o yaml print the body of
  GET /vlans/blacklist.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ids, removable, err := vlan.Blacklist(context.Background())
			if err != nil {
				return util.HumanizeError(err, "Unable to read the VLAN blacklist").Humanized()
			}

			body := vlanBlacklistBody{VLANs: nonNil(ids), Removable: removable}

			return out.printAPI(cmd, body, outputViews{
				formatTable: func(w io.Writer) error {
					for _, id := range ids {
						fmt.Fprintln(w, id)
					}

					if !removable {
						fmt.Fprintln(cmd.ErrOrStderr(), "This minimega cannot remove a VLAN ID from its blacklist.")
					}

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	add := &cobra.Command{
		Use:   "add <vlan id>",
		Short: "Add a VLAN ID to the minimega blacklist",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := parseCLIVLANID(args[0])
			if err != nil {
				return err
			}

			if err := vlan.AddToBlacklist(context.Background(), id); err != nil {
				return vlanRefusal(err, "Unable to blacklist VLAN "+args[0])
			}

			plog.Info(plog.TypeSystem, "VLAN blacklisted", "vlan", id)

			return nil
		},
	}

	var removeDry dryRun

	remove := &cobra.Command{
		Use:   "remove <vlan id>",
		Short: "Remove a VLAN ID from the minimega blacklist",
		Long: `Remove a VLAN ID from the minimega blacklist

  Needs a minimega with the command "clear vlans blacklist <vlan>".`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeArgs(completeBlacklistedVLANs),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseCLIVLANID(args[0])
			if err != nil {
				return err
			}

			if removeDry.enabled() {
				if err := vlan.CheckRemoveFromBlacklist(commandContext(cmd), id); err != nil {
					return vlanRefusal(err, "Unable to remove VLAN "+args[0]+" from the blacklist")
				}

				removeDry.skip("remove", fmt.Sprintf("VLAN %d from the minimega blacklist", id))

				return nil
			}

			if err := vlan.RemoveFromBlacklist(context.Background(), id); err != nil {
				return vlanRefusal(err, "Unable to remove VLAN "+args[0]+" from the blacklist")
			}

			plog.Info(plog.TypeSystem, "VLAN removed from the blacklist", "vlan", id)

			return nil
		},
	}

	removeDry = addDryRunFlag(remove)

	cmd.AddCommand(add, remove)

	return cmd
}

// completeBlacklistedVLANs completes the VLAN IDs on the minimega blacklist.
func completeBlacklistedVLANs(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	ids, _, err := vlan.Blacklist(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strconv.Itoa(id))
	}

	return completionMatches(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeVLANOwners completes the experiments, and the minimega namespaces
// that minimega reports.
func completeVLANOwners(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	in, _, err := vlan.Read(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(in.Experiments)+len(in.Minimega.Namespaces))
	for _, e := range in.Experiments {
		names = append(names, e.Name)
	}

	names = append(names, slices.Sorted(maps.Keys(in.Minimega.Namespaces))...)

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeUnownedNamespaces completes the minimega namespaces that no
// experiment has, which phenix vlan release takes.
func completeUnownedNamespaces(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	in, _, err := vlan.Read(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string

	for _, ns := range slices.Sorted(maps.Keys(in.Minimega.Namespaces)) {
		owned := slices.ContainsFunc(in.Experiments, func(e vlan.ExperimentInput) bool { return e.Name == ns })

		if !owned {
			names = append(names, ns)
		}
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// vlanRefusal returns a refusal as it is, since its message is the answer,
// and humanizes any other error.
func vlanRefusal(err error, msg string) error {
	for _, refusal := range []error{
		vlan.ErrInvalid, vlan.ErrNotFound, vlan.ErrInUse, vlan.ErrUnsupported, vlan.ErrRunning, vlan.ErrNotRunning,
	} {
		if errors.Is(err, refusal) {
			return err
		}
	}

	return util.HumanizeError(err, "%s", msg).Humanized()
}

func newVlanReleaseCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "release <namespace>",
		Short: "Release the VLAN IDs of a minimega namespace that no experiment has",
		Long: `Release the VLAN IDs of a minimega namespace that no experiment has

  Clears the VLAN aliases of the namespace, so minimega can give the IDs to
  other experiments. It refuses a namespace that an experiment has, or whose
  VMs are on its VLAN IDs.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeArgs(completeUnownedNamespaces),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dry.enabled() {
				if err := vlan.CheckRelease(commandContext(cmd), args[0]); err != nil {
					return vlanRefusal(err, "Unable to release the VLANs of namespace "+args[0])
				}

				dry.skip("release", "the VLAN IDs of namespace "+args[0])

				return nil
			}

			if err := vlan.Release(context.Background(), args[0]); err != nil {
				return vlanRefusal(err, "Unable to release the VLANs of namespace "+args[0])
			}

			plog.Info(plog.TypeSystem, "VLANs released", "namespace", args[0])

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func newVlanPinLiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin-live <experiment name>",
		Short: "Pin the live VLAN IDs of a running experiment",
		Long: `Pin the live VLAN IDs of a running experiment

  Copies the VLAN IDs that minimega gave the aliases of a running experiment
  into its spec, so the next start gives the same IDs.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false)),
		RunE: func(cmd *cobra.Command, args []string) error {
			live, err := vlan.PinLive(context.Background(), args[0])
			if err != nil {
				return vlanRefusal(err, "Unable to pin the live VLANs of the "+args[0]+" experiment")
			}

			printer.PrintTableOfVLANAliases(cmd.OutOrStdout(), map[string]map[string]int{args[0]: live})

			return nil
		},
	}
}
