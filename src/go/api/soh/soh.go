package soh

import (
	"fmt"
	"regexp"
	"strings"

	"phenix/api/experiment"
	"phenix/api/vm"

	"github.com/mitchellh/mapstructure"
)

var vlanAliasRegex = regexp.MustCompile(`(.*) \(\d*\)`)

func Get(expName, statusFilter string) (*Network, error) {
	// Create an empty network
	network := new(Network)

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
		network.Started = true

		if app, ok := exp.Status.AppStatus()["soh"]; ok {
			data, ok := app.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("unable to decode state of health details: %w", err)
			}

			var statuses []*HostState

			if err := mapstructure.Decode(data["hosts"], &statuses); err != nil {
				return nil, fmt.Errorf("unable to decode state of health host details: %w", err)
			}

			for _, s := range statuses {
				status[s.Hostname] = s
			}
		}
	}

	// Internally use to track connections, VM's state, and whether or not the
	// VM is in minimega
	var (
		interfaces      = make(map[string]int)
		ifaceCount      = len(vms) + 1
		edgeCount       int
		runningCount    int
		notRunningCount int
		notDeployCount  int
		notBootCount    int
	)

	// Traverse the experiment VMs and create topology
	for _, vm := range vms {
		var vmState string

		if vm.Running {
			vmState = "running"
			runningCount++
		} else {
			vmState = "notrunning"
			notRunningCount++
		}

		/*
			An empty `vm.State` means the VM was not found in minimega. If the VM
			was supposed to boot (ie. DNB is false) and it's not in minimega then
			it's likely that someone has flushed it since deployment.
		*/
		if vm.State == "" {
			if vm.DoNotBoot == true {
				vmState = "notboot"
				notBootCount++
			} else {
				vmState = "notdeploy"
				notDeployCount++
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
					Image:  "Switch",
					Fonts:  font,
					Status: "ignore",
				}

				network.Nodes = append(network.Nodes, node)
				ifaceCount++
			}

			// If already exists get interface's id and connect the node
			id, _ := interfaces[vmIface]

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

	network.RunningCount = runningCount
	network.NotRunningCount = notRunningCount
	network.NotBootCount = notBootCount
	network.NotDeployCount = notDeployCount
	network.TotalCount = runningCount + notRunningCount + notBootCount + notDeployCount

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
