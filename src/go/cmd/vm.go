package cmd

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
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

func addVMLabelFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayP(
		"label",
		"l",
		nil,
		"Label to filter VMs (supports glob patterns); use 'all' to select every VM",
	)
}

func normalizeVMLabels(labels []string) []string {
	var normalized []string

	for _, label := range labels {
		for piece := range strings.SplitSeq(label, ",") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}

			normalized = append(normalized, piece)
		}
	}

	return normalized
}

func vmLabelMatchesLabel(label, expected string) (bool, error) {
	if strings.EqualFold(label, expected) {
		return true, nil
	}

	if strings.ContainsAny(expected, "*?[") {
		matched, err := path.Match(strings.ToLower(expected), strings.ToLower(label))
		if err != nil {
			return false, err
		}

		if matched {
			return true, nil
		}
	}

	return false, nil
}

func vmMatchesAnyLabel(vmInfo mm.VM, labels []string) (bool, error) {
	if len(labels) == 0 {
		return false, nil
	}

	if slices.ContainsFunc(labels, func(label string) bool { return strings.EqualFold(label, "all") }) {
		return true, nil
	}

	for label := range vmInfo.Tags {
		for _, expected := range labels {
			matched, err := vmLabelMatchesLabel(label, expected)
			if err != nil {
				return false, fmt.Errorf("invalid label %q: %w", expected, err)
			}

			if matched {
				return true, nil
			}
		}
	}

	return false, nil
}

func listVMsByLabel(expName string, labels []string) ([]mm.VM, error) {
	allVMs, err := vm.List(expName)
	if err != nil {
		return nil, err
	}

	if slices.ContainsFunc(labels, func(label string) bool { return strings.EqualFold(label, "all") }) {
		return allVMs, nil
	}

	var matchedVMs []mm.VM

	for _, vmInfo := range allVMs {
		matched, matchErr := vmMatchesAnyLabel(vmInfo, labels)
		if matchErr != nil {
			return nil, matchErr
		}

		if matched {
			matchedVMs = append(matchedVMs, vmInfo)
		}
	}

	if len(matchedVMs) == 0 {
		return nil, fmt.Errorf("no VMs matched label(s): %s", strings.Join(labels, ", "))
	}

	return matchedVMs, nil
}

func vmTargetNamesForCommand(cmd *cobra.Command, args []string) (string, []string, error) {
	if len(args) < 1 {
		return "", nil, errors.New("must provide an experiment name")
	}

	expName := args[0]

	if !cmd.Flags().Changed("label") {
		if len(args) != pauseArgs {
			return "", nil, errors.New("must provide an experiment and VM name (or use --label)")
		}

		return expName, []string{args[1]}, nil
	}

	flagLabels, err := cmd.Flags().GetStringArray("label")
	if err != nil {
		return "", nil, fmt.Errorf("getting vm label values: %w", err)
	}

	labels := normalizeVMLabels(append(flagLabels, args[1:]...))
	if len(labels) == 0 {
		return "", nil, errors.New("must provide at least one VM label filter")
	}

	vms, err := listVMsByLabel(expName, labels)
	if err != nil {
		return "", nil, err
	}

	names := make([]string, 0, len(vms))
	for _, vmInfo := range vms {
		names = append(names, vmInfo.Name)
	}

	return expName, names, nil
}

// Calls the appropriate VM API function for a command with one or more VMs specified by the user and handles error humanization and logging for the command
// This function assumes that the function passed to fn accepts two string arguments for the experiment name and VM name and returns an error if the operation was unsuccessful.
func processVariableVMsArgument(fn func(expName, vmName string) error, cmd *cobra.Command, args []string, opDesc, opPast string) error {
	expName, vmNames, err := vmTargetNamesForCommand(cmd, args)
	if err != nil {
		return err
	}

	for _, vmName := range vmNames {
		err := fn(expName, vmName)
		if err != nil {
			err := util.HumanizeError(err, "%s", "Unable to "+opDesc+" the "+vmName+" VM")

			return err.Humanized()
		}

		plog.Info(plog.TypeSystem, "vm "+opPast, "vm", vmName, "exp", expName)
	}

	return nil
}

