package cmd

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/printer"
)

const (
	infoArgs         = 2
	pauseArgs        = 2
	defaultMem       = 512
	redeployArgs     = 2
	shutdownArgs     = 2
	killArgs         = 2
	connectArgs      = 4
	disconnectArgs   = 3
	startCaptureArgs = 4
	startSubnetArgs  = 2
	stopCaptureArgs  = 2
	stopSubnetArgs   = 2
	stopAllArgs      = 1
	memSnapArgs      = 3
)

func vmArgsCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		exps, err := experiment.List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		var names []string
		for _, e := range exps {
			if strings.HasPrefix(e.Metadata.Name, toComplete) {
				names = append(names, e.Metadata.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	} else if len(args) == 1 {
		vms, err := vm.List(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		var names []string
		for _, v := range vms {
			if strings.HasPrefix(v.Name, toComplete) {
				names = append(names, v.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func newVMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vm",
		Short: "Virtual machine management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newVMInfoCmd() *cobra.Command {
	desc := `Table of virtual machine(s)

  Used to display a table of virtual machine(s) for a specific experiment;
  virtual machine name is optional, when included will display only that VM.`

	cmd := &cobra.Command{
		Use:               "info <experiment name> <vm name>",
		Short:             "Table of virtual machine(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("must provide an experiment name")
			}

			switch len(args) {
			case 1:
				vms, err := vm.List(args[0])
				if err != nil {
					err := util.HumanizeError(err, "Unable to get a list of VMs")

					return err.Humanized()
				}

				printer.PrintTableOfVMs(os.Stdout, vms...)
			case infoArgs:
				vm, err := vm.Get(args[0], args[1])
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to get information for the "+args[1]+" VM",
					)

					return err.Humanized()
				}

				printer.PrintTableOfVMs(os.Stdout, *vm)
			default:
				return errors.New("invalid argument")
			}

			return nil
		},
	}

	return cmd
}

func newVMPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "pause <experiment name> <vm name>",
		Short:             "Pause a running VM for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != pauseArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.Pause(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to pause the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm paused", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "resume <experiment name> <vm name>",
		Short:             "Resume a paused VM for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != pauseArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.Resume(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to resume the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm resumed", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "restart <experiment name> <vm name>",
		Short:             "Restart a running, paused, or powered off VM",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != pauseArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.Restart(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to restart the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm restarted", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMResetDiskCmd() *cobra.Command {
	desc := `Resets the disk state to the initial pre-boot disk state for a running or powered off VM

  Used to reset the disk state for the first disk for a running or powered off virtual machine for a specific
  experiment.  The VM's snapshot flag must be set to true in order to use this command.`

	cmd := &cobra.Command{
		Use:               "reset-disk <experiment name> <vm name>",
		Short:             "Resets the disk state for a running or powered off VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != pauseArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.ResetDiskState(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to reset disk for "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm disk reset", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMRedeployCmd() *cobra.Command {
	var (
		cpu  int
		mem  int
		part int
	)

	desc := `Redeploy a running experiment VM

  Used to redeploy a running virtual machine for a specific experiment; several
  values can be modified`

	cmd := &cobra.Command{
		Use:               "redeploy <experiment name> <vm name>",
		Short:             "Redeploy a running experiment VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != redeployArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
				disk    = MustGetString(cmd.Flags(), "disk")
				inject  = MustGetBool(cmd.Flags(), "replicate-injects")
			)

			if cpu != 0 && (cpu < 1 || cpu > 8) {
				return errors.New("cpus can only be 1-8")
			}

			if mem != 0 && (mem < 512 || mem > 16384 || mem%512 != 0) {
				return errors.New(
					"memory must be one of 512, 1024, 2048, 3072, 4096, 8192, 12288, 16384",
				)
			}

			opts := []vm.RedeployOption{
				vm.CPU(cpu),
				vm.Memory(mem),
				vm.Disk(disk),
				vm.Inject(inject),
				vm.InjectPartition(part),
			}

			err := vm.Redeploy(expName, vmName, opts...)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to redeploy the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm redeployed", "vm", vmName, "exp", expName)

			return nil
		},
	}

	// not sure that this is the correct way to handle ints
	cmd.Flags().IntVarP(&cpu, "cpu", "c", 1, "Number of VM CPUs (1-8 is valid)")
	cmd.Flags().
		IntVarP(&mem, "mem", "m", defaultMem, "Amount of memory in megabytes (512, 1024, 2048, 3072, 4096, 8192, 12288, 16384 are valid)")
	cmd.Flags().StringP("disk", "d", "", "VM backing disk image")
	cmd.Flags().BoolP("replicate-injects", "r", false, "Recreate disk snapshot and VM injections")
	cmd.Flags().
		IntVarP(&part, "partition", "p", 1, "Partition of disk to inject files into (only used if disk option is specified)")

	return cmd
}

func newVMShutdownCmd() *cobra.Command {
	desc := `Shuts down or powers off a running or paused VM

  Used to shutdown or power off a running or paused virtual machine for a specific
  experiment.  The shutdown is not graceful and is equivalent to pulling the power cord`

	cmd := &cobra.Command{
		Use:               "shutdown <experiment name> <vm name>",
		Short:             "Shutdown a running or paused VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != shutdownArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.Shutdown(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to shutdown the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm shutdown", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMKillCmd() *cobra.Command {
	desc := `Kill a running or paused VM

  Used to kill or delete a running or paused virtual machine for a specific
  experiment`

	cmd := &cobra.Command{
		Use:               "kill <experiment name> <vm name>",
		Short:             "Kill a running or pause VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != killArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.Kill(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to kill the "+vmName+" VM")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm killed", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func newVMSetCmd() *cobra.Command {
	desc := `Set configuration value for a VM

  Used to set a configuration value for a virtual machine in a stopped
  experiment. This command is not yet implemented. For now, you can edit the
  experiment directly with 'phenix config edit'`

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set configuration value for a VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newVMNetConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "connect <experiment name> <vm name> <iface index> <vlan id>",
		Short:             "Connect a VM interface to a VLAN",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != connectArgs {
				return errors.New(
					"must provide an experiment name, VM name, iface index, and VLAN ID",
				)
			}

			var (
				expName = args[0]
				vmName  = args[1]
				vlan    = args[3]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return errors.New("the network interface index must be an integer")
			}

			if err := vm.Connect(expName, vmName, iface, vlan); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to modify the connectivity for the "+vmName+" VM",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm network modified", "vm", vmName, "exp", expName)

			return nil
		},
	}
}

func newVMNetDisconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "disconnect <experiment name> <vm name> <iface index>",
		Short:             "Disconnect a VM interface",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != disconnectArgs {
				return errors.New("must provide an experiment name, VM name, and iface index>")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return errors.New("the network interface index must be an integer")
			}

			if err := vm.Disconnect(expName, vmName, iface); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to disconnect the interface on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			plog.Info(
				plog.TypeSystem,
				"vm interface disconnected",
				"iface",
				iface,
				"vm",
				vmName,
				"exp",
				expName,
			)

			return nil
		},
	}
}

func newVMNetCmd() *cobra.Command {
	desc := `Modify network connectivity for a VM

  Used to modify the network connectivity for a virtual machine in a running
  experiment; see command help for connect or disconnect for additional
  arguments.`

	cmd := &cobra.Command{
		Use:   "net",
		Short: "Modify network connectivity for a VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVMNetConnectCmd())
	cmd.AddCommand(newVMNetDisconnectCmd())

	return cmd
}

//nolint:funlen,maintidx // command definition
func newVMCaptureCmd() *cobra.Command {
	desc := `Modify network packet captures for a VM

  Used to modify the network packet captures for virtual machines in a running
  experiment; see command help for start and stop for additional arguments.`

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Modify network packet captures for one or more VMs",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	startVMCapture := &cobra.Command{
		Use:               "start <experiment name> <vm name> <iface index> <output file>",
		Short:             "Start a packet capture for a VM specifying the interface index and using given output file as name of capture file",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != startCaptureArgs {
				return errors.New(
					"must provide an experiment name, VM name, iface index, and output file",
				)
			}

			var (
				expName = args[0]
				vmName  = args[1]
				out     = args[3]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return errors.New("the network interface index must be an integer")
			}

			if err := vm.StartCapture(expName, vmName, iface, out); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to start a capture on the interface on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			plog.Info(
				plog.TypeSystem,
				"vm packet capture started",
				"iface",
				iface,
				"vm",
				vmName,
				"exp",
				expName,
			)

			return nil
		},
	}

	startSubnetCaptures := &cobra.Command{
		Use:   "start-subnet <experiment name> <subnet>",
		Short: "Start packet captures for the specified subnet",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < startSubnetArgs {
				return errors.New("must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = MustGetString(cmd.Flags(), "filter")
				vmList  = []string{}
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("an invalid ipv4 subnet was detected: %v", subnet)
			}

			// Apply the optional filter to restrict the
			// VMs searched
			if len(filter) > 0 {
				filterTree := mm.BuildTree(filter)

				vms, err := vm.List(expName)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to retrieve a list of VMs for "+expName+" ",
					)

					return err.Humanized()
				}

				for _, vm := range vms {
					if filterTree == nil {
						continue
					} else {
						if !filterTree.Evaluate(&vm) {
							continue
						}

						vmList = append(vmList, vm.Name)
					}
				}
			}

			vms, err := vm.CaptureSubnet(expName, subnet, vmList)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to start the packet capture(s) for "+subnet+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "subnet packet captures started", "subnet", subnet)

			printer.PrintTableOfSubnetCaptures(os.Stdout, vms)

			return nil
		},
	}

	stopVMCaptures := &cobra.Command{
		Use:               "stop <experiment name> <vm name>",
		Short:             "Stop all packet captures for the specified VM",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != stopCaptureArgs {
				return errors.New("must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			err := vm.StopCaptures(expName, vmName)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to stop the packet capture(s) on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "vm packet captures stopped", "vm", vmName, "exp", expName)

			return nil
		},
	}

	stopSubnetCaptures := &cobra.Command{
		Use:   "stop-subnet <experiment name> <subnet>",
		Short: "Stop all packet captures for the specified subnet",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < stopSubnetArgs {
				return errors.New("must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = MustGetString(cmd.Flags(), "filter")
				vmList  = []string{}
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("an invalid subnet was detected: %v", subnet)
			}

			// Apply the optional filter to restrict the
			// VMs searched
			if len(filter) > 0 {
				filterTree := mm.BuildTree(filter)

				vms, err := vm.List(expName)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to retrieve a list of VMs for "+expName+" ",
					)

					return err.Humanized()
				}

				for _, vm := range vms {
					if filterTree == nil {
						continue
					} else {
						if !filterTree.Evaluate(&vm) {
							continue
						}

						vmList = append(vmList, vm.Name)
					}
				}
			}

			if _, err := vm.StopCaptureSubnet(expName, subnet, vmList); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to stop the packet capture(s) on the "+subnet+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "subnet packet captures stopped", "subnet", subnet)

			return nil
		},
	}

	stopAllCaptures := &cobra.Command{
		Use:   "stop-all <experiment name>",
		Short: "Stop all packet captures for the specified experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != stopAllArgs {
				return errors.New("must provide an experiment name")
			}

			expName := args[0]

			if _, err := vm.StopCaptureSubnet(expName, "", []string{}); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to stop the packet capture(s) for "+expName+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "all packet captures stopped", "exp", expName)

			return nil
		},
	}

	cmd.AddCommand(startVMCapture)
	cmd.AddCommand(startSubnetCaptures)
	cmd.AddCommand(stopVMCaptures)
	cmd.AddCommand(stopSubnetCaptures)
	cmd.AddCommand(stopAllCaptures)

	startSubnetCaptures.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")
	stopSubnetCaptures.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")

	return cmd
}

