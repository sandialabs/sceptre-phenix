package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"phenix/api/vm"
	"phenix/util/plog"
)

const (
	cdromInsertArgs = 3
	cdromEjectArgs  = 2
)

func newVMCdromCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cdrom",
		Short: "Insert or eject the optical disc of a VM",
		Long: `Insert or eject the optical disc of a VM

  Changes the disc in the first optical drive of a running VM. The experiment
  must run.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	insert := &cobra.Command{
		Use:   "insert <experiment name> <vm name> <iso path>",
		Short: "Insert an optical disc image into a VM",
		Long: `Insert an optical disc image into a VM

  Puts the ISO image in the first optical drive of the VM, in place of the
  disc it holds. minimega reads the path on the cluster node that runs the
  VM.`,
		Example:           "  phenix vm cdrom insert my-exp web /phenix/images/tools.iso",
		Args:              argsWithUsage(cobra.ExactArgs(cdromInsertArgs)),
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmName, iso := args[0], args[1], args[2]

			if _, err := vm.CheckTarget(expName, vmName, true); err != nil {
				return fmt.Errorf("unable to insert a disc into the %s VM: %w", vmName, err)
			}

			if err := vm.ChangeOpticalDisc(commandContext(cmd), expName, vmName, iso); err != nil {
				return fmt.Errorf("unable to insert a disc into the %s VM: %w", vmName, err)
			}

			plog.Info(plog.TypeSystem, "optical disc changed", "exp", expName, "vm", vmName, "iso", iso)

			return nil
		},
	}

	eject := &cobra.Command{
		Use:               "eject <experiment name> <vm name>",
		Short:             "Eject the optical disc of a VM",
		Example:           "  phenix vm cdrom eject my-exp web",
		Args:              argsWithUsage(cobra.ExactArgs(cdromEjectArgs)),
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmName := args[0], args[1]

			if _, err := vm.CheckTarget(expName, vmName, true); err != nil {
				return fmt.Errorf("unable to eject the disc of the %s VM: %w", vmName, err)
			}

			if err := vm.EjectOpticalDisc(commandContext(cmd), expName, vmName); err != nil {
				return fmt.Errorf("unable to eject the disc of the %s VM: %w", vmName, err)
			}

			plog.Info(plog.TypeSystem, "optical disc ejected", "exp", expName, "vm", vmName)

			return nil
		},
	}

	cmd.AddCommand(insert, eject)

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMCdromCmd())
}
