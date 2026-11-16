package mm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"golang.org/x/sync/errgroup"

	"phenix/util/mm/mmcli"
)

// listBridgesCommand lists the Open vSwitch bridges of the host, one per line. It only reads, unlike the minimega bridge command.
// The timeout stops ovs-vsctl from holding the shared minimega connection when its database is not running.
const listBridgesCommand = "ovs-vsctl --timeout=5 list-br"

// errNoBridgeList is the error when the answer of a host to the bridge
// listing holds nothing, for example when the connection to minimega is lost.
var errNoBridgeList = errors.New("minimega returned no answer to the bridge listing")

// GetBridges returns the Open vSwitch bridges of each named host, by host name, and changes nothing on a host.
// A host that fails fails the whole listing, so a host is never reported as without a bridge. Its commands give up
// once ctx is done.
func (m Minimega) GetBridges(ctx context.Context, hosts ...string) (map[string][]string, error) {
	return listBridges(hosts, m.IsHeadnode, func(line mmcli.ReadLine, cmd *mmcli.Command) chan *miniclient.Response {
		return mmcli.RunReadOnly(ctx, line, cmd)
	})
}

// listBridges lists the bridges of each host, up to mmcli.ReadOnlyLimit()
// hosts at once. It sends through run the one command that [bridgeListing]
// gives for that host, in the mmcli.ReadLine of that host. headnode reports whether a host is the head node. The
// error is that of the first host, in the order given, that failed.
func listBridges(
	hosts []string,
	headnode func(string) bool,
	run func(mmcli.ReadLine, *mmcli.Command) chan *miniclient.Response,
) (map[string][]string, error) {
	var (
		names = make([][]string, len(hosts))
		errs  = make([]error, len(hosts))
		reads errgroup.Group
	)

	reads.SetLimit(mmcli.ReadOnlyLimit())

	for i, host := range hosts {
		reads.Go(func() error {
			head := headnode(host)
			cmd := mmcli.NewCommand()
			cmd.Command = bridgeListing(host, head)

			line := mmcli.MeshRead
			if head {
				line = mmcli.HeadRead
			}

			names[i], errs[i] = bridgeNames(run(line, cmd))

			return nil
		})
	}

	_ = reads.Wait()

	bridges := make(map[string][]string, len(hosts))

	for i, host := range hosts {
		if errs[i] != nil {
			return nil, fmt.Errorf("listing the bridges of host %s: %w", host, errs[i])
		}

		bridges[host] = names[i]
	}

	return bridges, nil
}

// bridgeListing is the minimega command that lists the bridges of host:
// the shell command itself on the head node, else that command sent to the
// host over the mesh.
func bridgeListing(host string, headnode bool) string {
	if headnode {
		return "shell " + listBridgesCommand
	}

	return fmt.Sprintf("mesh send %s shell %s", host, listBridgesCommand)
}

// bridgeNames reads the sorted, unique bridge names from the responses. It reads every response, as the shared connection needs.
// It fails on an error response or on no response. A host without bridges answers with no names.
func bridgeNames(responses chan *miniclient.Response) ([]string, error) {
	var (
		names   []string
		errs    []error
		answers int
	)

	for response := range responses {
		for _, resp := range response.Resp {
			if resp.Error != "" {
				errs = append(errs, errors.New(strings.TrimSpace(resp.Error)))

				continue
			}

			answers++

			for name := range strings.FieldsSeq(resp.Response) {
				names = append(names, name)
			}
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if answers == 0 {
		return nil, errNoBridgeList
	}

	slices.Sort(names)

	return slices.Compact(names), nil
}

// bridgeNotFoundPrefix starts the error of a minimega that does not know the bridge to destroy.
const bridgeNotFoundPrefix = "bridge not found: "

// errNoBridgeDestroy is the error when a host gives no answer to the bridge
// destroy command, for example when the connection to minimega is lost.
var errNoBridgeDestroy = errors.New("minimega returned no answer to the bridge destroy")

// DestroyBridge destroys bridge on each named host with minimega's bridge destroy command, which also removes the
// taps, trunks and tunnels on it. minimega keeps an Open vSwitch bridge that it did not create. A host that does not
// know the bridge is not an error. It tries every host and returns the errors of all hosts that failed.
func (m Minimega) DestroyBridge(bridge string, hosts ...string) error {
	return destroyBridge(bridge, hosts, m.IsHeadnode, mmcli.Run)
}

// destroyBridge destroys bridge on each host. It sends through run the one
// command that [bridgeDestroy] gives for that host. headnode reports whether a
// host is the head node.
func destroyBridge(
	bridge string,
	hosts []string,
	headnode func(string) bool,
	run func(*mmcli.Command) chan *miniclient.Response,
) error {
	if err := ValidateArg(bridge); err != nil {
		return fmt.Errorf("destroying bridge: %w", err)
	}

	var errs []error

	for _, host := range hosts {
		if err := ValidateArg(host); err != nil {
			errs = append(errs, fmt.Errorf("destroying bridge %s on host %q: %w", bridge, host, err))

			continue
		}

		cmd := mmcli.NewCommand()
		cmd.Command = bridgeDestroy(bridge, host, headnode(host))

		if err := bridgeDestroyed(run(cmd)); err != nil {
			errs = append(errs, fmt.Errorf("destroying bridge %s on host %s: %w", bridge, host, err))
		}
	}

	return errors.Join(errs...)
}

// bridgeDestroy is the minimega command that destroys bridge on host: the
// command itself on the head node, else that command sent to the host over
// the mesh.
func bridgeDestroy(bridge, host string, headnode bool) string {
	if headnode {
		return "bridge destroy " + bridge
	}

	return fmt.Sprintf("mesh send %s bridge destroy %s", host, bridge)
}

// bridgeDestroyed reads every response to a bridge destroy command, as the shared connection needs. It fails on an
// error response other than "bridge not found", or on no response.
func bridgeDestroyed(responses chan *miniclient.Response) error {
	var (
		errs    []error
		answers int
	)

	for response := range responses {
		for _, resp := range response.Resp {
			answers++

			if resp.Error != "" && !strings.HasPrefix(resp.Error, bridgeNotFoundPrefix) {
				errs = append(errs, errors.New(strings.TrimSpace(resp.Error)))
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if answers == 0 {
		return errNoBridgeDestroy
	}

	return nil
}
