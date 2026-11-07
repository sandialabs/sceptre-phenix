package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"phenix/api/experiment"
	"phenix/api/livecapture"
	"phenix/api/vm"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/printer"
)

// memRangeMsg is the error for a VM memory value that the CLI refuses.
const memRangeMsg = "memory must be a multiple of 512 from 512 to 16384"

const (
	infoArgs               = 2
	pauseArgs              = 2
	connectArgs            = 4
	disconnectArgs         = 3
	startCaptureArgs       = 4
	startSubnetArgs        = 2
	startVLANArgs          = 2
	stopCaptureArgs        = 2
	stopCaptureIfaceArgs   = 3
	stopSubnetArgs         = 2
	stopVLANArgs           = 2
	stopAllArgs            = 1
	streamCaptureArgs      = 3
	memSnapArgs            = 3
	mountArgs              = 2
	unmountArgs            = 2
	vmInfoColumnHost       = "host"
	vmInfoColumnName       = "name"
	vmInfoColumnRunning    = "running"
	vmInfoColumnDisk       = "disk"
	vmInfoColumnInterfaces = "interfaces"
	vmInfoColumnUptime     = "uptime"
	vmInfoColumnMemory     = "memory"
	vmInfoColumnVCPUs      = "vcpus"
	vmInfoColumnOSType     = "ostype"
	vmInfoColumnTaps       = "taps"
)

func addVMLabelFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayP(
		"label",
		"l",
		nil,
		"Label to filter VMs (supports glob patterns); use 'all' to select every VM",
	)
}

func addVMHostFlag(cmd *cobra.Command) {
	cmd.Flags().StringArray(
		"host",
		nil,
		"Cluster host to filter VMs by, named exactly as minimega reports it; may be repeated",
	)
	_ = cmd.RegisterFlagCompletionFunc("host", vmHostCompletion)
}

func vmHostCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	vms, err := vm.List(context.Background(), args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return vmHostNames(vms, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// vmHostNames returns the distinct cluster hosts running the VMs, sorted, that
// start with prefix.
func vmHostNames(vms []mm.VM, prefix string) []string {
	seen := map[string]struct{}{}

	for _, vmInfo := range vms {
		if vmInfo.Host != "" && strings.HasPrefix(vmInfo.Host, prefix) {
			seen[vmInfo.Host] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// vmInfoHosts returns the --host values, split at commas; nil when the flag is
// not set.
func vmInfoHosts(cmd *cobra.Command) ([]string, error) {
	if !cmd.Flags().Changed("host") {
		return nil, nil
	}

	flagHosts, err := cmd.Flags().GetStringArray("host")
	if err != nil {
		return nil, fmt.Errorf("getting vm host values: %w", err)
	}

	hosts := normalizeVMFilterValues(flagHosts)
	if len(hosts) == 0 {
		return nil, errors.New("must provide at least one VM host filter")
	}

	return hosts, nil
}

// filterVMsByHost keeps the VMs running on any of hosts, as the UI's Hosts
// filter does; no hosts keeps every VM, and none left is an error.
func filterVMsByHost(vms []mm.VM, hosts []string) ([]mm.VM, error) {
	if len(hosts) == 0 {
		return vms, nil
	}

	matched := make([]mm.VM, 0, len(vms))

	for _, vmInfo := range vms {
		if vmInfo.OnAnyHost(hosts) {
			matched = append(matched, vmInfo)
		}
	}

	if len(matched) == 0 {
		return nil, fmt.Errorf("no VMs matched host(s): %s", strings.Join(hosts, ", "))
	}

	return matched, nil
}

func normalizeVMFilterValues(labels []string) []string {
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

// parseLabelFlags turns the key=value values of the --label flag into labels. A key must not be blank; a value can be
// empty and can hold "=".
func parseLabelFlags(values []string) (map[string]string, error) {
	labels := make(map[string]string, len(values))

	for _, value := range values {
		k, v, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid label %q (expected key=value)", value)
		}

		labels[k] = v
	}

	return labels, nil
}

// errBadIfaceIndex is returned for a network interface index argument that is not one.
var errBadIfaceIndex = errors.New("the network interface index must be an integer")

// parseIfaceIndex parses the network interface index argument of vm net: a whole number, 0 or more.
func parseIfaceIndex(arg string) (int, error) {
	iface, err := strconv.Atoi(arg)
	if err != nil || iface < 0 {
		return 0, fmt.Errorf("%w of 0 or more, not %q", errBadIfaceIndex, arg)
	}

	return iface, nil
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
	allVMs, err := vm.List(context.Background(), expName)
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

type vmInfoFilter struct {
	column string
	value  string
}

func parseVMInfoFilters(rawFilters []string) ([]vmInfoFilter, error) {
	filters := make([]vmInfoFilter, 0, len(rawFilters))

	for _, rawFilter := range rawFilters {
		rawColumn, value, found := strings.Cut(rawFilter, "=")
		if !found || strings.TrimSpace(rawColumn) == "" {
			return nil, fmt.Errorf("invalid VM info filter %q: expected COLUMN=VALUE", rawFilter)
		}

		column := strings.ToLower(
			strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.TrimSpace(rawColumn)),
		)
		switch column {
		case vmInfoColumnHost,
			vmInfoColumnName,
			vmInfoColumnRunning,
			vmInfoColumnDisk,
			vmInfoColumnInterfaces,
			vmInfoColumnUptime,
			vmInfoColumnMemory,
			vmInfoColumnVCPUs,
			vmInfoColumnOSType,
			vmInfoColumnTaps:
		case "ram":
			column = vmInfoColumnMemory
		case "cpus":
			column = vmInfoColumnVCPUs
		default:
			return nil, fmt.Errorf("unknown VM info filter column %q", strings.TrimSpace(rawColumn))
		}

		filters = append(filters, vmInfoFilter{
			column: column,
			value:  strings.TrimSpace(value),
		})
	}

	return filters, nil
}

func vmInfoColumnValue(vmInfo mm.VM, column string) string {
	switch column {
	case vmInfoColumnHost:
		return vmInfo.Host
	case vmInfoColumnName:
		return vmInfo.Name
	case vmInfoColumnRunning:
		return strconv.FormatBool(vmInfo.Running)
	case vmInfoColumnDisk:
		return vmInfo.Disk
	case vmInfoColumnInterfaces:
		interfaces := make([]string, 0, len(vmInfo.Networks))
		for idx, network := range vmInfo.Networks {
			interfaces = append(interfaces, fmt.Sprintf("ID: %d, IP: %s, VLAN: %s", idx, vmInfo.IPv4[idx], network))
		}

		return strings.Join(interfaces, "\n")
	case vmInfoColumnUptime:
		if vmInfo.Running {
			return (time.Duration(vmInfo.Uptime) * time.Second).String()
		}

		return ""
	case vmInfoColumnMemory:
		return strconv.Itoa(vmInfo.RAM)
	case vmInfoColumnVCPUs:
		return strconv.Itoa(vmInfo.CPUs)
	case vmInfoColumnOSType:
		return vmInfo.OSType
	case vmInfoColumnTaps:
		return strings.Join(vmInfo.Taps, ", ")
	default:
		panic(fmt.Sprintf("unsupported VM info column %q", column))
	}
}

func vmInfoFilterMatches(vmInfo mm.VM, filter vmInfoFilter) bool {
	value := vmInfoColumnValue(vmInfo, filter.column)
	if strings.EqualFold(value, filter.value) {
		return true
	}

	if filter.column != vmInfoColumnDisk || value == "" {
		return false
	}

	filename := path.Base(value)
	stem := strings.TrimSuffix(filename, path.Ext(filename))

	return strings.EqualFold(filename, filter.value) || strings.EqualFold(stem, filter.value)
}

func filterVMInfo(vms []mm.VM, rawFilters []string) ([]mm.VM, error) {
	filters, err := parseVMInfoFilters(rawFilters)
	if err != nil {
		return nil, err
	}

	if len(filters) == 0 {
		return vms, nil
	}

	filtered := make([]mm.VM, 0, len(vms))

	for _, vmInfo := range vms {
		matches := true
		for _, filter := range filters {
			if !vmInfoFilterMatches(vmInfo, filter) {
				matches = false
				break
			}
		}

		if matches {
			filtered = append(filtered, vmInfo)
		}
	}

	return filtered, nil
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

	labels := normalizeVMFilterValues(append(flagLabels, args[1:]...))
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

// vmChange is a destructive VM command that processVMChange runs: check
// checks one VM as the change would, and changes nothing; change changes it.
// desc and past name the change, such as "kill" and "killed".
type vmChange struct {
	check  func(ctx context.Context, expName, vmName string) error
	change func(ctx context.Context, expName, vmName string) error
	desc   string
	past   string
}

// processVMChange runs c for each VM that the arguments or --label select,
// as processVariableVMsArgument does. With --dry-run, it checks each VM,
// writes what it would change, and changes nothing.
func processVMChange(cmd *cobra.Command, args []string, dry dryRun, c vmChange) error {
	if !dry.enabled() {
		return processVariableVMsArgument(withoutContext(c.change), cmd, args, c.desc, c.past)
	}

	expName, vmNames, err := vmTargetNamesForCommand(cmd, args)
	if err != nil {
		return err
	}

	for _, vmName := range vmNames {
		if err := c.check(commandContext(cmd), expName, vmName); err != nil {
			return util.HumanizeError(err, "%s", "Unable to "+c.desc+" the "+vmName+" VM").Humanized()
		}

		dry.skip(c.desc, "VM "+vmName+" in experiment "+expName)
	}

	return nil
}

// checkVMRunsInMinimega returns an error unless minimega has the VM vmName of
// the experiment expName, as a change of the VM by minimega needs.
func checkVMRunsInMinimega(ctx context.Context, expName, vmName string) error {
	if _, err := mm.GetVMState(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx)); err != nil {
		return fmt.Errorf("getting the state of VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}

// withoutContext is fn for the CLI, whose minimega commands wait as long as
// they must (see mm.Context).
func withoutContext(fn func(ctx context.Context, expName, vmName string) error) func(expName, vmName string) error {
	return func(expName, vmName string) error {
		return fn(context.Background(), expName, vmName)
	}
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

// listVMInfoVMs returns the VMs vm info starts from: those carrying a label,
// the named VM, or every VM in the experiment.
func listVMInfoVMs(cmd *cobra.Command, args []string) ([]mm.VM, error) {
	var (
		vms []mm.VM
		err error
	)

	if cmd.Flags().Changed("label") {
		flagLabels, err := cmd.Flags().GetStringArray("label")
		if err != nil {
			return nil, fmt.Errorf("getting vm label values: %w", err)
		}

		labels := normalizeVMFilterValues(append(flagLabels, args[1:]...))
		if len(labels) == 0 {
			return nil, errors.New("must provide at least one VM label filter")
		}

		vms, err = listVMsByLabel(args[0], labels)
		if err != nil {
			err := util.HumanizeError(err, "Unable to get a filtered list of VMs")

			return nil, err.Humanized()
		}
	} else {
		switch len(args) {
		case 1:
			vms, err = vm.List(context.Background(), args[0])
			if err != nil {
				err := util.HumanizeError(err, "Unable to get a list of VMs")

				return nil, err.Humanized()
			}
		case infoArgs:
			vmInfo, err := vm.Get(context.Background(), args[0], args[1])
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to get information for the "+args[1]+" VM",
				)

				return nil, err.Humanized()
			}

			vms = []mm.VM{*vmInfo}
		default:
			return nil, errors.New("invalid argument")
		}
	}

	return vms, nil
}

// vmInfoRow is a VM as phenix vm info -o json shows it, with the JSON names
// of GET /experiments/{exp}/vms.
type vmInfoRow struct {
	Name            string            `json:"name"             yaml:"name"`
	Host            string            `json:"host"             yaml:"host"`
	IPv4            []string          `json:"ipv4"             yaml:"ipv4"`
	CPUs            int               `json:"cpus"             yaml:"cpus"`
	RAM             int               `json:"ram"              yaml:"ram"`
	Disk            string            `json:"disk"             yaml:"disk"`
	Uptime          float64           `json:"uptime"           yaml:"uptime"`
	Networks        []string          `json:"networks"         yaml:"networks"`
	Taps            []string          `json:"taps"             yaml:"taps"`
	Captures        []mm.Capture      `json:"captures"         yaml:"captures"`
	DoNotBoot       bool              `json:"dnb"              yaml:"dnb"`
	Running         bool              `json:"running"          yaml:"running"`
	Busy            bool              `json:"busy"             yaml:"busy"`
	Experiment      string            `json:"experiment"       yaml:"experiment"`
	State           string            `json:"state"            yaml:"state"`
	CdRom           string            `json:"cdRom"            yaml:"cdRom"`
	Tags            map[string]string `json:"tags"             yaml:"tags"`
	CCActive        bool              `json:"ccActive"         yaml:"ccActive"`
	Snapshot        bool              `json:"snapshot"         yaml:"snapshot"`
	InjectPartition int               `json:"inject_partition" yaml:"inject_partition"`
	Description     string            `json:"description"      yaml:"description"`
	OSType          string            `json:"os_type"          yaml:"os_type"`
}

// vmInfoRows returns the rows of the VMs, with empty lists and maps in
// place of nil ones.
func vmInfoRows(vms []mm.VM) []vmInfoRow {
	rows := make([]vmInfoRow, 0, len(vms))

	for _, v := range vms {
		rows = append(rows, vmInfoRow{
			Name: v.Name, Host: v.Host, IPv4: nonNil(v.IPv4), CPUs: v.CPUs, RAM: v.RAM, Disk: v.Disk,
			Uptime: v.Uptime, Networks: nonNil(v.Networks), Taps: nonNil(v.Taps), Captures: nonNil(v.Captures),
			DoNotBoot: v.DoNotBoot, Running: v.Running, Busy: v.Busy, Experiment: v.Experiment, State: v.State,
			CdRom: v.CdRom, Tags: nonNilMap(v.Tags), CCActive: v.CCActive, Snapshot: v.Snapshot,
			InjectPartition: v.InjectPartition, Description: v.Description, OSType: v.OSType,
		})
	}

	return rows
}

func newVMInfoCmd() *cobra.Command {
	var out *outputFlag

	desc := `Table of VM(s)

  Used to display a table of virtual machine(s) for a specific experiment;
  VM name is optional, when included will display only that VM.

  Filter the output by repeating --filter with a COLUMN=VALUE expression.
  Multiple filters are combined with AND semantics. Column names and values
  are case-insensitive. Supported columns are HOST, NAME, RUNNING, DISK,
  INTERFACES, UPTIME, MEMORY, VCPUS, OS_TYPE, and TAPS. DISK values may be
  full paths, filenames, or filenames without extensions. For example:

    phenix vm info my-experiment --filter RUNNING=true
    phenix vm info my-experiment --filter RUNNING=true --filter OS_TYPE=linux
    phenix vm info my-experiment --filter DISK=jammy

  Repeat --host to list only the VMs running on any of the named cluster
  hosts, named exactly as minimega reports them. --host combines with the VM
  name or --label, and with --filter: a VM must match each of them. For
  example:

    phenix vm info my-experiment --host compute1 --host compute2
    phenix vm info my-experiment --host compute1 --label role-web

  -o json and -o yaml print each VM with the fields of the API, as
  GET /experiments/{exp}/vms gives them.`

	cmd := &cobra.Command{
		Use:               "info <experiment name> [vm name]",
		Short:             "Table of virtual machine(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("must provide an experiment name")
			}

			hosts, err := vmInfoHosts(cmd)
			if err != nil {
				return err
			}

			vms, err := listVMInfoVMs(cmd, args)
			if err != nil {
				return err
			}

			vms, err = filterVMsByHost(vms, hosts)
			if err != nil {
				return err
			}

			vms, err = filterVMInfo(vms, mustGet(cmd.Flags().GetStringArray, "filter"))
			if err != nil {
				return err
			}

			return out.printAPI(cmd, vmInfoRows(vms), outputViews{
				formatTable: func(w io.Writer) error {
					printer.PrintTableOfVMs(w, outputPalette(w), mustGet(cmd.Flags().GetBool, "taps"), vms...)

					return nil
				},
			})
		},
	}

	addVMLabelFlag(cmd)
	addVMHostFlag(cmd)
	cmd.Flags().StringArrayP("filter", "f", nil, "Filter output by column value (COLUMN=VALUE); may be repeated")
	cmd.Flags().BoolP("taps", "t", false, "Include tap interface names in the table")
	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newVMPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "pause <experiment name> [vm name]",
		Short:             "Pause running VM(s) for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(withoutContext(vm.Pause), cmd, args, "pause", "paused")
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
			return processVariableVMsArgument(withoutContext(vm.Resume), cmd, args, "resume", "resumed")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "restart <experiment name> [vm name]",
		Aliases:           []string{restartAlias},
		Short:             "Restart running, paused, or powered off VM(s) for a specific experiment",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVariableVMsArgument(withoutContext(vm.Restart), cmd, args, "restart", "restarted")
		},
	}

	addVMLabelFlag(cmd)

	return cmd
}

func newVMResetDiskCmd() *cobra.Command {
	var dry dryRun

	desc := `Resets the disk state to the initial pre-boot disk state for running or powered off VM(s)

  Used to reset the disk state for the first disk for running or powered off virtual machine(s) for a specific
  experiment.  The VM's snapshot flag must be set to true in order to use this command.`

	cmd := &cobra.Command{
		Use:               "reset-disk <experiment name> [vm name]",
		Short:             "Resets the disk state for running or powered off VM(s)",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVMChange(cmd, args, dry, vmChange{
				check: vm.CheckResetDiskState, change: vm.ResetDiskState, desc: "reset the disk of", past: "disk reset",
			})
		},
	}

	addVMLabelFlag(cmd)

	dry = addDryRunFlag(cmd)

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
  values can be modified. The CPU and memory values change only when their flags are set.
  A new disk replaces only the first disk of the VM. The disk value is
  <path>[,<interface>][,<cache>]; a field that the value leaves out keeps the value of
  the old first disk.`

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
				disk   = mustGet(cmd.Flags().GetString, "disk")
				inject = mustGet(cmd.Flags().GetBool, "replicate-injects")
			)

			if cpu != 0 && (cpu < 1 || cpu > 8) {
				return errors.New("cpus can only be 1-8")
			}

			if mem != 0 && (mem < 512 || mem > 16384 || mem%512 != 0) {
				return errors.New(memRangeMsg)
			}

			opts := []vm.RedeployOption{
				vm.CPU(cpu),
				vm.Memory(mem),
				vm.Disk(disk),
				vm.Inject(inject),
				vm.InjectPartition(part),
			}

			for _, vmName := range vmNames {
				err := vm.Redeploy(context.Background(), expName, vmName, opts...)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to redeploy the "+vmName+" VM")

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "vm redeployed", "vm", vmName, "exp", expName)
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&cpu, "cpu", "c", 0, "Number of VM CPUs (1-8 is valid; 0 keeps the current value)")
	cmd.Flags().
		IntVarP(&mem, "mem", "m", 0, "Amount of memory in megabytes (a multiple of 512 from 512 to 16384; 0 keeps the current value)")
	cmd.Flags().StringP("disk", "d", "", "New first disk: <path>[,<interface>][,<cache>]")
	_ = cmd.RegisterFlagCompletionFunc("disk", completeDisks)
	cmd.Flags().BoolP("replicate-injects", "r", false, "Recreate disk snapshot and VM injections")
	cmd.Flags().
		IntVarP(&part, "partition", "p", 1,
			"Partition of the new disk to inject files into (used only with --disk and --replicate-injects)")
	addVMLabelFlag(cmd)

	return cmd
}

func newVMShutdownCmd() *cobra.Command {
	var dry dryRun

	desc := `Shuts down or powers off running or paused VM(s)

  Used to shutdown or power off running or paused virtual machine(s) for a specific
  experiment.  The shutdown is not graceful and is equivalent to pulling the power cord`

	cmd := &cobra.Command{
		Use:               "shutdown <experiment name> [vm name]",
		Short:             "Shutdown a running or paused VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVMChange(cmd, args, dry, vmChange{
				check: checkVMRunsInMinimega, change: vm.Shutdown, desc: "shutdown", past: "shutdown",
			})
		},
	}

	addVMLabelFlag(cmd)

	dry = addDryRunFlag(cmd)

	return cmd
}

func newVMKillCmd() *cobra.Command {
	var dry dryRun

	desc := `Kill running or paused VM(s)

  Used to kill or delete running or paused virtual machine(s) for a specific
  experiment`

	cmd := &cobra.Command{
		Use:               "kill <experiment name> [vm name]",
		Short:             "Kill a running or paused VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return processVMChange(cmd, args, dry, vmChange{
				check: checkVMRunsInMinimega, change: vm.Kill, desc: "kill", past: "killed",
			})
		},
	}

	addVMLabelFlag(cmd)

	dry = addDryRunFlag(cmd)

	return cmd
}

// errBadInterfaceFlag is returned for a --interface value that is not
// <index>=<vlan>.
var errBadInterfaceFlag = errors.New("--interface must be <index>=<vlan>")

// vmInterfaceChange is a parsed --interface value of vm set. An empty vlan
// disconnects the interface.
type vmInterfaceChange struct {
	index int
	vlan  string
}

// parseVMInterfaceFlag parses a --interface value: an interface index, an
// equals sign, and a VLAN alias or ID, or nothing to disconnect it.
func parseVMInterfaceFlag(value string) (vmInterfaceChange, error) {
	index, vlanName, ok := strings.Cut(value, "=")
	if !ok {
		return vmInterfaceChange{}, fmt.Errorf("%w, not %q", errBadInterfaceFlag, value)
	}

	i, err := parseIfaceIndex(index)
	if err != nil {
		return vmInterfaceChange{}, fmt.Errorf("%w: %w", errBadInterfaceFlag, err)
	}

	if vlanName != strings.TrimSpace(vlanName) {
		return vmInterfaceChange{}, fmt.Errorf("%w: the VLAN %q has a space at an end", errBadInterfaceFlag, vlanName)
	}

	return vmInterfaceChange{index: i, vlan: vlanName}, nil
}

// vmSetFlags are the values of the vm set flags that the command line gives.
type vmSetFlags struct {
	cpu, mem, part *int
	disk           string
	dnb, snapshot  *bool
	host           *string
	labels         map[string]string
	appendLabels   bool
	labelsChanged  bool
	annotations    map[string]any
	iface          *vmInterfaceChange
}

// options returns the vm.Update options of the flags for the VM.
func (f vmSetFlags) options(expName, vmName string) []vm.UpdateOption {
	opts := []vm.UpdateOption{vm.UpdateExperiment(expName), vm.UpdateVM(vmName)}

	if f.cpu != nil {
		opts = append(opts, vm.UpdateWithCPU(*f.cpu))
	}

	if f.mem != nil {
		opts = append(opts, vm.UpdateWithMem(*f.mem))
	}

	if f.disk != "" {
		opts = append(opts, vm.UpdateWithDisk(f.disk))
	}

	if f.part != nil {
		opts = append(opts, vm.UpdateWithPartition(*f.part))
	}

	if f.dnb != nil {
		opts = append(opts, vm.UpdateWithDNB(*f.dnb))
	}

	if f.snapshot != nil {
		opts = append(opts, vm.UpdateWithSnapshot(*f.snapshot))
	}

	if f.host != nil {
		opts = append(opts, vm.UpdateWithHost(*f.host))
	}

	if f.labelsChanged {
		opts = append(opts, vm.UpdateWithTags(f.labels, f.appendLabels))
	}

	if f.annotations != nil {
		opts = append(opts, vm.UpdateWithAnnotations(f.annotations))
	}

	if f.iface != nil {
		opts = append(opts, vm.UpdateWithInterface(f.iface.index, f.iface.vlan))
	}

	return opts
}

// changedInt returns the value of the int flag name when the command line
// gives it.
func changedInt(cmd *cobra.Command, name string) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	v := mustGet(cmd.Flags().GetInt, name)

	return &v
}

// changedBool returns the value of the bool flag name when the command line
// gives it.
func changedBool(cmd *cobra.Command, name string) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	v := mustGet(cmd.Flags().GetBool, name)

	return &v
}

// exactStringArray returns the values of the string array flag name as the
// command line gives them, also a single empty value that GetStringArray drops.
func exactStringArray(cmd *cobra.Command, name string) []string {
	if values, ok := cmd.Flags().Lookup(name).Value.(pflag.SliceValue); ok {
		return values.GetSlice()
	}

	return mustGet(cmd.Flags().GetStringArray, name)
}

// parseVMSetFlags reads and checks the vm set flags of the command line.
func parseVMSetFlags(cmd *cobra.Command) (vmSetFlags, error) {
	var (
		f = vmSetFlags{
			cpu:           changedInt(cmd, "cpu"),
			mem:           changedInt(cmd, "mem"),
			part:          changedInt(cmd, "partition"),
			disk:          mustGet(cmd.Flags().GetString, "disk"),
			dnb:           changedBool(cmd, "do-not-boot"),
			snapshot:      changedBool(cmd, "snapshot"),
			host:          nil,
			labels:        nil,
			appendLabels:  mustGet(cmd.Flags().GetBool, "append-labels"),
			labelsChanged: cmd.Flags().Changed("label-changes") || cmd.Flags().Changed("append-labels"),
			annotations:   nil,
			iface:         nil,
		}
		err error
	)

	if f.cpu != nil && (*f.cpu < 1 || *f.cpu > 8) {
		return f, errors.New("cpus can only be 1-8")
	}

	if f.mem != nil && (*f.mem < 512 || *f.mem > 16384 || *f.mem%512 != 0) {
		return f, errors.New(memRangeMsg)
	}

	if f.part != nil && *f.part < 0 {
		return f, errors.New("the inject partition must be 0 or more")
	}

	if cmd.Flags().Changed("host") {
		host := mustGet(cmd.Flags().GetString, "host")
		f.host = &host
	}

	if f.labels, err = parseLabelFlags(mustGet(cmd.Flags().GetStringArray, "label-changes")); err != nil {
		return f, err
	}

	if cmd.Flags().Changed("annotation") {
		if f.annotations, err = parseNodeAnnotationFlags(exactStringArray(cmd, "annotation")); err != nil {
			return f, err
		}
	}

	if cmd.Flags().Changed("interface") {
		change, err := parseVMInterfaceFlag(mustGet(cmd.Flags().GetString, "interface"))
		if err != nil {
			return f, err
		}

		f.iface = &change
	}

	if f.cpu == nil && f.mem == nil && f.disk == "" && f.part == nil && f.dnb == nil && f.snapshot == nil &&
		f.host == nil && !f.labelsChanged && f.annotations == nil && f.iface == nil {
		return f, errors.New("no configuration values provided to set")
	}

	return f, nil
}

func newVMSetCmd() *cobra.Command {
	desc := `Set configuration value(s) for VM(s)

  Used to set one or more configuration values for virtual machine(s) in an
  experiment. Only flags that are explicitly provided will be applied.

  While the experiment is running, you can change only the labels
  (--label-changes, --append-labels), the annotations (--annotation) and the
  VLAN of an interface (--interface). Apps read the new annotations when the
  experiment starts again. --interface changes only a VM of a running
  experiment.

  --annotation replaces all of the annotations of the VM with the given ones.
  The values true and false become booleans, and a JSON list or object keeps
  its type. Other values, numbers too, stay text.

  --interface <index>=<vlan> connects the interface with that index to a VLAN
  alias or ID. --interface <index>= disconnects the interface.

  --partition 0 turns off disk injection for the VM, so the startup app sends
  its scripts through miniccc.`

	example := `
  phenix vm set <experiment name> <vm name> --cpu 2 --mem 4096
  phenix vm set <experiment name> <vm name> --host compute1
  phenix vm set <experiment name> <vm name> --annotation phenix/default-apps=false
  phenix vm set <experiment name> <vm name> --interface 0=EXP_1
  phenix vm set <experiment name> <vm name> --interface 1=
  phenix vm set <experiment name> --label role=server --label-changes tier=web --append-labels`

	cmd := &cobra.Command{
		Use:               "set <experiment name> [vm name]",
		Short:             "Set configuration value(s) for VM(s)",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: vmArgsCompletion,

		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmNames, err := vmTargetNamesForCommand(cmd, args)
			if err != nil {
				return err
			}

			flags, err := parseVMSetFlags(cmd)
			if err != nil {
				return err
			}

			if flags.iface != nil && !experiment.Running(expName) {
				return fmt.Errorf("--interface: %w", vm.ErrStoppedInterfaceUpdate)
			}

			for _, vmName := range vmNames {
				if err := vm.Update(flags.options(expName, vmName)...); err != nil {
					err := util.HumanizeError(err, "%s", "Unable to update the "+vmName+" VM")

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "vm updated", "vm", vmName, "exp", expName)
			}

			return nil
		},
	}

	cmd.Flags().IntP("cpu", "c", 0, "Number of VM CPUs (1-8 is valid)")
	cmd.Flags().IntP("mem", "m", 0, "Amount of memory in megabytes (a multiple of 512 from 512 to 16384)")
	cmd.Flags().StringP("disk", "d", "", "VM backing disk image file")
	cmd.Flags().IntP("partition", "p", 0, "Partition of the first disk to inject files into (0 turns off disk injection)")
	cmd.Flags().Bool("do-not-boot", false, "Set the do-not-boot flag for the VM")
	cmd.Flags().Bool("snapshot", false, "Set the snapshot (non-persistent) flag for the VM")
	cmd.Flags().String("host", "", "Cluster host to run the VM on (an empty value lets the scheduler choose the host)")
	cmd.Flags().StringArrayP(
		"label-changes",
		"L",
		nil,
		"VM label(s) to add or edit in key=value form (may be repeated)",
	)
	cmd.Flags().
		Bool("append-labels", false, "Append the provided labels to the VM's existing labels instead of replacing the labels")
	cmd.Flags().StringArray(
		"annotation",
		nil,
		"VM annotation in key=value form (may be repeated; replaces all of the annotations of the VM)",
	)
	cmd.Flags().String(
		"interface",
		"",
		"Connect an interface to a VLAN alias or ID as <index>=<vlan>, or disconnect it as <index>= (running experiments only)",
	)
	addVMLabelFlag(cmd)

	_ = cmd.RegisterFlagCompletionFunc("host", completeClusterHosts)
	_ = cmd.RegisterFlagCompletionFunc("disk", completeDisks)

	return cmd
}

func newVMNetConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <experiment name> <vm name> <iface index> <vlan id>",
		Short: "Connect a VM interface to a VLAN",
		ValidArgsFunction: completeArgs(
			completeExperiments(expRunning, false), completeVMs, completeVMInterfaceIndexes, completeVLANAliases,
		),
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

			iface, err := parseIfaceIndex(args[2])
			if err != nil {
				return err
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
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "disconnect <experiment name> <vm name> <iface index>",
		Short: "Disconnect a VM interface",
		ValidArgsFunction: completeArgs(
			completeExperiments(expRunning, false), completeVMs, completeVMInterfaceIndexes,
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != disconnectArgs {
				return errors.New("must provide an experiment name, VM name, and iface index")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			iface, err := parseIfaceIndex(args[2])
			if err != nil {
				return err
			}

			if dry.enabled() {
				if err := checkVMInterface(commandContext(cmd), expName, vmName, iface); err != nil {
					return util.HumanizeError(err, "%s", "Unable to disconnect the interface on the "+vmName+" VM").Humanized()
				}

				dry.skip("disconnect", fmt.Sprintf("interface %d of VM %s in experiment %s", iface, vmName, expName))

				return nil
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

	dry = addDryRunFlag(cmd)

	return cmd
}

// checkVMInterface returns an error unless minimega has the VM vmName of the
// experiment expName, and the VM has an interface with the index iface.
func checkVMInterface(ctx context.Context, expName, vmName string, iface int) error {
	if err := checkVMRunsInMinimega(ctx, expName, vmName); err != nil {
		return err
	}

	v, err := vm.Get(ctx, expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM %s in experiment %s: %w", vmName, expName, err)
	}

	if iface >= len(v.Networks) {
		return fmt.Errorf("VM %s has %d interfaces, so it has no interface %d: %w",
			vmName, len(v.Networks), iface, vm.ErrNoInterface)
	}

	return nil
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

	var stopDry, subnetDry dryRun

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Modify network packet captures for one or more VMs",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	var (
		vmIfaceCompletion = completeArgs(completeExperiments(expRunning, false), completeVMs, completeVMInterfaces)
		expCompletion     = completeArgs(completeExperiments(expRunning, false))
		vlanCompletion    = completeArgs(completeExperiments(expRunning, false), completeVLANAliases)
	)

	startVMCapture := &cobra.Command{
		Use:               "start <experiment name> <vm name> <iface name/index> <output file>",
		Short:             "Start a packet capture for a VM specifying the interface (by name or index) and using given output file as name of capture file",
		ValidArgsFunction: vmIfaceCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != startCaptureArgs {
				return errors.New(
					"must provide an experiment name, VM name, iface name/index, and output file",
				)
			}

			var (
				expName  = args[0]
				vmName   = args[1]
				ifaceArg = args[2]
				out      = args[3]
			)

			v, err := vm.Get(context.Background(), expName, vmName)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to get details for the "+vmName+" VM",
				)

				return err.Humanized()
			}

			iface, err := vm.ResolveInterface(v, ifaceArg)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to resolve interface "+ifaceArg+" on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			if err := vm.StartCaptureForVM(context.Background(), v, expName, vmName, iface, out); err != nil {
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
		Use:               "start-subnet <experiment name> <subnet>",
		Short:             "Start packet captures for the specified subnet",
		ValidArgsFunction: expCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < startSubnetArgs {
				return errors.New("must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = mustGet(cmd.Flags().GetString, "filter")
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("an invalid ipv4 subnet was detected: %v", subnet)
			}

			vmList, err := subnetFilterVMs(expName, filter)
			if err != nil {
				return err
			}

			vms, err := vm.CaptureSubnet(context.Background(), expName, subnet, vmList)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to start the packet capture(s) for "+subnet+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "subnet packet captures started", "subnet", subnet)

			printer.PrintTableOfSubnetCaptures(cmd.OutOrStdout(), vms)

			return nil
		},
	}

	startVLANCaptures := &cobra.Command{
		Use:   "start-vlan <experiment name> <vlan alias>",
		Short: "Start packet captures for the specified VLAN",
		Long: `Start packet captures for the specified VLAN

  Starts a packet capture on each interface on the VLAN of the running VMs.
  Give the VLAN alias in the case that minimega reports it, for example EXP_1.`,
		ValidArgsFunction: vlanCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < startVLANArgs {
				return errors.New("must provide an experiment name and VLAN")
			}

			var (
				expName = args[0]
				vlan    = args[1]
				filter  = mustGet(cmd.Flags().GetString, "filter")
			)

			vmList, err := subnetFilterVMs(expName, filter)
			if err != nil {
				return err
			}

			captures, err := vm.CaptureVLAN(context.Background(), expName, vlan, vmList)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to start the packet capture(s) on VLAN "+vlan+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "VLAN packet captures started", "vlan", vlan, "exp", expName)

			printer.PrintTableOfSubnetCaptures(cmd.OutOrStdout(), captures)

			return nil
		},
	}

	stopVMCaptures := &cobra.Command{
		Use:   "stop <experiment name> <vm name> [iface name/index]",
		Short: "Stop packet captures for the specified VM",
		Long: `Stop packet captures for the specified VM

  If an interface name or index is provided, only the packet capture running
  on that interface is stopped, leaving any other running captures for the VM
  untouched. Otherwise, all packet captures for the VM are stopped.`,
		ValidArgsFunction: vmIfaceCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != stopCaptureArgs && len(args) != stopCaptureIfaceArgs {
				return errors.New(
					"must provide an experiment name and VM name, and may optionally provide an iface name/index",
				)
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if stopDry.enabled() {
				return dryRunStopVMCaptures(cmd, stopDry, args)
			}

			if len(args) == stopCaptureIfaceArgs {
				ifaceArg := args[2]

				v, err := vm.Get(context.Background(), expName, vmName)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to get details for the "+vmName+" VM",
					)

					return err.Humanized()
				}

				iface, err := vm.ResolveInterface(v, ifaceArg)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to resolve interface "+ifaceArg+" on the "+vmName+" VM",
					)

					return err.Humanized()
				}

				if err := vm.StopCapture(context.Background(), expName, vmName, iface); err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to stop the packet capture on the "+vmName+" VM",
					)

					return err.Humanized()
				}

				plog.Info(
					plog.TypeSystem,
					"vm packet capture stopped",
					"iface",
					iface,
					"vm",
					vmName,
					"exp",
					expName,
				)

				return nil
			}

			err := vm.StopCaptures(context.Background(), expName, vmName)
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
		Use:               "stop-subnet <experiment name> <subnet>",
		Short:             "Stop all packet captures for the specified subnet",
		ValidArgsFunction: expCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < stopSubnetArgs {
				return errors.New("must provide an experiment name and subnet")
			}

			var (
				expName = args[0]
				subnet  = args[1]
				filter  = mustGet(cmd.Flags().GetString, "filter")
			)

			ipv4Re := regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)

			if !ipv4Re.MatchString(subnet) {
				return fmt.Errorf("an invalid subnet was detected: %v", subnet)
			}

			vmList, err := subnetFilterVMs(expName, filter)
			if err != nil {
				return err
			}

			if subnetDry.enabled() {
				return dryRunStopSubnetCaptures(
					cmd,
					subnetDry,
					expName,
					subnet,
					vmList,
					"Unable to stop the packet capture(s) on the "+subnet+" ",
				)
			}

			if _, err := vm.StopCaptureSubnet(context.Background(), expName, subnet, vmList); err != nil {
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

	cmd.AddCommand(startVMCapture)
	cmd.AddCommand(startSubnetCaptures)
	cmd.AddCommand(startVLANCaptures)
	cmd.AddCommand(stopVMCaptures)
	cmd.AddCommand(stopSubnetCaptures)
	cmd.AddCommand(newVMCaptureStopVLANCmd(vlanCompletion))
	cmd.AddCommand(newVMCaptureStopAllCmd(expCompletion))
	cmd.AddCommand(newVMCaptureStreamCmd())

	startSubnetCaptures.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")
	stopSubnetCaptures.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")
	startVLANCaptures.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")

	stopDry = addDryRunFlag(stopVMCaptures)
	subnetDry = addDryRunFlag(stopSubnetCaptures)

	return cmd
}

// newVMCaptureStopVLANCmd returns phenix vm capture stop-vlan, which
// completes its arguments with complete.
func newVMCaptureStopVLANCmd(complete cobra.CompletionFunc) *cobra.Command {
	var vlanDry dryRun

	cmd := &cobra.Command{
		Use:   "stop-vlan <experiment name> <vlan alias>",
		Short: "Stop the packet captures for the specified VLAN",
		Long: `Stop the packet captures for the specified VLAN

  Stops the packet capture on each interface on the VLAN of the running VMs.
  The captures on other interfaces of the VMs continue.`,
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < stopVLANArgs {
				return errors.New("must provide an experiment name and VLAN")
			}

			var (
				expName = args[0]
				vlan    = args[1]
				filter  = mustGet(cmd.Flags().GetString, "filter")
			)

			vmList, err := subnetFilterVMs(expName, filter)
			if err != nil {
				return err
			}

			if vlanDry.enabled() {
				captures, err := vm.VLANCapturesToStop(commandContext(cmd), expName, vlan, vmList)
				if err != nil {
					return util.HumanizeError(err, "%s", "Unable to stop the packet capture(s) on VLAN "+vlan+" ").Humanized()
				}

				skipCaptures(vlanDry, expName, captures)

				return nil
			}

			if _, err := vm.StopCaptureVLAN(context.Background(), expName, vlan, vmList); err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to stop the packet capture(s) on VLAN "+vlan+" ",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "VLAN packet captures stopped", "vlan", vlan, "exp", expName)

			return nil
		},
	}

	cmd.Flags().StringP("filter", "f", "", "Filter to restrict the list of VMs")

	vlanDry = addDryRunFlag(cmd)

	return cmd
}

// newVMCaptureStopAllCmd returns phenix vm capture stop-all, which completes
// its argument with complete.
func newVMCaptureStopAllCmd(complete cobra.CompletionFunc) *cobra.Command {
	var allDry dryRun

	cmd := &cobra.Command{
		Use:               "stop-all <experiment name>",
		Short:             "Stop all packet captures for the specified experiment",
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != stopAllArgs {
				return errors.New("must provide an experiment name")
			}

			expName := args[0]

			if allDry.enabled() {
				return dryRunStopSubnetCaptures(
					cmd,
					allDry,
					expName,
					"",
					[]string{},
					"Unable to stop the packet capture(s) for "+expName+" ",
				)
			}

			if _, err := vm.StopCaptureSubnet(context.Background(), expName, "", []string{}); err != nil {
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

	allDry = addDryRunFlag(cmd)

	return cmd
}

// dryRunStopVMCaptures checks the captures that phenix vm capture stop would
// stop for args, and writes them.
func dryRunStopVMCaptures(cmd *cobra.Command, dry dryRun, args []string) error {
	expName, vmName := args[0], args[1]

	var iface *int

	if len(args) == stopCaptureIfaceArgs {
		v, err := vm.Get(commandContext(cmd), expName, vmName)
		if err != nil {
			return util.HumanizeError(err, "%s", "Unable to get details for the "+vmName+" VM").Humanized()
		}

		index, err := vm.ResolveInterface(v, args[2])
		if err != nil {
			return util.HumanizeError(err, "%s", "Unable to resolve interface "+args[2]+" on the "+vmName+" VM").Humanized()
		}

		iface = &index
	}

	captures, err := vm.CapturesToStop(commandContext(cmd), expName, vmName, iface)
	if err != nil {
		msg := "Unable to stop the packet capture(s) on the " + vmName + " VM"
		if iface != nil {
			msg = "Unable to stop the packet capture on the " + vmName + " VM"
		}

		return util.HumanizeError(err, "%s", msg).Humanized()
	}

	skipCaptures(dry, expName, captures)

	return nil
}

// dryRunStopSubnetCaptures checks the captures that StopCaptureSubnet would
// stop, and writes them. msg is the error message of the real command.
func dryRunStopSubnetCaptures(cmd *cobra.Command, dry dryRun, expName, subnet string, vmList []string, msg string) error {
	vms, err := vm.SubnetCaptureVMs(commandContext(cmd), expName, subnet, vmList)
	if err != nil {
		return util.HumanizeError(err, "%s", msg).Humanized()
	}

	for _, v := range vms {
		skipCaptures(dry, expName, v.Captures)
	}

	return nil
}

// skipCaptures writes the dry-run line of each capture that a stop would stop.
func skipCaptures(dry dryRun, expName string, captures []mm.Capture) {
	for _, c := range captures {
		dry.skip("stop", fmt.Sprintf("the packet capture on interface %d of VM %s in experiment %s", c.Interface, c.VM, expName))
	}
}

// subnetFilterVMs returns the names of the VMs of experiment expName that the
// --filter value filter matches, to restrict capture start-subnet,
// stop-subnet, start-vlan and stop-vlan to them. It is empty for an empty filter, and an error when the
// filter matches no VM, as an empty list restricts nothing.
func subnetFilterVMs(expName, filter string) ([]string, error) {
	names, err := vm.FilterVMs(context.Background(), expName, filter)
	if err != nil && !errors.Is(err, vm.ErrNoFilterMatch) {
		err := util.HumanizeError(err, "%s", "Unable to retrieve a list of VMs for "+expName+" ")

		return nil, err.Humanized()
	}

	return names, err //nolint:wrapcheck // the filter's error as it is
}

func newVMCaptureStreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stream <experiment name> <vm name> <iface name/index>",
		Short: "Write a running packet capture to stdout as it is captured",
		Long: `Write a running packet capture to stdout as it is captured

  Writes the capture as a pcap stream: every packet captured so far (or, with
  --from-now, none of them), then each packet as it is captured, until the
  capture stops or the command is interrupted. Pipe it into Wireshark to
  watch it live:

    phenix vm capture stream <exp> <vm> <iface> | wireshark -k -i -`,
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false), completeVMs, completeVMInterfaces),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != streamCaptureArgs {
				return errors.New("must provide an experiment name, VM name, and iface name/index")
			}

			var (
				expName = args[0]
				vmName  = args[1]
				output  = mustGet(cmd.Flags().GetString, outputFileFlag)
				fromNow = mustGet(cmd.Flags().GetBool, "from-now")
			)

			v, err := vm.Get(context.Background(), expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to get details for the "+vmName+" VM")

				return err.Humanized()
			}

			iface, err := vm.ResolveInterface(v, args[2])
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to resolve interface "+args[2]+" on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			out := cmd.OutOrStdout()

			if !isStdoutPath(output) {
				f, err := os.Create(output)
				if err != nil {
					return fmt.Errorf("creating %s: %w", output, err)
				}

				defer f.Close()

				out = f
			} else if err := refuseTerminal(out, "a packet capture"); err != nil {
				return err
			}

			// a signal ends the stream rather than the process, so the cleanup
			// below runs; once one has, the next ends the process at once, even
			// while it waits on minimega
			ctx, stop := signal.NotifyContext(cmd.Context(), captureStreamStopSignals()...)
			defer stop()

			context.AfterFunc(ctx, stop)

			// with SIGPIPE ignored, a write to a pipe whose reader has gone
			// fails with EPIPE instead of killing the process
			signal.Ignore(syscall.SIGPIPE)

			tap, err := livecapture.Open(expName, vmName, iface)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to follow the packet capture on the "+vmName+" VM",
				)

				return err.Humanized()
			}

			// the process exits once streaming ends, before a released Tap's
			// idle expiry would remove the spool of a capture on another node.
			// From here a signal ends the process: CloseAll removes the spool
			// before it waits for a read in flight, which on a node that does
			// not answer lasts until the read times out.
			defer func() {
				stop()
				livecapture.CloseAll()
			}()

			err = tap.Stream(ctx, fromNow, func(b []byte) error {
				_, err := out.Write(b)

				return err //nolint:wrapcheck // the reader went away
			})

			// a signal, or the reader closing the pipe, is how streaming
			// normally ends
			if err != nil && ctx.Err() == nil && !errors.Is(err, syscall.EPIPE) {
				return fmt.Errorf("streaming the capture: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "Write the capture to this file instead of stdout")
	cmd.Flags().Bool("from-now", false, "Skip the packets captured before the command started")

	return cmd
}

// captureStreamStopSignals are the signals that end `phenix vm capture
// stream`. One the process was started ignoring, as nohup has it ignore a
// hangup, stays ignored: asking to be notified of it would stop that.
func captureStreamStopSignals() []os.Signal {
	stopOn := []os.Signal{syscall.SIGTERM}

	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			stopOn = append(stopOn, sig)
		}
	}

	return stopOn
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
			if res, err := vm.MemorySnapshot(context.Background(), expName, vmName, snapshot, cb); err != nil {
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

func newVMMountCmd() *cobra.Command {
	desc := `Mount a VM's filesystem on the headnode

  Mount the filesystem of a running virtual machine to a directory on the
  headnode. The VM must run the miniccc agent. phenix mounts the filesystem at
  <mount-dir>/<experiment name>/<vm name>. The default mount-dir is
  <base-dir.phenix>/mounts, which is /phenix/mounts with the default settings.
  Use the global --mount-dir flag (or the PHENIX_MOUNT_DIR environment
  variable) to change it.`

	cmd := &cobra.Command{
		Use:               "mount <experiment name> <vm name>",
		Short:             "Mount a VM's filesystem on the headnode",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != mountArgs {
				return errors.New("must provide an experiment name and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			mountPath, err := vm.Mount(expName, vmName)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to mount the "+vmName+" VM")

				return err.Humanized()
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VM %s in experiment %s mounted at: %s\n", vmName, expName, mountPath)
			plog.Info(
				plog.TypeSystem,
				"vm mounted",
				"vm",
				vmName,
				"exp",
				expName,
				"path",
				mountPath,
			)

			return nil
		},
	}

	return cmd
}

func newVMUnmountCmd() *cobra.Command {
	desc := `Unmount a VM's filesystem from the headnode

  Unmount a previously mounted virtual machine filesystem from the headnode.`

	cmd := &cobra.Command{
		Use:               "unmount <experiment name> <vm name>",
		Short:             "Unmount a VM's filesystem from the headnode",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != unmountArgs {
				return errors.New("must provide an experiment name and VM name")
			}

			var (
				expName = args[0]
				vmName  = args[1]
			)

			if err := vm.Unmount(expName, vmName); err != nil {
				err := util.HumanizeError(err, "%s", "Unable to unmount the "+vmName+" VM")

				return err.Humanized()
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VM %s in experiment %s unmounted\n", vmName, expName)
			plog.Info(plog.TypeSystem, "vm unmounted", "vm", vmName, "exp", expName)

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
	vmCmd.AddCommand(newVMSnapshotCmd())
	vmCmd.AddCommand(newVMCommitCmd())
	vmCmd.AddCommand(newVMMountCmd())
	vmCmd.AddCommand(newVMUnmountCmd())

	addCommandToRoot(vmCmd, true)
}
