package v0

import (
	ifaces "phenix/types/interfaces"
)

type TopologySpec struct {
	NodesF []*Node `json:"nodes" yaml:"nodes" structs:"nodes" mapstructure:"nodes"`
}

func (this *TopologySpec) Nodes() []ifaces.NodeSpec {
	if this == nil {
		return nil
	}

	nodes := make([]ifaces.NodeSpec, len(this.NodesF))

	for i, n := range this.NodesF {
		nodes[i] = n
	}

	return nodes
}

func (this TopologySpec) FindNodeByName(name string) ifaces.NodeSpec {
	for _, node := range this.NodesF {
		if node.GeneralF.HostnameF == name {
			return node
		}
	}

	return nil
}

// FindNodesWithLabels finds all nodes in the topology containing at least one
// of the labels provided. Take note that the node does not have to have all the
// labels provided, just one.
func (this TopologySpec) FindNodesWithLabels(labels ...string) []ifaces.NodeSpec {
	var nodes []ifaces.NodeSpec

	for _, n := range this.NodesF {
		for _, l := range labels {
			if _, ok := n.LabelsF[l]; ok {
				nodes = append(nodes, n)
				break
			}
		}
	}

	return nodes
}

func (this *TopologySpec) AddNode(typ, hostname string) ifaces.NodeSpec {
	n := &Node{
		TypeF: typ,
		GeneralF: &General{
			HostnameF: hostname,
		},
	}

	this.NodesF = append(this.NodesF, n)

	return n
}

func (this *TopologySpec) RemoveNode(hostname string) {
	idx := -1

	for i, node := range this.NodesF {
		if node.GeneralF.HostnameF == hostname {
			idx = i
			break
		}
	}

	if idx != -1 {
		this.NodesF = append(this.NodesF[:idx], this.NodesF[idx+1:]...)
	}
}

func (this *TopologySpec) Init() error {
	this.SetDefaults()
	return nil
}

func (this *TopologySpec) SetDefaults() {
	for _, n := range this.NodesF {
		n.SetDefaults()
	}
}
