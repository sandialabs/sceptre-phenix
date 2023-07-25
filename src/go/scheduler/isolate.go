package scheduler

import (
	"fmt"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

func init() {
	schedulers["isolate-experiment"] = new(isolateExperiment)
}

type isolateExperiment struct{}

func (isolateExperiment) Init(...Option) error {
	return nil
}

func (isolateExperiment) Name() string {
	return "isolate-experiment"
}

func (isolateExperiment) Schedule(spec ifaces.ExperimentSpec) error {
	if len(spec.Topology().Nodes()) == 0 {
		return fmt.Errorf("no VMs defined for experiment")
	}

	cluster, err := mm.GetClusterHosts(true)
	if err != nil {
		return fmt.Errorf("getting cluster hosts: %w", err)
	}

	var (
		totalCPU int
		totalMEM int
		first    string
	)

	// get VM totals

	for _, node := range spec.Topology().Nodes() {
		if !node.External() {
			totalCPU += node.Hardware().VCPU()
			totalMEM += node.Hardware().Memory()

			if first == "" {
				first = node.General().Hostname()
			}
		}
	}

	// if first VM is scheduled manually, put all VMs there

	if name, ok := spec.Schedules()[first]; ok {
		if host := cluster.FindHostByName(name); host != nil {
			if host.VMs == 0 {
				cpuUsage := float64(totalCPU+host.CPUCommit) / float64(host.CPUs)
				memUsage := float64(totalMEM+host.MemCommit) / float64(host.MemTotal)

				if cpuUsage > 1 || memUsage > 1 {
					fmt.Printf("Using host %s. It may become overloaded.", host.Name)
				}

				for _, node := range spec.Topology().Nodes() {
					if !node.External() {
						spec.Schedules()[node.General().Hostname()] = host.Name
					}
				}

				return nil
			}

			fmt.Printf("Host %s is currently in use; will not use.\n", host.Name)
		}
	}

	// if that didn't work, use first unoccupied host where everything fits

	// sort hosts by unallocated memory
	cluster.SortByUnallocatedMem(false)

	for _, host := range cluster {
		if host.VMs == 0 {
			cpuUsage := float64(totalCPU+host.CPUCommit) / float64(host.CPUs)
			memUsage := float64(totalMEM+host.MemCommit) / float64(host.MemTotal)

			if cpuUsage < 1 && memUsage < 1 {
				for _, node := range spec.Topology().Nodes() {
					if !node.External() {
						spec.Schedules()[node.General().Hostname()] = host.Name
					}
				}

				return nil
			}
		}
	}

	// if everything doesn't fit, use first unoccupied host

	for _, host := range cluster {
		if host.VMs == 0 {
			for _, node := range spec.Topology().Nodes() {
				if !node.External() {
					spec.Schedules()[node.General().Hostname()] = host.Name
				}
			}

			return nil
		}
	}

	// if that doesn't work either, there are no unoccupied hosts

	return fmt.Errorf("no unused hosts -- cannot isolate experiment")
}