/*
---------- VM Command Definitions ----------
*/

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
	desc := `Table of VM(s)

  Used to display a table of virtual machine(s) for a specific experiment;
  VM name is optional, when included will display only that VM.`

	cmd := &cobra.Command{
		Use:               "info <experiment name> [vm name]",
		Short:             "Table of virtual machine(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("must provide an experiment name")
			}

			if cmd.Flags().Changed("label") {
				flagLabels, err := cmd.Flags().GetStringArray("label")
				if err != nil {
					return fmt.Errorf("getting vm label values: %w", err)
				}

				labels := normalizeVMLabels(append(flagLabels, args[1:]...))
				if len(labels) == 0 {
					return errors.New("must provide at least one VM label filter")
				}

				vms, err := listVMsByLabel(args[0], labels)
				if err != nil {
					err := util.HumanizeError(err, "Unable to get a filtered list of VMs")

					return err.Humanized()
				}

				printer.PrintTableOfVMs(os.Stdout, vms...)

				return nil
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

	addVMLabelFlag(cmd)

	return cmd
}

func newVMPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "pause <experiment name> [vm name]",
		Short:             "Pause running VM(s) for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.Pause, cmd, args, "pause", "paused")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "resume <experiment name> [vm name]",
		Short:             "Resume paused VM(s) for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.Resume, cmd, args, "resume", "resumed")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "restart <experiment name> [vm name]",
		Short:             "Restart running, paused, or powered off VM(s) for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.Restart, cmd, args, "restart", "restarted")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMResetDiskCmd() *cobra.Command {
	desc := `Resets the disk state to the initial pre-boot disk state for running or powered off VM(s)

  Used to reset the disk state for the first disk for running or powered off virtual machine(s) for a specific
  experiment.  The VM's snapshot flag must be set to true in order to use this command.`

	cmd := &cobra.Command{
		Use:               "reset-disk <experiment name> [vm name]",
		Short:             "Resets the disk state for running or powered off VM(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.ResetDiskState, cmd, args, "reset disk", "disk reset")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMRedeployCmd() *cobra.Command {
	var (
		cpu  int
		mem  int
		part int
	)

	desc := `Redeploy running experiment VM(s)

  Used to redeploy running virtual machine(s) for a specific experiment; several redeploy
  values can be modified`

	cmd := &cobra.Command{
		Use:               "redeploy <experiment name> [vm name]",
		Short:             "Redeploy running experiment VM(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmNames, err := vmTargetNamesForCommand(cmd, args)
			if err != nil {
				return err
			}

			var (
				disk   = MustGetString(cmd.Flags(), "disk")
				inject = MustGetBool(cmd.Flags(), "replicate-injects")
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

			for _, vmName := range vmNames {
				err := vm.Redeploy(expName, vmName, opts...)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to redeploy the "+vmName+" VM")

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "vm redeployed", "vm", vmName, "exp", expName)
			}

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
	addVMLabelFlag(cmd)

	return cmd
}

func newVMShutdownCmd() *cobra.Command {
	desc := `Shuts down or powers off running or paused VM(s)

  Used to shutdown or power off running or paused virtual machine(s) for a specific
  experiment.  The shutdown is not graceful and is equivalent to pulling the power cord`

	cmd := &cobra.Command{
		Use:               "shutdown <experiment name> [vm name]",
		Short:             "Shutdown a running or paused VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.Shutdown, cmd, args, "shutdown", "shutdown")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMKillCmd() *cobra.Command {
	desc := `Kill running or paused VM(s)

  Used to kill or delete running or paused virtual machine(s) for a specific
  experiment`

	cmd := &cobra.Command{
		Use:               "kill <experiment name> [vm name]",
		Short:             "Kill a running or pause VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(vm.Kill, cmd, args, "kill", "killed")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

//nolint:funlen // complex logic
func newVMSetCmd() *cobra.Command {
	var (
		cpu  int
		mem  int
		part int
	)

	desc := `Set configuration value(s) for VM(s)

  Used to set one or more configuration values for virtual machine(s) in an
  experiment. Only flags that are explicitly provided will be applied. While
  an experiment is running, only labels can be updated via this command (use
  'phenix vm net' to modify interface VLAN connections on a running
  experiment).`

	cmd := &cobra.Command{
		Use:               "set <experiment name> [vm name]",
		Short:             "Set configuration value(s) for VM(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,

		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmNames, err := vmTargetNamesForCommand(cmd, args)
			if err != nil {
				return err
			}

			var (
				disk          = MustGetString(cmd.Flags(), "disk")
				dnb           = MustGetBool(cmd.Flags(), "do-not-boot")
				snapshot      = MustGetBool(cmd.Flags(), "snapshot")
				rawLabels     = MustGetStringArray(cmd.Flags(), "label-changes")
				appendLabels  = MustGetBool(cmd.Flags(), "append-labels")
				cpuChanged    = cmd.Flags().Changed("cpu")
				memChanged    = cmd.Flags().Changed("mem")
				partChanged   = cmd.Flags().Changed("partition")
				dnbChanged    = cmd.Flags().Changed("do-not-boot")
				snapChanged   = cmd.Flags().Changed("snapshot")
				labelsChanged = cmd.Flags().Changed("label-changes") ||
					cmd.Flags().Changed("append-labels")
			)

			if cpuChanged && (cpu < 1 || cpu > 8) {
				return errors.New("cpus can only be 1-8")
			}

			if memChanged && (mem < 512 || mem > 16384 || mem%512 != 0) {
				return errors.New(
					"memory must be one of 512, 1024, 2048, 3072, 4096, 8192, 12288, 16384",
				)
			}

			labels := make(map[string]string)

			for _, t := range rawLabels {
				k, v, ok := strings.Cut(t, "=")
				if !ok || k == "" {
					return fmt.Errorf("invalid label %q (expected key=value)", t)
				}

				labels[k] = v
			}

			if !cpuChanged && !memChanged && disk == "" && !partChanged &&
				!dnbChanged && !snapChanged && !labelsChanged {
				return errors.New("no configuration values provided to set")
			}

			for _, vmName := range vmNames {
				opts := []vm.UpdateOption{
					vm.UpdateExperiment(expName),
					vm.UpdateVM(vmName),
				}

				if cpuChanged {
					opts = append(opts, vm.UpdateWithCPU(cpu))
				}

				if memChanged {
					opts = append(opts, vm.UpdateWithMem(mem))
				}

				if disk != "" {
					opts = append(opts, vm.UpdateWithDisk(disk))
				}

				if partChanged {
					opts = append(opts, vm.UpdateWithPartition(part))
				}

				if dnbChanged {
					opts = append(opts, vm.UpdateWithDNB(dnb))
				}

				if snapChanged {
					opts = append(opts, vm.UpdateWithSnapshot(snapshot))
				}

				if labelsChanged {
					opts = append(opts, vm.UpdateWithTags(labels, appendLabels))
				}

				if err := vm.Update(opts...); err != nil {
					err := util.HumanizeError(err, "%s", "Unable to update the "+vmName+" VM")

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "vm updated", "vm", vmName, "exp", expName)
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&cpu, "cpu", "c", 0, "Number of VM CPUs (1-8 is valid)")
	cmd.Flags().
		IntVarP(&mem, "mem", "m", 0, "Amount of memory in megabytes (512, 1024, 2048, 3072, 4096, 8192, 12288, 16384 are valid)")
	cmd.Flags().StringP("disk", "d", "", "VM backing disk image file")
	cmd.Flags().
		IntVarP(&part, "partition", "p", 0, "Partition of disk to inject files into")
	cmd.Flags().Bool("do-not-boot", false, "Set the do-not-boot flag for the VM")
	cmd.Flags().Bool("snapshot", false, "Set the snapshot (non-persistent) flag for the VM")
	cmd.Flags().StringArrayP(
		"label-changes",
		"L",
		nil,
		"VM label(s) to add or edit in key=value form (may be repeated)",
	)
	cmd.Flags().
		Bool("append-labels", false, "Append the provided labels to the VM's existing labels instead of replacing the labels")
	addVMLabelFlag(cmd)

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
