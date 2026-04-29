package soh

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/api/experiment"
	"phenix/api/vm"
)

const defaultEdgeLength = 150

var vlanAliasRegex = regexp.MustCompile(`(.*) \(\d*\)`)

func Get(expName, statusFilter string) (*Network, error) { //nolint:funlen // complex logic
	// Create an empty network
	network := new(Network)

	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("unable to get experiment %s: %w", expName, err)
	}

	// fetch all the VMs in the experiment
	vms, err := vm.List(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s VMs: %w", expName, err)
	}

	status := make(map[string]*HostState)

	if exp.Running() {
		network.ExpStarted = true
		network.SOHInitialized = Initialized(exp)
		network.SOHRunning = Running(exp)

		if app, ok := exp.Status.AppStatus()["soh"]; ok {
			data, ok2 := app.(map[string]any)
			if !ok2 {
				return nil, fmt.Errorf("unable to decode state of health details: %w", err)
			}

			var states []*HostState

			err = mapstructure.Decode(data["hosts"], &states)
			if err != nil {
				return nil, fmt.Errorf("unable to decode state of health host details: %w", err)
			}

			for _, state := range states {
				for _, s := range state.AllStates() {
					if s.Error != "" {
						state.Errors = true

						break
					}
				}

				status[state.Hostname] = state
			}
		}
	}

	// Internally use to track connections, VM's state, and whether or not the
	// VM is in minimega
	var (
		interfaces = make(map[string]int)
		ifaceCount = len(vms) + 1
		edgeCount  int
	)

	// Traverse the experiment VMs and create topology
	for _, vm := range vms {
		var vmState string

		/*
			An empty `vm.State` means the VM was not found in minimega. If the VM
			was supposed to boot (ie. DNB is false) and it's not in minimega then
			it's likely that someone has flushed it since deployment.
		*/
		switch vm.State {
		case "":
			if vm.DoNotBoot {
				vmState = "notboot"
			} else {
				vmState = "notdeploy"
			}
		case "EXTERNAL":
			vmState = "external"
		default:
			if vm.Running {
				vmState = "running"
			} else {
				vmState = "notrunning"
			}
		}

		if statusFilter != "" && vmState != statusFilter {
			continue
		}

		node := Node{ //nolint:exhaustruct // partial initialization
			ID:     vm.ID,
			Label:  vm.Name,
			Image:  vm.OSType,
			Tags:   vm.Tags,
			Status: vmState,
		}

		if vm.Type == "Router" || vm.Type == "Firewall" {
			node.Image = vm.Type
		}

		if soh, ok := status[vm.Name]; ok {
			node.SOH = soh
		}

		network.Nodes = append(network.Nodes, node)

		// Look at the VM's interface and create an interface node
		// Unless it is a member of the ignore list
		var vlanIgnoreList = []string{
			"MGMT",
			"MIRROR", // default in mirror app https://github.com/sandialabs/sceptre-phenix-apps/blob/main/src/go/cmd/phenix-app-mirror/types.go#L56
		}

		for _, vmIface := range vm.Networks {
			if match := vlanAliasRegex.FindStringSubmatch(vmIface); match != nil {
				vmIface = match[1]
			}

			if slices.Contains(vlanIgnoreList, strings.ToUpper(vmIface)) {
				continue
			}

			// If we got a new interface create the node
			if _, ok := interfaces[vmIface]; !ok {
				interfaces[vmIface] = ifaceCount

				ifaceNode := Node{ //nolint:exhaustruct // partial initialization
					ID:     ifaceCount,
					Label:  vmIface,
					Image:  "switch",
					Tags:   vm.Tags,
					Status: "ignore",
				}

				network.Nodes = append(network.Nodes, ifaceNode)
				ifaceCount++
			}

			// If already exists get interface's id and connect the node
			id := interfaces[vmIface]

			// create and edge for the node and interface
			edge := Edge{
				ID:     edgeCount,
				Source: vm.ID,
				Target: id,
				Length: defaultEdgeLength,
			}

			network.Edges = append(network.Edges, edge)
			edgeCount++
		}
	}

	return network, err
}

func GetFlows(name string) ([]string, [][]int, error) {
	exp, err := experiment.Get(name)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get experiment %s: %w", name, err)
	}

	if exp.Status == nil {
		return nil, nil, nil
	}

	soh, ok := exp.Status.AppStatus()["soh"]
	if !ok {
		return nil, nil, nil
	}

	status, ok := soh.(map[string]any)
	if !ok {
		return nil, nil, errors.New("invalid format for SoH app status")
	}

	capture, ok := status["packetCapture"]
	if !ok {
		return nil, nil, nil
	}

	packets := struct { //nolint:exhaustruct // partial initialization
		Hosts []string
		Flows [][]int
	}{}

	if err = mapstructure.Decode(capture, &packets); err != nil { //nolint:musttag // struct is used for decoding
		return nil, nil, errors.New("invalid format for SoH packet capture status")
	}

	return packets.Hosts, packets.Flows, nil
}
