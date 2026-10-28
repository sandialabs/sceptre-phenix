package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/util/common"
	ft "phenix/web/forward/forwardtypes"
)

const (
	forwardVMArgs   = 2
	forwardDstArgs  = 3
	forwardVNC      = "vnc"
	forwardVNCPort  = 5900
	maxForwardPort  = 65535
	defaultFwdHost  = "127.0.0.1"
	forwardsHeldBy  = "port forwards exist only in the running server"
	maxHostnameSize = 253
)

var (
	// errBadForwardPort is returned for a port that is not a whole number from 1 to 65535.
	errBadForwardPort = errors.New("not a port")
	// errBadForwardHost is returned for a destination host that is not an IP address or a host name.
	errBadForwardHost = errors.New("not a host")
	// errNoForward is returned by a dry run of phenix vm forward remove for a forward that the server does not hold.
	errNoForward = errors.New("forward not found")

	// forwardHostRegex matches a DNS host name: labels of letters, digits and inner hyphens, joined by dots.
	forwardHostRegex = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// parseForwardPort parses a port argument or flag: a whole number from 1 to 65535.
func parseForwardPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > maxForwardPort || strconv.Itoa(port) != value {
		return 0, fmt.Errorf("%w: %q is not a whole number from 1 to %d", errBadForwardPort, value, maxForwardPort)
	}

	return port, nil
}

// parseForwardDestination parses the destination of a forward: a port, or
// vnc, in any case, for the VM's VNC console. It returns the value the API
// takes and the port the forward has.
func parseForwardDestination(value string) (string, int, error) {
	if strings.EqualFold(value, forwardVNC) {
		return "VNC", forwardVNCPort, nil
	}

	port, err := parseForwardPort(value)
	if err != nil {
		return "", 0, fmt.Errorf("%w, or vnc", err)
	}

	return strconv.Itoa(port), port, nil
}

// checkForwardHost checks the destination host of a forward, which minimega
// gets in a command: an IP address or a DNS host name.
func checkForwardHost(host string) error {
	if net.ParseIP(host) != nil || (len(host) <= maxHostnameSize && forwardHostRegex.MatchString(host)) {
		return nil
	}

	return fmt.Errorf("%w: %q is not an IP address or a host name", errBadForwardHost, host)
}

// forwardsPath is the API path of the port forwards of the VM vmName of the experiment expName.
func forwardsPath(expName, vmName string) string {
	return "/experiments/" + url.PathEscape(expName) + "/vms/" + url.PathEscape(vmName) + "/forwards"
}

func newVMForwardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forward",
		Short: "List, add, or remove the port forwards of a VM",
		Long: `List, add, or remove the port forwards of a VM

  A port forward is a minimega tunnel from a cluster port to a port that the
  VM reaches, which phenix-tunneler serves on the computer of its owner. The
  running phenix server holds the forwards, so these commands call it over
  its unix socket (--unix-socket). The server takes a request from the socket
  as the user global-admin, so a forward that the CLI adds has that owner,
  and the CLI removes only the forwards of that owner.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVMForwardListCmd(), newVMForwardAddCmd(), newVMForwardRemoveCmd())

	return cmd
}

// listForwards returns the port forwards of the VM vmName of the experiment
// expName that the server holds, of every owner.
func listForwards(ctx context.Context, expName, vmName string) ([]ft.Listener, error) {
	var body struct {
		Listeners []ft.Listener `json:"listeners"`
	}

	client := newServerClient(common.UnixSocket, forwardsHeldBy)
	if err := client.call(ctx, http.MethodGet, forwardsPath(expName, vmName), nil, nil, &body); err != nil {
		return nil, fmt.Errorf("unable to list the port forwards of the %s VM: %w", vmName, err)
	}

	return nonNil(body.Listeners), nil
}

func newVMForwardListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list <experiment name> <vm name>",
		Short: "List the port forwards of a VM",
		Long: `List the port forwards of a VM

  Lists the forwards of every owner. -o json and -o yaml print the forwards
  with the fields of GET /experiments/{exp}/vms/{name}/forwards.`,
		Example:           "  phenix vm forward list my-exp web\n  phenix vm forward list my-exp web -o json",
		Args:              argsWithUsage(cobra.ExactArgs(forwardVMArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false), completeVMs),
		RunE: func(cmd *cobra.Command, args []string) error {
			listeners, err := listForwards(commandContext(cmd), args[0], args[1])
			if err != nil {
				return err
			}

			return out.printAPI(cmd, listeners, outputViews{
				formatTable: func(w io.Writer) error {
					table := tablewriter.NewWriter(w)
					table.SetHeader([]string{"Source Port", "Destination Host", "Destination Port", "Owner"})

					for _, l := range listeners {
						table.Append([]string{strconv.Itoa(l.SrcPort), l.DstHost, strconv.Itoa(l.DstPort), l.Owner})
					}

					table.Render()

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newVMForwardAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <experiment name> <vm name> <destination port|vnc>",
		Short: "Add a port forward to a VM",
		Long: `Add a port forward to a VM

  Opens a minimega tunnel from a cluster port to the destination port on
  --host, as the cluster node of the VM reaches it. With vnc, the forward
  goes to the VNC console of the VM. --source-port is the port that
  phenix-tunneler listens on; it defaults to the destination port. An owner
  has at most one forward to each host and port of a VM.`,
		Example: `
  phenix vm forward add my-exp web 22
  phenix vm forward add my-exp web 80 --host 10.0.0.5 --source-port 8080
  phenix vm forward add my-exp web vnc`,
		Args: argsWithUsage(cobra.ExactArgs(forwardDstArgs)),
		ValidArgsFunction: completeArgs(
			completeExperiments(expRunning, false), completeVMs, completeValues(forwardVNC),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			dst, dstPort, err := parseForwardDestination(args[2])
			if err != nil {
				return err
			}

			host := mustGet(cmd.Flags().GetString, "host")
			if err := checkForwardHost(host); err != nil {
				return err
			}

			src := dstPort

			if value := mustGet(cmd.Flags().GetString, "source-port"); value != "" {
				if src, err = parseForwardPort(value); err != nil {
					return err
				}
			}

			query := url.Values{"src": {strconv.Itoa(src)}, "host": {host}, "dst": {dst}}

			client := newServerClient(common.UnixSocket, forwardsHeldBy)
			if err := client.call(commandContext(cmd), http.MethodPost, forwardsPath(args[0], args[1]), query, nil, nil); err != nil {
				return fmt.Errorf("unable to add a port forward to the %s VM: %w", args[1], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added a port forward from port %d to %s:%d of VM %s in experiment %s\n",
				src, host, dstPort, args[1], args[0])

			return nil
		},
	}

	cmd.Flags().String("host", defaultFwdHost, "Destination host, as the cluster node of the VM reaches it")
	cmd.Flags().String("source-port", "", "Port for phenix-tunneler to listen on (default the destination port)")

	return cmd
}

func newVMForwardRemoveCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "remove <experiment name> <vm name> <destination port|vnc>",
		Short: "Remove a port forward of a VM",
		Long: `Remove a port forward of a VM

  Closes the forward of the owner global-admin to the destination port on
  --host, and its minimega tunnel.`,
		Example:           "  phenix vm forward remove my-exp web 22",
		Args:              argsWithUsage(cobra.ExactArgs(forwardDstArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false), completeVMs, completeForwardPorts),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dstPort, err := parseForwardDestination(args[2])
			if err != nil {
				return err
			}

			host := mustGet(cmd.Flags().GetString, "host")
			if err := checkForwardHost(host); err != nil {
				return err
			}

			if dry.enabled() {
				return dryRunRemoveForward(cmd, dry, args[0], args[1], host, dstPort)
			}

			query := url.Values{"host": {host}, "dst": {strconv.Itoa(dstPort)}}

			client := newServerClient(common.UnixSocket, forwardsHeldBy)
			if err := client.call(commandContext(cmd), http.MethodDelete, forwardsPath(args[0], args[1]), query, nil, nil); err != nil {
				return fmt.Errorf("unable to remove the port forward of the %s VM: %w", args[1], err)
			}

			return nil
		},
	}

	cmd.Flags().String("host", defaultFwdHost, "Destination host of the forward")

	dry = addDryRunFlag(cmd)

	return cmd
}

// dryRunRemoveForward checks that the server holds the forward of the owner
// global-admin to host:port of the VM, as a removal needs, and writes it.
func dryRunRemoveForward(cmd *cobra.Command, dry dryRun, expName, vmName, host string, port int) error {
	listeners, err := listForwards(commandContext(cmd), expName, vmName)
	if err != nil {
		return err
	}

	for _, l := range listeners {
		if l.Owner == socketUser && l.DstHost == host && l.DstPort == port {
			dry.skip("remove", fmt.Sprintf("the port forward from port %d to %s:%d of VM %s in experiment %s",
				l.SrcPort, host, port, vmName, expName))

			return nil
		}
	}

	return fmt.Errorf("unable to remove the port forward of the %s VM: %w: the owner %s has no forward to %s:%d",
		vmName, errNoForward, socketUser, host, port)
}

// completeForwardPorts completes the destination ports of the forwards of the
// VM in the second argument that the owner global-admin has, and vnc.
func completeForwardPorts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ports := []string{forwardVNC}

	if len(args) >= forwardVMArgs {
		ctx, cancel := context.WithTimeout(commandContext(cmd), completionTimeout)
		defer cancel()

		if listeners, err := listForwards(ctx, args[0], args[1]); err == nil {
			for _, l := range listeners {
				if l.Owner == socketUser && l.DstPort != forwardVNCPort {
					ports = append(ports, strconv.Itoa(l.DstPort))
				}
			}
		}
	}

	return completionMatches(ports, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMForwardCmd())
}