func newVMMemorySnapshotCmd() *cobra.Command {
	desc := `Create an ELF memory snapshot of the VM

  Used to create an ELF memory snapshot for a running virtual machine
  that is compatible with memory forensic toolkits Volatility and Google's Rekall.`

	cmd := &cobra.Command{
		Use:               "memory-snapshot <experiment name> <vm name> <snapshot file path>",
		Short:             "Create an ELF memory snapshot of a VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != memSnapArgs {
				return errors.New(
					"must provide an experiment name, VM name, and snapshot file path",
				)
			}

			var (
				expName  = args[0]
				vmName   = args[1]
				snapshot = args[2]
			)

			cb := func(s string) {}
			if res, err := vm.MemorySnapshot(expName, vmName, snapshot, cb); err != nil {
				if res != "failed" {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to create a memory snapshot for the "+vmName+" VM",
					)

					return err.Humanized()
				} else {
					err := util.HumanizeError(
						err,
						"%s",
						"Failed to create a memory snapshot for the "+vmName+" VM",
					)

					return err.Humanized()
				}
			}

			plog.Info(plog.TypeSystem, "vm memory snapshot created", "vm", vmName, "exp", expName)

			return nil
		},
	}

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	vmCmd := newVMCmd()

	vmCmd.AddCommand(newVMInfoCmd())
	vmCmd.AddCommand(newVMPauseCmd())
	vmCmd.AddCommand(newVMResumeCmd())
	vmCmd.AddCommand(newVMRestartCmd())
	vmCmd.AddCommand(newVMResetDiskCmd())
	vmCmd.AddCommand(newVMRedeployCmd())
	vmCmd.AddCommand(newVMShutdownCmd())
	vmCmd.AddCommand(newVMKillCmd())
	vmCmd.AddCommand(newVMSetCmd())
	vmCmd.AddCommand(newVMNetCmd())
	vmCmd.AddCommand(newVMCaptureCmd())
	vmCmd.AddCommand(newVMMemorySnapshotCmd())

	rootCmd.AddCommand(vmCmd)
}
