package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

	"phenix/api/vm"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/printer"

	"github.com/spf13/cobra"
)

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
		Use:   "info <experiment name> <vm name>",
		Short: "Table of virtual machine(s)",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("Must provide an experiment name")
			}

			switch len(args) {
			case 1:
				vms, err := vm.List(args[0])
				if err != nil {
					err := util.HumanizeError(err, "Unable to get a list of VMs")
					return err.Humanized()
				}

				printer.PrintTableOfVMs(os.Stdout, vms...)
			case 2:
				vm, err := vm.Get(args[0], args[1])
				if err != nil {
					err := util.HumanizeError(err, "Unable to get information for the "+args[1]+" VM")
					return err.Humanized()
				}

				printer.PrintTableOfVMs(os.Stdout, *vm)
			default:
				return fmt.Errorf("Invalid argument")
			}

			return nil
		},
	}

	return cmd
}

func newVMPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause <experiment name> <vm name>",
		Short: "Pause a running VM for a specific experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Pause(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to pause the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was paused\n", vmName, expName)

			return nil
		},
	}

	return cmd
}

func newVMResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <experiment name> <vm name>",
		Short: "Resume a paused VM for a specific experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Resume(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to resume the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was resumed\n", vmName, expName)

			return nil
		},
	}

	return cmd
}

func newVMRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart <experiment name> <vm name>",
		Short: "Restart a running, paused, or powered off VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Restart(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to restart the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was restarted\n", vmName, expName)

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
		Use:   "reset-disk <experiment name> <vm name>",
		Short: "Resets the disk state for a running or powered off VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.ResetDiskState(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to reset disk for "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM's disk in the %s experiment was reset\n", vmName, expName)

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
		Use:   "redeploy <experiment name> <vm name>",
		Short: "Redeploy a running experiment VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
				disk    = MustGetString(cmd.Flags(), "disk")
				inject  = MustGetBool(cmd.Flags(), "replicate-injects")
			)

			if cpu != 0 && (cpu < 1 || cpu > 8) {
				return fmt.Errorf("CPUs can only be 1-8")
			}

			if mem != 0 && (mem < 512 || mem > 16384 || mem%512 != 0) {
				return fmt.Errorf("Memory must be one of 512, 1024, 2048, 3072, 4096, 8192, 12288, 16384")
			}

			opts := []vm.RedeployOption{
				vm.CPU(cpu),
				vm.Memory(mem),
				vm.Disk(disk),
				vm.Inject(inject),
				vm.InjectPartition(part),
			}

			if err := vm.Redeploy(expName, vmName, opts...); err != nil {
				err := util.HumanizeError(err, "Unable to redeploy the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was redeployed\n", vmName, expName)

			return nil
		},
	}

	// not sure that this is the correct way to handle ints
	cmd.Flags().IntVarP(&cpu, "cpu", "c", 1, "Number of VM CPUs (1-8 is valid)")
	cmd.Flags().IntVarP(&mem, "mem", "m", 512, "Amount of memory in megabytes (512, 1024, 2048, 3072, 4096, 8192, 12288, 16384 are valid)")
	cmd.Flags().StringP("disk", "d", "", "VM backing disk image")
	cmd.Flags().BoolP("replicate-injects", "r", false, "Recreate disk snapshot and VM injections")
	cmd.Flags().IntVarP(&part, "partition", "p", 1, "Partition of disk to inject files into (only used if disk option is specified)")

	return cmd
}

