package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/util/mm"
)

func newHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Cluster host management",
		Long: `Cluster host management

  Shows the cluster nodes that minimega reports, as the Hosts page of the web
  UI does. The commands ask minimega in the CLI process, and do not need
  phenix ui to run.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newHostListCmd())

	return cmd
}

func newHostListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   commandList,
		Short: "Display a table of the cluster hosts",
		Long: `Display a table of the cluster hosts

  Shows each node with its CPUs, load, memory in MB, VMs and network traffic
  in MB/s. The commits are what the VMs on the node ask for. A node that is
  not schedulable gets no VMs from the schedulers. -o json and -o yaml print
  each node with the fields of GET /hosts.`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			hosts, _, err := mm.GetClusterHostsObserved(commandContext(cmd), false)
			if err != nil {
				return fmt.Errorf("listing cluster hosts: %w", err)
			}

			return out.printAPI(cmd, hosts, outputViews{
				formatTable: func(w io.Writer) error {
					if len(hosts) == 0 {
						fmt.Fprintln(w, "No cluster hosts")

						return nil
					}

					printHosts(w, hosts)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

// printHosts writes the cluster hosts as a table.
func printHosts(w io.Writer, hosts []mm.Host) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{
		"Name", "Head Node", "Schedulable", "CPUs", "CPU Commit", "Load", "Memory Used", "Memory Total", "Memory Commit",
		"VMs", "Tx", "Rx", "Uptime",
	})

	for _, h := range hosts {
		table.Append([]string{
			h.Name,
			strconv.FormatBool(h.Headnode),
			strconv.FormatBool(h.Schedulable),
			strconv.Itoa(h.CPUs),
			strconv.Itoa(h.CPUCommit),
			strings.Join(h.Load, " "),
			strconv.Itoa(h.MemUsed),
			strconv.Itoa(h.MemTotal),
			strconv.Itoa(h.MemCommit),
			strconv.Itoa(h.VMs),
			strconv.FormatFloat(h.Tx, 'f', 1, 64),
			strconv.FormatFloat(h.Rx, 'f', 1, 64),
			time.Duration(h.Uptime * float64(time.Second)).Round(time.Second).String(),
		})
	}

	table.Render()
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newHostCmd(), true)
}
