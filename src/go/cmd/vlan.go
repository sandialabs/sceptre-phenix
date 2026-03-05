package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

const aliasArgs = 3

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
	cmd := &cobra.Command{
		Use:   "alias <experiment name> <alias name> <vlan id>",
		Short: "View or set an alias for a given VLAN ID",
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

	rootCmd.AddCommand(vlanCmd)
}
