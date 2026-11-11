package scheduler

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// mebibytesPerGibibyte converts the MiB that minimega and the topology give
// memory in to GiB. formatMemory writes in GiB only a multiple of
// halfGibibyte, which one decimal shows exactly.
const (
	mebibytesPerGibibyte = 1024
	halfGibibyte         = mebibytesPerGibibyte / 2
)

// missingPin is a VM that the schedules set to a host that is not a
// schedulable cluster host.
type missingPin struct {
	vm, host string
}

// missingHostPins lists, in VM name order, the VMs of spec that are not
// external and that the schedules set to a host that cluster does not have.
func missingHostPins(spec ifaces.ExperimentSpec, cluster mm.Hosts) []missingPin {
	var pins []missingPin

	for _, vm := range slices.Sorted(maps.Keys(spec.Schedules())) {
		node := spec.Topology().FindNodeByName(vm)
		if node == nil || node.External() {
			continue
		}

		if host := spec.Schedules()[vm]; cluster.FindHostByName(host) == nil {
			pins = append(pins, missingPin{vm: vm, host: host})
		}
	}

	return pins
}

// keptPinWarnings warns of each VM that missingHostPins lists, for a
// scheduler that keeps the hosts that the schedules set.
func keptPinWarnings(spec ifaces.ExperimentSpec, cluster mm.Hosts) []string {
	pins := missingHostPins(spec, cluster)
	warnings := make([]string, 0, len(pins))

	for _, pin := range pins {
		warnings = append(warnings, fmt.Sprintf(
			"VM %s stays on host %s, which is not a schedulable cluster host", pin.vm, pin.host,
		))
	}

	return warnings
}

// movedPinWarnings warns of each VM that missingHostPins lists, for a
// scheduler that moved them to host, then of each overcommitted host (see
// overcommitWarnings).
func movedPinWarnings(spec ifaces.ExperimentSpec, cluster mm.Hosts, pins []missingPin, host string) []string {
	warnings := make([]string, 0, len(pins))

	for _, pin := range pins {
		warnings = append(warnings, fmt.Sprintf(
			"VM %s was set to host %s, which is not a schedulable cluster host; it goes to host %s instead",
			pin.vm, pin.host, host,
		))
	}

	return append(warnings, overcommitWarnings(spec, cluster)...)
}

// overcommitWarnings warns of each host of cluster, in name order, where the
// VMs of spec that the schedules put on it, with what the host already has
// committed, need more memory or CPUs than the host has. cluster is the hosts
// as read, before a scheduler counted VMs on them. A capacity of 0 is unknown
// and is not checked. Hosts have no VM limit, so the VM count is not checked.
func overcommitWarnings(spec ifaces.ExperimentSpec, cluster mm.Hosts) []string {
	memory := make(map[string]int)
	cpus := make(map[string]int)

	for _, node := range spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		host, ok := spec.Schedules()[node.General().Hostname()]
		if !ok || cluster.FindHostByName(host) == nil {
			continue
		}

		memory[host] += node.Hardware().Memory()
		cpus[host] += node.Hardware().VCPU()
	}

	var warnings []string

	for _, name := range slices.Sorted(maps.Keys(memory)) {
		host := cluster.FindHostByName(name)

		var over []string

		if committed := host.MemCommit + memory[name]; host.MemTotal > 0 && committed > host.MemTotal {
			over = append(over, fmt.Sprintf(
				"%s of memory committed, %s available", formatMemory(committed), formatMemory(host.MemTotal),
			))
		}

		if committed := host.CPUCommit + cpus[name]; host.CPUs > 0 && committed > host.CPUs {
			over = append(over, fmt.Sprintf("%d CPUs committed, %d available", committed, host.CPUs))
		}

		if len(over) > 0 {
			warnings = append(warnings, fmt.Sprintf("host %s: %s", name, strings.Join(over, "; ")))
		}
	}

	return warnings
}

// formatMemory writes mib MiB of memory exactly: in GiB when it is at least
// 1 GiB and a multiple of half a GiB, else in MiB.
func formatMemory(mib int) string {
	if mib < mebibytesPerGibibyte || mib%halfGibibyte != 0 {
		return strconv.Itoa(mib) + " MiB"
	}

	gib := strconv.FormatFloat(float64(mib)/mebibytesPerGibibyte, 'f', 1, 64)

	return strings.TrimSuffix(gib, ".0") + " GiB"
}
