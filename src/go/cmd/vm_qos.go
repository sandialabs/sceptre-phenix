package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
)

const (
	qosVMArgs    = 2
	qosIfaceArgs = 3
)

// qosInterface resolves the interface argument, an index or a topology interface name, of the VM vmName of the
// experiment expName to its index, and returns it with the VM's interface names.
func qosInterface(ctx context.Context, expName, vmName, iface string) (int, []string, error) {
	v, err := vm.Get(ctx, expName, vmName)
	if err != nil {
		return 0, nil, fmt.Errorf("unable to get the %s VM: %w", vmName, err)
	}

	index, err := vm.ResolveInterface(v, iface)
	if err != nil {
		return 0, nil, fmt.Errorf("unable to find interface %s of the %s VM: %w", iface, vmName, err)
	}

	return index, v.IfaceNames, nil
}

// qosIfaceNames returns the topology interface names of the VM vmName, or none when phenix cannot read the VM.
func qosIfaceNames(ctx context.Context, expName, vmName string) []string {
	v, err := vm.Get(ctx, expName, vmName)
	if err != nil {
		return nil
	}

	return v.IfaceNames
}

// qosRow is the link quality of one VM interface, with the JSON names of
// GET /experiments/{exp}/vms/{name}/qos and the interface's name.
type qosRow struct {
	Interface int    `json:"interface" yaml:"interface"`
	Name      string `json:"name"      yaml:"name"`
	Loss      string `json:"loss"      yaml:"loss"`
	Delay     string `json:"delay"     yaml:"delay"`
	Rate      string `json:"rate"      yaml:"rate"`
}

// qosRows returns the rows of ifaces, with the interface's name from names.
func qosRows(ifaces []mm.InterfaceQoS, names []string) []qosRow {
	rows := make([]qosRow, 0, len(ifaces))

	for _, q := range ifaces {
		name := ""
		if q.Interface < len(names) {
			name = names[q.Interface]
		}

		rows = append(rows, qosRow{Interface: q.Interface, Name: name, Loss: q.Loss, Delay: q.Delay, Rate: q.Rate})
	}

	return rows
}

// printQoS writes a table of the link quality of each interface to w, with the interface's name from names.
func printQoS(w io.Writer, ifaces []mm.InterfaceQoS, names []string) {
	printQoSRows(w, qosRows(ifaces, names))
}

// printQoSRows writes a table of the rows to w.
func printQoSRows(w io.Writer, rows []qosRow) {
	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"Interface Index", "Interface Name", "Loss (%)", "Delay", "Rate"})

	for _, q := range rows {
		table.Append([]string{strconv.Itoa(q.Interface), q.Name, q.Loss, q.Delay, q.Rate})
	}

	table.Render()
}

func newVMQoSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qos",
		Short: "View or change the link quality of VM interfaces",
		Long: `View or change the link quality of VM interfaces

  The link quality is the packet loss, the delay or the rate limit that
  minimega applies to the traffic that a VM interface receives. A rate cannot
  go with a loss or a delay. The experiment must run. An interface is an
  index from 0 or an interface name of the topology.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVMQoSGetCmd(), newVMQoSSetCmd(), newVMQoSClearCmd())

	return cmd
}

func newVMQoSGetCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:               "get <experiment name> <vm name> [<interface>]",
		Short:             "Show the link quality of each interface of a VM",
		Example:           "  phenix vm qos get my-exp web\n  phenix vm qos get my-exp web IF0 -o json",
		Args:              argsWithUsage(cobra.RangeArgs(qosVMArgs, qosIfaceArgs)),
		ValidArgsFunction: qosArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				ctx             = commandContext(cmd)
				expName, vmName = args[0], args[1]
			)

			_, ifaces, err := vm.GetQoS(ctx, expName, vmName)
			if err != nil {
				return fmt.Errorf("unable to read the link quality of the %s VM: %w", vmName, err)
			}

			var rows []qosRow

			if len(args) < qosIfaceArgs {
				rows = qosRows(ifaces, qosIfaceNames(ctx, expName, vmName))
			} else {
				index, names, err := qosInterface(ctx, expName, vmName, args[2])
				if err != nil {
					return err
				}

				if index >= len(ifaces) {
					return fmt.Errorf("the %s VM has no interface %d: %w", vmName, index, vm.ErrNoInterface)
				}

				rows = qosRows(ifaces[index:index+1], names)
			}

			return out.printAPI(cmd, rows, outputViews{
				formatTable: func(w io.Writer) error {
					printQoSRows(w, rows)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

// qosArgsCompletion completes <experiment name> <vm name> <interface> of a
// running experiment.
func qosArgsCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeArgs(completeExperiments(expRunning, false), completeVMs, completeVMInterfaces)(cmd, args, toComplete)
}

func newVMQoSSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <experiment name> <vm name> <interface>",
		Short: "Replace the link quality of a VM interface",
		Long: `Replace the link quality of a VM interface

  Clears the link quality of the interface, then applies each setting given.
  Give a --loss, a --delay or both, or give a --rate alone.`,
		Example: `
  phenix vm qos set my-exp web 0 --loss 0.5 --delay 100ms
  phenix vm qos set my-exp web IF0 --rate 10mbit`,
		Args:              argsWithUsage(cobra.ExactArgs(qosIfaceArgs)),
		ValidArgsFunction: qosArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				ctx             = commandContext(cmd)
				expName, vmName = args[0], args[1]
				q               = mm.InterfaceQoS{
					Interface: 0,
					Loss:      mustGet(cmd.Flags().GetString, "loss"),
					Delay:     mustGet(cmd.Flags().GetString, "delay"),
					Rate:      mustGet(cmd.Flags().GetString, "rate"),
				}
			)

			if err := mm.ValidateQoS(q); err != nil {
				return err //nolint:wrapcheck // the reason is the answer
			}

			index, names, err := qosInterface(ctx, expName, vmName, args[2])
			if err != nil {
				return err
			}

			q.Interface = index

			_, applied, err := vm.SetQoS(ctx, expName, vmName, q)
			if err != nil {
				return fmt.Errorf("unable to set the link quality of the %s VM: %w", vmName, err)
			}

			plog.Info(plog.TypeSystem, "vm link quality changed", "exp", expName, "vm", vmName, "interface", index)
			printQoS(cmd.OutOrStdout(), []mm.InterfaceQoS{applied}, names)

			return nil
		},
	}

	cmd.Flags().String("loss", "", "Percent of packets to drop, from 0 up to 100, such as 0.5")
	cmd.Flags().String("delay", "", "Delay to add to each packet, in us, ms or s, such as 100ms")
	cmd.Flags().String("rate", "", "Rate limit, a whole number of kbit, mbit or gbit, such as 10mbit")

	return cmd
}

func newVMQoSClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <experiment name> <vm name> [<interface>]",
		Short: "Remove the link quality of a VM interface, or of all its interfaces",
		Long: `Remove the link quality of a VM interface, or of all its interfaces

  With an interface, removes the link quality of that interface. Without one,
  removes the link quality of each interface that has one.`,
		Example:           "  phenix vm qos clear my-exp web 0\n  phenix vm qos clear my-exp web",
		Args:              argsWithUsage(cobra.RangeArgs(qosVMArgs, qosIfaceArgs)),
		ValidArgsFunction: qosArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				ctx             = commandContext(cmd)
				expName, vmName = args[0], args[1]
				indexes         []int
			)

			if len(args) == qosIfaceArgs {
				index, _, err := qosInterface(ctx, expName, vmName, args[2])
				if err != nil {
					return err
				}

				indexes = append(indexes, index)
			} else {
				_, ifaces, err := vm.GetQoS(ctx, expName, vmName)
				if err != nil {
					return fmt.Errorf("unable to read the link quality of the %s VM: %w", vmName, err)
				}

				for _, q := range ifaces {
					if !q.Empty() {
						indexes = append(indexes, q.Interface)
					}
				}
			}

			for _, index := range indexes {
				if _, err := vm.ClearQoS(ctx, expName, vmName, index); err != nil {
					return fmt.Errorf("unable to clear the link quality of interface %d of the %s VM: %w", index, vmName, err)
				}

				plog.Info(plog.TypeSystem, "vm link quality cleared", "exp", expName, "vm", vmName, "interface", index)
			}

			return nil
		},
	}
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMQoSCmd())
}
