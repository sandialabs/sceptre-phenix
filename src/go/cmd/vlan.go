package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

const (
	aliasArgs       = 3
	aliasLookupArgs = 2
)

func newVlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vlan",
		Short: "Used to manage VLANs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newVlanAliasCmd() *cobra.Command {
	desc := `View or set VLAN aliases

  With no arguments, lists the VLAN aliases of every experiment. With an
  experiment name, lists that experiment's VLAN aliases. With an experiment
  name and an alias name, prints the VLAN ID of that alias. With a VLAN ID as
  well, sets the alias to that VLAN ID.`

	example := `
  phenix vlan alias
  phenix vlan alias <experiment name>
  phenix vlan alias <experiment name> <alias name>
  phenix vlan alias <experiment name> <alias name> <vlan id>`

	cmd := &cobra.Command{
		Use:               "alias [experiment name] [alias name] [vlan id]",
		Short:             "View or set VLAN aliases",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: vlanAliasArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				info, err := vlan.Aliases()
				if err != nil {
					err := util.HumanizeError(err, "Unable to display all aliases")

					return err.Humanized()
				}

				printer.PrintTableOfVLANAliases(os.Stdout, info)
			case 1:
				exp := args[0]

				info, err := vlan.Aliases(vlan.Experiment(exp))
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to display aliases for the "+exp+" experiment",
					)

					return err.Humanized()
				}

				printer.PrintTableOfVLANAliases(os.Stdout, info)
			case aliasLookupArgs:
				return printVLANAliasID(cmd.OutOrStdout(), args[0], args[1])
			case aliasArgs:
				var (
					exp   = args[0]
					alias = args[1]
					id    = args[2]
					force = MustGetBool(cmd.Flags(), "force")
				)

				vid, err := strconv.Atoi(id)
				if err != nil {
					return errors.New("the VLAN identifier provided is not a valid integer")
				}

				if err := vlan.SetAlias(
					vlan.Experiment(exp),
					vlan.Alias(alias),
					vlan.ID(vid),
					vlan.Force(force),
				); err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to set the alias for the "+exp+" experiment",
					)

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "vlan alias set", "alias", alias, "exp", exp)
			default:
				return errors.New("there were an unexpected number of arguments provided")
			}

			return nil
		},
	}

	cmd.Flags().BoolP("force", "f", false, "Force update on set action if alias already exists")

	return cmd
}

// printVLANAliasID writes the VLAN ID of the given alias in the given
// experiment to w. A missing or unassigned alias is reported as is, since its
// message is the answer the user asked for.
func printVLANAliasID(w io.Writer, exp, alias string) error {
	id, err := vlan.AliasID(vlan.Experiment(exp), vlan.Alias(alias))
	if err != nil {
		if errors.Is(err, vlan.ErrAliasNotFound) || errors.Is(err, vlan.ErrAliasUnassigned) {
			return err
		}

		err := util.HumanizeError(
			err,
			"%s",
			"Unable to get VLAN alias "+alias+" for the "+exp+" experiment",
		)

		return err.Humanized()
	}

	fmt.Fprintln(w, id)

	return nil
}

// vlanAliasArgsCompletion completes the experiment name, then the names of
// that experiment's VLAN aliases.
func vlanAliasArgsCompletion(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return expNameCompletion(false)(cmd, args, toComplete)
	case 1:
		info, err := vlan.Aliases(vlan.Experiment(args[0]))
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		var aliases []string

		for alias := range info[args[0]] {
			if strings.HasPrefix(alias, toComplete) {
				aliases = append(aliases, alias)
			}
		}

		sort.Strings(aliases)

		return aliases, cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func newVlanRangeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "range <experiment name> <range minimum> <range maximum>",
		Short: "View or set a range for a give VLAN",
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				info, err := vlan.Ranges()
				if err != nil {
					err := util.HumanizeError(err, "Unable to display VLAN range(s)")

					return err.Humanized()
				}

				printer.PrintTableOfVLANRanges(os.Stdout, info)
			case 1:
				exp := args[0]

				info, err := vlan.Ranges(vlan.Experiment(exp))
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to display VLAN range(s) for "+exp+" experiment",
					)

					return err.Humanized()
				}

				printer.PrintTableOfVLANRanges(os.Stdout, info)
			case aliasArgs:
				var (
					exp    = args[0]
					minVal = args[1]
					maxVal = args[2]
					force  = MustGetBool(cmd.Flags(), "force")
				)

				vmin, err := strconv.Atoi(minVal)
				if err != nil {
					return errors.New(
						"the VLAN range minimum identifier provided is not a valid integer",
					)
				}

				vmax, err := strconv.Atoi(maxVal)
				if err != nil {
					return errors.New(
						"the VLAN range maximum identifier provided is not a valid integer",
					)
				}

				if err := vlan.SetRange(
					vlan.Experiment(exp),
					vlan.Min(vmin),
					vlan.Max(vmax),
					vlan.Force(force),
				); err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to set the VLAN range for the "+exp+" experiment",
					)

					return fmt.Errorf(err.Humanize(), 1)
				}

				plog.Info(plog.TypeSystem, "vlan range set", "exp", exp)
			default:
				return errors.New("there were an unexpected number of arguments provided")
			}

			return nil
		},
	}

	cmd.Flags().BoolP("force", "f", false, "Force update on set action if alias already exists")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	vlanCmd := newVlanCmd()

	vlanCmd.AddCommand(newVlanAliasCmd())
	vlanCmd.AddCommand(newVlanRangeCmd())

	addCommandToRoot(vlanCmd, true)
}
