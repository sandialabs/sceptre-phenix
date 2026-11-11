package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"

	ifaces "phenix/types/interfaces"
)

func init() { //nolint:gochecknoinits // scheduler registration
	schedulers["round-robin"] = new(roundRobin)
}

type roundRobin struct{}

func (roundRobin) Init(...Option) error {
	return nil
}

func (roundRobin) Name() string {
	return "round-robin"
}

func (roundRobin) Schedule(ctx context.Context, spec ifaces.ExperimentSpec) ([]string, error) {
	if len(spec.Topology().Nodes()) == 0 {
		return nil, errors.New("no VMs defined for experiment")
	}

	cluster, err := schedulableHosts(ctx, false)
	if err != nil {
		return nil, err
	}

	read := slices.Clone(cluster)
	warnings := keptPinWarnings(spec, read)

	// Update cluster host VM count to account for VMs manually scheduled before
	// sorting hosts by VM count below; a pin of a VM the topology lacks counts for nothing.
	for vm, name := range spec.Schedules() {
		if node := spec.Topology().FindNodeByName(vm); node != nil && !node.External() {
			_ = cluster.IncrHostVMs(name, 1)
		}
	}

	cluster.SortByVMs(true)

	// TODO: sort VMs by scheduled,memory (??)

	for _, node := range spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		if _, ok := spec.Schedules()[node.General().Hostname()]; !ok {
			if len(cluster) == 0 {
				return nil, fmt.Errorf("scheduling VM %s: %w", node.General().Hostname(), ErrNoSchedulableHosts)
			}

			spec.Schedules()[node.General().Hostname()] = cluster[0].Name

			cluster[0].VMs += 1
			cluster.SortByVMs(true)
		}
	}

	return append(warnings, overcommitWarnings(spec, read)...), nil
}
