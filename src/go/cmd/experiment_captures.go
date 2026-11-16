package cmd

import (
	"io"

	"github.com/spf13/cobra"

	"phenix/util/printer"
)

func newExperimentCapturesCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "captures <experiment name>",
		Short: "List the running packet captures of an experiment",
		Long: `List the running packet captures of an experiment

  Prints the VM, the interface index and the file of each running packet
  capture of the experiment, as phenix vm capture list <experiment name>
  does. A stopped experiment has no captures.`,
		Example:           "  phenix experiment captures my-exp\n  phenix experiment captures my-exp -o json",
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			captures, err := listCaptures(cmd, args[0], "")
			if err != nil {
				return err
			}

			return out.printAPI(cmd, captures, outputViews{
				formatTable: func(w io.Writer) error {
					printer.PrintTableOfSubnetCaptures(w, captures)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("experiment").AddCommand(newExperimentCapturesCmd())
}
