package mm

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/activeshadow/libminimega/miniclient"

	"phenix/util/mm/mmcli"
)

// listBridgesCommand lists the Open vSwitch bridges of the host it runs on,
// one name per line, which are the bridges minimega puts VMs on. It only
// reads, unlike minimega's own bridge command, which creates minimega's
// default bridge and rewrites its bridges file when it lists. The timeout
// keeps ovs-vsctl from waiting forever for a database that is not running,
// which would hold minimega's shell command, and so the shared connection,
// as long.
const listBridgesCommand = "ovs-vsctl --timeout=5 list-br"

// errNoBridgeList is reported when a host's answer to the bridge listing
// holds nothing, as when the connection to minimega is lost.
var errNoBridgeList = errors.New("minimega returned no answer to the bridge listing")

// GetBridges returns the Open vSwitch bridges each named host has, by host
// name. The head node runs ovs-vsctl through minimega's shell command, and
// every other host is sent that command over the mesh, as
// [Minimega.MeshShell] sends shell commands; nothing else is sent, and
// nothing on a host is changed. A host that cannot be reached, or that
// answers with an error, fails the whole listing, so a host is never
// reported as lacking a bridge it was not asked about.
func (m Minimega) GetBridges(hosts ...string) (map[string][]string, error) {
	return listBridges(hosts, m.IsHeadnode, mmcli.Run)
}

// listBridges lists the bridges of each host, sending through run the one
// command [bridgeListing] gives for that host. headnode reports whether a
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

// bridgeNames reads the bridge names ovs-vsctl printed, one per line, from
// the responses to the listing, sorted and each once. It drains every
// response, which the shared minimega connection needs, and fails when a
// response is an error (minimega's shell command reports what the command
// wrote to standard error, or its failure, as one), or when there is no
// response at all. A host without bridges answers with no names.
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
