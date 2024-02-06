package vm

import (
	"fmt"

	"phenix/api/experiment"
	"phenix/util/cache"
	"phenix/util/mm"

	"golang.org/x/exp/slices"
)

type topology struct {
	Nodes   []mm.VM `json:"nodes"`
	Edges   []edge  `json:"edges"`
	Running bool    `json:"running"`
}

type edge struct {
	ID     int `json:"id"`
	Source int `json:"source"`
	Target int `json:"target"`
	Length int `json:"length"`
}

func Topology(exp string, ignore []string) (topology, error) {
	vms, err := List(exp)
	if err != nil {
		return topology{}, fmt.Errorf("getting VMs: %w", err)
	}

	var (
		networks = make(map[string]mm.VM)
		search   TopologySearch

		cacheKey = fmt.Sprintf("experiment|%s|search", exp)
		cached   bool

		nodes  []mm.VM
		nodeID int
		edges  []edge
		edgeID int
	)

	if val, ok := cache.Get(cacheKey); ok {
		search = val.(TopologySearch)
		cached = true
	}

	for _, vm := range vms {
		node := vm.Copy()
		node.ID = nodeID

		nodes = append(nodes, node)
		nodeID++

		if !cached {
			search.AddHostname(node.Name, node.ID)
			search.AddDisk(node.Disk, node.ID)
			search.AddType(node.Type, node.ID)
			search.AddOSType(node.OSType, node.ID)

			for k, v := range node.Labels {
				search.AddLabel(k, v, node.ID)
			}

			for k := range node.Annotations {
				search.AddAnnotation(k, node.ID)
			}
		}

		for i, iface := range vm.Networks {
			if match := vlanAliasRegex.FindStringSubmatch(iface); match != nil {
				iface = match[1]
			}

			if slices.Contains(ignore, iface) {
				continue
			}

			if !cached {
				// TODO: what if these change during an experiment (e.g., via user updates)?
				search.AddVLAN(iface, node.ID)
				search.AddIP(vm.IPv4[i], node.ID)
			}

			network, ok := networks[iface]
			if !ok { // create new node for VLAN network switch
				network = mm.VM{ID: nodeID, Name: iface, Type: "Switch", Networks: []string{iface}}
				networks[iface] = network

				nodes = append(nodes, network)
				nodeID++
			}

			edges = append(edges, edge{ID: edgeID, Source: node.ID, Target: network.ID, Length: 150})
			edgeID++
		}
	}

	if !cached {
		// TODO: cache with expire?
		cache.Set(cacheKey, search)
	}

	topo := topology{Nodes: nodes, Edges: edges}

	if exp, err := experiment.Get(exp); err == nil {
		topo.Running = exp.Running()
	}

	return topo, nil
}
