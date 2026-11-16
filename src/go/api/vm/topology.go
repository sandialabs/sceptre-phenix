package vm

import (
	"context"
	"fmt"
	"slices"
	"time"

	"phenix/api/experiment"
	"phenix/types"
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
// kept. The index is keyed by the experiment's store revision, which every
// edit, start and stop changes, so the bound only limits the memory it holds;
// VM details minimega changes without a store write (an IP a VM takes by DHCP)
// show once it expires.
const topologySearchTTL = 30 * time.Second

// topologySearchKey is the cache key of the experiment's search index.
func topologySearchKey(exp *types.Experiment) string {
	return fmt.Sprintf("experiment|%s|search|r%d", exp.Metadata.Name, exp.Metadata.Revision)
}

// TopologySearchIndex returns the experiment's topology search index, from the
// cache while the experiment is unchanged, or built (and cached) anew.
func TopologySearchIndex(exp string) (TopologySearch, error) {
	stored, err := experiment.Get(exp)
	if err != nil {
		return TopologySearch{}, fmt.Errorf("getting experiment %s: %w", exp, err)
	}

	if val, ok := cache.Get(topologySearchKey(stored)); ok {
		if search, ok := val.(TopologySearch); ok {
			return search, nil
		}
	}

	_, search, err := topologyFor(context.Background(), stored, nil)

	return search, err
}

// Topology returns the experiment's network topology. Once ctx is done, its
// minimega commands give up (see mm.Context) and it returns an error wrapping
// ctx's error.
func Topology(ctx context.Context, exp string, ignore []string) (topology, error) {
	stored, err := experiment.Get(exp)
	if err != nil {
		return topology{}, fmt.Errorf("getting VMs: getting experiment %s: %w", exp, err)
	}

	topo, _, err := topologyFor(ctx, stored, ignore)

	return topo, err
}

// topologyFor is Topology for an experiment read from the store, also
// returning its search index, which is cached unless ctx is done. It uses the
// cached index, or builds the index anew when the cached value is not an index.
// The index is of the whole topology, so with an ignore list it returns an
// empty index and does not read or write the cache.
func topologyFor(ctx context.Context, stored *types.Experiment, ignore []string) (topology, TopologySearch, error) {
	vms := ListFor(ctx, stored)
	if err := ctx.Err(); err != nil {
		return topology{}, TopologySearch{}, fmt.Errorf("listing VMs of experiment %s: %w", stored.Metadata.Name, err)
	}

	var (
		networks = make(map[string]mm.VM)
		search   TopologySearch

		cacheKey = topologySearchKey(stored)
		// node IDs change when VLANs are ignored, so only the whole topology gives the index
		skip = len(ignore) > 0

		// empty lists, never null
		nodes  = []mm.VM{}
		nodeID int
		edges  = []edge{}
		edgeID int
	)

	// a cached value of another type is not trusted, so the index is built and cached anew
	if val, ok := cache.Get(cacheKey); ok && !skip {
		search, skip = val.(TopologySearch)
	}

	for _, vm := range vms {
		node := vm.Copy()
		node.ID = nodeID

		nodes = append(nodes, node)
		nodeID++

		if !skip {
			search.AddHostname(node.Name, node.ID)
			search.AddDisk(node.Disk, node.ID)
			search.AddType(node.Type, node.ID)
			search.AddOSType(node.OSType, node.ID)

			// ListFor does not set Labels or Annotations, so read them from the topology node.
			if spec := stored.Spec.Topology().FindNodeByName(node.Name); spec != nil {
				for k, v := range spec.Labels() {
					search.AddLabel(k, v, node.ID)
				}

				for k := range spec.Annotations() {
					search.AddAnnotation(k, node.ID)
				}
			}
		}

		for i, iface := range vm.Networks {
			if match := vlanAliasRegex.FindStringSubmatch(iface); match != nil {
				iface = match[1]
			}

			if slices.Contains(ignore, iface) {
				continue
			}

			if !skip {
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

	if !skip {
		_ = cache.SetWithExpire(cacheKey, search, topologySearchTTL)
	}

	return topology{Nodes: nodes, Edges: edges, Running: stored.Running()}, search, nil
}