func newVMShutdownCmd() *cobra.Command {
	desc := `Shuts down or powers off a running or paused VM
	
  Used to shutdown or power off a running or paused virtual machine for a specific 
  experiment.  The shutdown is not graceful and is equivalent to pulling the power cord`

	cmd := &cobra.Command{
		Use:   "shutdown <experiment name> <vm name>",
		Short: "Shutdown a running or paused VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Shutdown(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to shutdown the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was shutdown\n", vmName, expName)

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
		Use:   "kill <experiment name> <vm name>",
		Short: "Kill a running or pause VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Kill(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to kill the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %s VM in the %s experiment was killed\n", vmName, expName)

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

	connect := &cobra.Command{
		Use:   "connect <experiment name> <vm name> <iface index> <vlan id>",
		Short: "Connect a VM interface to a VLAN",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 4 {
				return fmt.Errorf("Must provide an experiment name, VM name, iface index, and VLAN ID")
			}

			var (
				expName = args[0]
				vmName  = args[1]
				vlan    = args[3]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("The network interface index must be an integer")
			}

			if err := vm.Connect(expName, vmName, iface, vlan); err != nil {
				err := util.HumanizeError(err, "Unable to modify the connectivity for the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The network for the %s VM in the %s experiment was modified\n", vmName, expName)

			return nil
		},
	}

	disconnect := &cobra.Command{
		Use:   "disconnect <experiment name> <vm name> <iface index>",
		Short: "Disconnect a VM interface",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 3 {
				return fmt.Errorf("Must provide an experiment name, VM name, and iface index>")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("The network interface index must be an integer")
			}

			if err := vm.Disonnect(expName, vmName, iface); err != nil {
				err := util.HumanizeError(err, "Unable to disconnect the interface on the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The %d interface on the %s VM in the %s experiment was paused\n", iface, vmName, expName)

			return nil
		},
	}

	cmd.AddCommand(connect)
	cmd.AddCommand(disconnect)

	return cmd
}

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
		Use:   "start <experiment name> <vm name> <iface index> <output file>",
		Short: "Start a packet capture for a VM specifying the interface index and using given output file as name of capture file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 4 {
				return fmt.Errorf("Must provide an experiment name, VM name, iface index, and output file")
			}

			var (
				expName = args[0]
				vmName  = args[1]
				out     = args[3]
			)

			iface, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("The network interface index must be an integer")
			}

			if err := vm.StartCapture(expName, vmName, iface, out); err != nil {
				err := util.HumanizeError(err, "Unable to start a capture on the interface on the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("A packet capture was started for the %d interface on the %s VM in the %s experiment\n", iface, vmName, expName)

			return nil
		},
	}

	startSubnetCaptures := &cobra.Command{
		Use:   "start-subnet <experiment name> <subnet>",
		Short: "Start packet captures for the specified subnet",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("Must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = MustGetString(cmd.Flags(), "filter")
				vmList  = []string{}
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("An invalid ipv4 subnet was detected: %v", subnet)
			}

			// Apply the optional filter to restrict the
			// VMs searched
			if len(filter) > 0 {
				filterTree := mm.BuildTree(filter)

				vms, err := vm.List(expName)

				if err != nil {
					err := util.HumanizeError(err, "Unable to retrieve a list of VMs for "+expName+" ")
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
				err := util.HumanizeError(err, "Unable to start the packet capture(s) for "+subnet+" ")
				return err.Humanized()
			}

			fmt.Printf("The packet capture(s) for subnet %s were started\n\n", subnet)

			printer.PrintTableOfSubnetCaptures(os.Stdout, vms)

			return nil
		},
	}

	stopVMCaptures := &cobra.Command{
		Use:   "stop <experiment name> <vm name>",
		Short: "Stop all packet captures for the specified VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("Must provide an experiment and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.StopCaptures(expName, vmName); err != nil {
				err := util.HumanizeError(err, "Unable to stop the packet capture(s) on the "+vmName+" VM")
				return err.Humanized()
			}

			fmt.Printf("The packet capture(s) for the %s VM in the %s experiment was stopped\n", vmName, expName)

			return nil
		},
	}

	stopSubnetCaptures := &cobra.Command{
		Use:   "stop-subnet <experiment name> <subnet>",
		Short: "Stop all packet captures for the specified subnet",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("Must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = MustGetString(cmd.Flags(), "filter")
				vmList  = []string{}
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("An invalid subnet was detected: %v", subnet)
			}

			// Apply the optional filter to restrict the
			// VMs searched
			if len(filter) > 0 {
				filterTree := mm.BuildTree(filter)

				vms, err := vm.List(expName)

				if err != nil {
					err := util.HumanizeError(err, "Unable to retrieve a list of VMs for "+expName+" ")
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
				err := util.HumanizeError(err, "Unable to stop the packet capture(s) on the "+subnet+" ")
				return err.Humanized()
			}

			fmt.Printf("The packet capture(s) for the subnet %s were stopped\n", subnet)

			return nil
		},
	}

	stopAllCaptures := &cobra.Command{
		Use:   "stop-all <experiment name>",
		Short: "Stop all packet captures for the specified experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("Must provide an experiment name")
			}

			var (
				expName = args[0]
			)

			if _, err := vm.StopCaptureSubnet(expName, "", []string{}); err != nil {
				err := util.HumanizeError(err, "Unable to stop the packet capture(s) for "+expName+" ")
				return err.Humanized()
			}

			fmt.Printf("All packet captures for experiment %s were stopped\n", expName)

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
		Use:   "memory-snapshot <experiment name> <vm name> <snapshot file path>",
		Short: "Create an ELF memory snapshot of a VM",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 3 {
				return fmt.Errorf("Must provide an experiment name, VM name, and snapshot file path")
			}

			var (
				expName  = args[0]
				vmName   = args[1]
				snapshot = args[2]
			)

			cb := func(s string) {}
			if res, err := vm.MemorySnapshot(expName, vmName, snapshot, cb); err != nil {
				if res != "failed" {
					err := util.HumanizeError(err, "Unable to create a memory snapshot for the "+vmName+" VM")
					return err.Humanized()
				} else {
					err := util.HumanizeError(err, "Failed to create a memory snapshot for the "+vmName+" VM")
					return err.Humanized()
				}
			}

			fmt.Printf("Memory snapshot was created for the %s VM in the %s experiment\n", vmName, expName)

			return nil

		},
	}

	return cmd
}

func init() {
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
