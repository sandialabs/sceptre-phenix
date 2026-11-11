package scheduler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

func init() { //nolint:gochecknoinits // scheduler registration
	schedulers["subnet-compute"] = new(subnetCompute)
}

type subnetCompute struct{}

func (subnetCompute) Init(...Option) error {
	return nil
}

func (subnetCompute) Name() string {
	return "subnet-compute"
}

func (subnetCompute) Schedule(ctx context.Context, spec ifaces.ExperimentSpec) ([]string, error) {
	if len(spec.Topology().Nodes()) == 0 {
		return nil, errors.New("no VMs defined for experiment")
	}

	cluster, err := schedulableHosts(ctx, false)
	if err != nil {
		return nil, err
	}

	read := slices.Clone(cluster)
	warnings := keptPinWarnings(spec, read)

	// map VLAN aliases (key) to one (or more) cluster hosts
	vlans := make(map[string][]string)

	// Update VLAN alias map and cluster host VM count to account for VMs manually
	// scheduled before sorting hosts by VM count below, in VM name order so the
	// hosts of a VLAN are always tried in the same order.
	for _, node := range slices.Sorted(maps.Keys(spec.Schedules())) {
		host := spec.Schedules()[node]

		if h := cluster.FindHostByName(host); h != nil {
			if n := spec.Topology().FindNodeByName(node); n != nil && !n.External() {
				_ = cluster.IncrHostMemCommit(host, n.Hardware().Memory())

				if len(n.Network().Interfaces()) == 0 {
					continue
				}

				vlan := n.Network().Interfaces()[0].VLAN()

				hosts := vlans[vlan]
				hosts = append(hosts, h.Name)
				vlans[vlan] = hosts
			}
		}
	}

	cluster.SortByCommittedMem(true)

	// the VMs without interfaces, placed after the others
	var unconnected []ifaces.NodeSpec

	for _, node := range spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		if _, ok := spec.Schedules()[node.General().Hostname()]; ok {
			continue
		}

		if len(node.Network().Interfaces()) == 0 {
			unconnected = append(unconnected, node)

			continue
		}

		var scheduled *mm.Host

		vlan := node.Network().Interfaces()[0].VLAN()

		if hosts, ok := vlans[vlan]; ok {
			for _, name := range hosts {
				if host := cluster.FindHostByName(name); host != nil {
					if host.MemCommit+node.Hardware().Memory() <= host.MemTotal {
						scheduled = host

						break
					}
				}
			}
		}

		if scheduled == nil {
			if len(cluster) == 0 {
				return nil, fmt.Errorf("scheduling VM %s: %w", node.General().Hostname(), ErrNoSchedulableHosts)
			}

			// VM's VLAN alias not mapped to a cluster host yet, so use round-robin
			// approach to find a cluster host to map it to.
			scheduled = &cluster[0]

			hosts := vlans[vlan]
			hosts = append(hosts, scheduled.Name)
			vlans[vlan] = hosts
		}

		spec.Schedules()[node.General().Hostname()] = scheduled.Name

		_ = cluster.IncrHostMemCommit(scheduled.Name, node.Hardware().Memory())

		cluster.SortByCommittedMem(true)
	}

	if err := placeUnconnected(spec, cluster, unconnected); err != nil {
		return nil, err
	}

	return append(warnings, overcommitWarnings(spec, read)...), nil
}

// placeUnconnected puts each of vms, the VMs without interfaces that have no
// host, on the host of cluster that holds the most VMs of spec, pinned or
// placed, with ties taken by host name. When that host does not have the
// memory for the VM, it takes the next host in that order that has it, and
// the first host when none has it. It counts each VM that it places, on the
// host and in the host's committed memory.
func placeUnconnected(spec ifaces.ExperimentSpec, cluster mm.Hosts, vms []ifaces.NodeSpec) error {
	if len(vms) == 0 {
		return nil
	}

	if len(cluster) == 0 {
		return fmt.Errorf("scheduling VM %s: %w", vms[0].General().Hostname(), ErrNoSchedulableHosts)
	}

	held := make(map[string]int)

	for vm, host := range spec.Schedules() {
		if node := spec.Topology().FindNodeByName(vm); node != nil && !node.External() {
			held[host]++
		}
	}

	for _, vm := range vms {
		order := slices.Clone(cluster)
		slices.SortFunc(order, func(a, b mm.Host) int {
			if byCount := held[b.Name] - held[a.Name]; byCount != 0 {
				return byCount
			}

			return strings.Compare(a.Name, b.Name)
		})

		memory := vm.Hardware().Memory()
		chosen := order[0].Name

		for _, host := range order {
			if host.MemCommit+memory <= host.MemTotal {
				chosen = host.Name

				break
			}
		}

		spec.Schedules()[vm.General().Hostname()] = chosen
		held[chosen]++

		_ = cluster.IncrHostMemCommit(chosen, memory)
	}

	return nil
}
