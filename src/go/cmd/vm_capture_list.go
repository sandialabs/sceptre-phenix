package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/printer"
)

// listCaptures returns the running packet captures of the experiment expName:
// those of the VM vmName, or of every VM when vmName is empty. It refuses an
// experiment or VM that does not exist.
func listCaptures(cmd *cobra.Command, expName, vmName string) ([]mm.Capture, error) {
	ctx := commandContext(cmd)

	if vmName == "" {
		if _, err := experiment.Get(expName); err != nil {
			return nil, util.HumanizeError(err, "Unable to list the packet captures of the %s experiment", expName).Humanized()
		}

		return vm.ExperimentCaptures(ctx, expName), nil
	}

	if _, err := vm.CheckTarget(expName, vmName, false); err != nil {
		return nil, fmt.Errorf("unable to list the packet captures of the %s VM: %w", vmName, err)
	}

	return vm.Captures(ctx, expName, vmName), nil
}

func newVMCaptureListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list <experiment name> [<vm name>]",
		Short: "List the running packet captures of an experiment or a VM",
		Long: `List the running packet captures of an experiment or a VM

  Prints the VM, the interface index and the file of each running packet
  capture. With a VM name, the table shows only the captures of that VM. A
  stopped experiment has no captures. -o json and -o yaml print the
  captures with the fields of GET /experiments/{name}/captures.`,
		Example: `
  phenix vm capture list my-exp
  phenix vm capture list my-exp web -o json`,
		Args:              argsWithUsage(cobra.RangeArgs(1, 2)), //nolint:mnd // an experiment and a VM
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			vmName := ""
			if len(args) > 1 {
				vmName = args[1]
			}

			captures, err := listCaptures(cmd, args[0], vmName)
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
	subcommandOf("vm", "capture").AddCommand(newVMCaptureListCmd())
}
