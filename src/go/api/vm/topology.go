package vm

import (
	"fmt"
	"slices"
	"time"

	"phenix/api/experiment"
	"phenix/util/cache"
	"phenix/util/mm"
)

const defaultEdgeLength = 150

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

// topologySearchTTL bounds how long an experiment's topology search index is
// reused. VM details (VLANs, IPs, disks) change when the experiment starts,
// stops, or its VMs are edited, and nothing invalidates the index then.
const topologySearchTTL = 30 * time.Second

func Topology(exp string, ignore []string) (topology, error) {
	stored, err := experiment.Get(exp)
	if err != nil {
		return topology{}, fmt.Errorf("getting VMs: getting experiment %s: %w", exp, err)
	}

	vms := ListFor(stored)

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
		search, _ = val.(TopologySearch)
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
				//nolint:exhaustruct // partial initialization
				network = mm.VM{ID: nodeID, Name: iface, Type: "Switch", Networks: []string{iface}}
				networks[iface] = network

				nodes = append(nodes, network)
				nodeID++
			}

			edges = append(
				edges,
				edge{ID: edgeID, Source: node.ID, Target: network.ID, Length: defaultEdgeLength},
			)
			edgeID++
		}
	}

	if !cached {
		_ = cache.SetWithExpire(cacheKey, search, topologySearchTTL)
	}

	return topology{Nodes: nodes, Edges: edges, Running: stored.Running()}, nil
}
