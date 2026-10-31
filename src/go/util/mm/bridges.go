package mm

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/activeshadow/libminimega/miniclient"

	"phenix/util/mm/mmcli"
)

// listBridgesCommand lists the Open vSwitch bridges of the host, one per line. It only reads, unlike the minimega bridge command.
// The timeout stops ovs-vsctl from holding the shared minimega connection when its database is not running.
const listBridgesCommand = "ovs-vsctl --timeout=5 list-br"

// errNoBridgeList is the error when the answer of a host to the bridge
// listing holds nothing, for example when the connection to minimega is lost.
var errNoBridgeList = errors.New("minimega returned no answer to the bridge listing")

// GetBridges returns the Open vSwitch bridges of each named host, by host name, and changes nothing on a host.
// A host that fails fails the whole listing, so a host is never reported as without a bridge.
func (m Minimega) GetBridges(hosts ...string) (map[string][]string, error) {
	return listBridges(hosts, m.IsHeadnode, mmcli.Run)
}

// listBridges lists the bridges of each host. It sends through run the one
// command that [bridgeListing] gives for that host. headnode reports whether a
// host is the head node.
func listBridges(
	hosts []string,
	headnode func(string) bool,
	run func(*mmcli.Command) chan *miniclient.Response,
) (map[string][]string, error) {
	bridges := make(map[string][]string, len(hosts))

	for _, host := range hosts {
		cmd := mmcli.NewCommand()
		cmd.Command = bridgeListing(host, headnode(host))

		names, err := bridgeNames(run(cmd))
		if err != nil {
			return nil, fmt.Errorf("listing the bridges of host %s: %w", host, err)
		}

		bridges[host] = names
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
