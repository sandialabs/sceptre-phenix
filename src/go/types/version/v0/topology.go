package v0

import (
	ifaces "phenix/types/interfaces"
)

type TopologySpec struct {
	NodesF []*Node `json:"nodes" mapstructure:"nodes" structs:"nodes" yaml:"nodes"`
}

func (t *TopologySpec) IncludedTopologies() []string {
	return nil
}

func (t *TopologySpec) Nodes() []ifaces.NodeSpec {
	if t == nil {
		return nil
	}

	nodes := make([]ifaces.NodeSpec, len(t.NodesF))

	for i, n := range t.NodesF {
		nodes[i] = n
	}

	return nodes
}

func (t *TopologySpec) BootableNodes() []ifaces.NodeSpec {
	if t == nil {
		return nil
	}

	var bootable []ifaces.NodeSpec

	for _, n := range t.NodesF {
		var dnb bool

		if n.GeneralF.DoNotBootF != nil {
			dnb = *n.GeneralF.DoNotBootF
		}

		if dnb {
			continue
		}

		bootable = append(bootable, n)
	}

	return bootable
}

func (t *TopologySpec) SchedulableNodes(platform string) []ifaces.NodeSpec {
	if t == nil {
		return nil
	}

	var schedulable []ifaces.NodeSpec

	for _, n := range t.NodesF {
		if !n.External() {
			schedulable = append(schedulable, n)
		}
	}

	return schedulable
}

func (t TopologySpec) FindNodeByName(name string) ifaces.NodeSpec { //nolint:ireturn // interface
	for _, node := range t.NodesF {
		if node.GeneralF.HostnameF == name {
			return node
		}
	}

	return nil
}

// FindNodesWithLabels finds all nodes in the topology containing at least one
// of the labels provided. Take note that the node does not have to have all the
// labels provided, just one.
func (t TopologySpec) FindNodesWithLabels(labels ...string) []ifaces.NodeSpec {
	var nodes []ifaces.NodeSpec

	for _, n := range t.NodesF {
		for _, l := range labels {
			if _, ok := n.LabelsF[l]; ok {
				nodes = append(nodes, n)

				break
			}
		}
	}

	return nodes
}

func (TopologySpec) FindDelayedNodes() []ifaces.NodeSpec {
	return nil
}

func (t TopologySpec) FindNodesWithVLAN(vlan string) []ifaces.NodeSpec {
	var nodes []ifaces.NodeSpec

	for _, n := range t.NodesF {
		for _, i := range n.NetworkF.InterfacesF {
			if i.VLAN() == vlan {
				nodes = append(nodes, n)

				break
			}
		}
	}

	return nodes
}

func (t *TopologySpec) AddNode(typ, hostname string) ifaces.NodeSpec { //nolint:ireturn // interface
	n := &Node{ //nolint:exhaustruct // partial initialization
		TypeF: typ,
		GeneralF: &General{ //nolint:exhaustruct // partial initialization
			HostnameF: hostname,
		},
	}

	t.NodesF = append(t.NodesF, n)

	return n
}

func (t *TopologySpec) RemoveNode(hostname string) {
	idx := -1

	for i, node := range t.NodesF {
		if node.GeneralF.HostnameF == hostname {
			idx = i

			break
		}
	}

	if idx != -1 {
		t.NodesF = append(t.NodesF[:idx], t.NodesF[idx+1:]...)
	}
}

func (TopologySpec) HasCommands() bool {
	return false
}

func (t *TopologySpec) Init() error {
	t.SetDefaults()

	return nil
}

func (t *TopologySpec) SetDefaults() {
	for _, n := range t.NodesF {
		n.SetDefaults()
	}
}
