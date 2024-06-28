package soh

import (
	"fmt"
	"regexp"
	"strings"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/app"

	"github.com/mitchellh/mapstructure"
)

var vlanAliasRegex = regexp.MustCompile(`(.*) \(\d*\)`)

func Get(expName, statusFilter string) (*Network, error) {
	// Create an empty network
	network := &Network{
		Nodes: []Node{},
		Edges: []Edge{},
	}

	// Create structure to format nodes' font
	font := Font{
		Color: "whitesmoke",
		Align: "center",
	}

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
			data, ok := app.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("unable to decode state of health details: %w", err)
			}

			var states []*HostState

			if err := mapstructure.Decode(data["hosts"], &states); err != nil {
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
		vmIDs      = make(map[string]int)
		interfaces = make(map[string]int)
		ifaceCount = len(vms) + 1
		edgeCount  int
	)

	// Traverse the experiment VMs and create topology
	for _, vm := range vms {
		vmIDs[vm.Name] = vm.ID

		var vmState string

		/*
			An empty `vm.State` means the VM was not found in minimega. If the VM
			was supposed to boot (ie. DNB is false) and it's not in minimega then
			it's likely that someone has flushed it since deployment.
		*/
		if vm.State == "" {
			if vm.DoNotBoot {
				vmState = "notboot"
			} else {
				vmState = "notdeploy"
			}
		} else if vm.State == "EXTERNAL" {
			vmState = "external"
		} else {
			if vm.Running {
				vmState = "running"
			} else {
				vmState = "notrunning"
			}
		}

		if statusFilter != "" && vmState != statusFilter {
			continue
		}

		node := Node{
			ID:     vm.ID,
			Label:  vm.Name,
			Image:  vm.OSType,
			Fonts:  font,
			Status: vmState,
		}

		if vm.Type == "Router" || vm.Type == "Firewall" {
			node.Image = vm.Type
		}

		if soh, ok := status[vm.Name]; ok {
			node.SOH = soh
		}

		network.Nodes = append(network.Nodes, node)

		// Look at the VM's interface and create an interface node, ignoring MGMT
		// VLAN
		for _, vmIface := range vm.Networks {
			if match := vlanAliasRegex.FindStringSubmatch(vmIface); match != nil {
				vmIface = match[1]
			}

			if strings.ToUpper(vmIface) == "MGMT" {
				continue
			}

			// If we got a new interface create the node
			if _, ok := interfaces[vmIface]; !ok {
				interfaces[vmIface] = ifaceCount

				node := Node{
					ID:     ifaceCount,
					Label:  vmIface,
					Image:  "switch",
					Fonts:  font,
					Status: "ignore",
				}

				network.Nodes = append(network.Nodes, node)
				ifaceCount++
			}

			// If already exists get interface's id and connect the node
			id := interfaces[vmIface]

			// create and edge for the node and interface
			edge := Edge{
				ID:     edgeCount,
				Source: vm.ID,
				Target: id,
				Length: 150,
			}

			network.Edges = append(network.Edges, edge)
			edgeCount++
		}
	}

	// Check to see if a scenario exists for this experiment and if it contains a
	// "serial" app. If so, add edges for all the serial connections.
	for _, a := range exp.Apps() {
		if a.Name() == "serial" {
			var config app.SerialConfig

			if err := a.ParseMetadata(&config); err != nil {
				continue // TODO: handle this better? Like warn the user perhaps?
			}

			for _, conn := range config.Connections {
				// create edge for serial connection
				edge := Edge{
					ID:     edgeCount,
					Source: vmIDs[conn.Src],
					Target: vmIDs[conn.Dst],
					Length: 150,
					Type:   "serial",
				}

				network.Edges = append(network.Edges, edge)
				edgeCount++
			}
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

	status, ok := soh.(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("invalid format for SoH app status")
	}

	capture, ok := status["packetCapture"]
	if !ok {
		return nil, nil, nil
	}

	var packets = struct {
		Hosts []string
		Flows [][]int
	}{}

	if err := mapstructure.Decode(capture, &packets); err != nil {
		return nil, nil, fmt.Errorf("invalid format for SoH packet capture status")
	}

	return packets.Hosts, packets.Flows, nil
}
