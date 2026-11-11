package scheduler

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/mm"
)

// propertyMM gives the hosts it points to; other mm.MM methods panic.
type propertyMM struct {
	mm.MM

	hosts *mm.Hosts
}

func (m propertyMM) GetClusterHosts(bool) (mm.Hosts, error) {
	return slices.Clone(*m.hosts), nil
}

// propertyHosts draws one to four cluster hosts, some without a capacity,
// some with VMs or commits.
func propertyHosts() *rapid.Generator[mm.Hosts] {
	return rapid.Custom(func(t *rapid.T) mm.Hosts {
		hosts := make(mm.Hosts, rapid.IntRange(1, 4).Draw(t, "hosts"))

		for i := range hosts {
			hosts[i] = mm.Host{
				Name:      fmt.Sprintf("h%d", i),
				CPUs:      rapid.SampledFrom([]int{0, 1, 4}).Draw(t, "cpus"),
				MemTotal:  rapid.SampledFrom([]int{0, 512, 1024, 4096}).Draw(t, "memtotal"),
				MemCommit: rapid.SampledFrom([]int{0, 256}).Draw(t, "memcommit"),
				VMs:       rapid.IntRange(0, 2).Draw(t, "vms"),
			}
		}

		return hosts
	})
}

// propertyVMs draws one to eight topology nodes, with or without
// interfaces, some external, some that do not boot at start.
func propertyVMs() *rapid.Generator[[]builtinVM] {
	return rapid.Custom(func(t *rapid.T) []builtinVM {
		vms := make([]builtinVM, rapid.IntRange(1, 8).Draw(t, "nodes"))

		for i := range vms {
			vms[i] = builtinVM{
				name:      fmt.Sprintf("vm%d", i),
				vlan:      rapid.SampledFrom([]string{"", "A", "B"}).Draw(t, "vlan"),
				memory:    rapid.SampledFrom([]int{256, 512, 1024}).Draw(t, "memory"),
				external:  rapid.IntRange(0, 5).Draw(t, "external") == 0,
				doNotBoot: rapid.Bool().Draw(t, "do not boot"),
			}
		}

		return vms
	})
}

// propertyPins draws the pins of vms: none, a host of hosts, or "gone", a
// host that is not in the cluster.
func propertyPins(hosts mm.Hosts, vms []builtinVM) *rapid.Generator[map[string]string] {
	targets := make([]string, 0, len(hosts)+2)
	targets = append(targets, "", "gone")

	for _, host := range hosts {
		targets = append(targets, host.Name)
	}

	return rapid.Custom(func(t *rapid.T) map[string]string {
		pins := make(map[string]string)

		for _, vm := range vms {
			if host := rapid.SampledFrom(targets).Draw(t, "pin"); host != "" {
				pins[vm.name] = host
			}
		}

		return pins
	})
}

// Each built-in scheduler gives every VM that is not external one host, the
// same schedule and warnings for the same input, and a cluster host to each
// VM it places. round-robin and subnet-compute keep every pin and warn of
// each pin to a host that is not in the cluster; isolate-experiment puts all
// VMs on one host that had no VMs, or fails when there is none.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestBuiltinSchedulerProperties(t *testing.T) {
	var hosts mm.Hosts

	useClusterMM(t, propertyMM{hosts: &hosts})

	rapid.Check(t, func(rt *rapid.T) {
		hosts = propertyHosts().Draw(rt, "cluster")
		vms := propertyVMs().Draw(rt, "topology")
		pins := propertyPins(hosts, vms).Draw(rt, "pins")
		name := rapid.SampledFrom([]string{"round-robin", "subnet-compute", "isolate-experiment"}).Draw(rt, "scheduler")

		schedules := make(map[string]any, len(pins))
		for vm, host := range pins {
			schedules[vm] = host
		}

		spec := builtinSpec(rt, vms, schedules)
		again := builtinSpec(rt, vms, schedules)

		warnings, err := Schedule(t.Context(), name, spec)
		warningsAgain, errAgain := Schedule(t.Context(), name, again)

		if (err == nil) != (errAgain == nil) {
			rt.Fatalf("Schedule(%s) errors %v and %v for the same input", name, err, errAgain)
		}

		if diff := cmp.Diff(spec.Schedules(), again.Schedules()); diff != "" {
			rt.Fatalf("Schedule(%s) schedules differ for the same input (-first +second):\n%s", name, diff)
		}

		if diff := cmp.Diff(warnings, warningsAgain, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("Schedule(%s) warnings differ for the same input (-first +second):\n%s", name, diff)
		}

		if err != nil {
			if name != "isolate-experiment" || slices.ContainsFunc(hosts, func(h mm.Host) bool { return h.VMs == 0 }) {
				rt.Fatalf("Schedule(%s) error = %v", name, err)
			}

			return
		}

		placed := make(map[string]bool)

		for _, vm := range vms {
			host, ok := spec.Schedules()[vm.name]

			switch {
			case vm.external:
				if host != pins[vm.name] {
					rt.Fatalf("Schedule(%s) changed the host of external node %s to %q", name, vm.name, host)
				}
			case !ok:
				rt.Fatalf("Schedule(%s) gave VM %s no host", name, vm.name)
			case name == "isolate-experiment":
				placed[host] = true
			case pins[vm.name] != "" && host != pins[vm.name]:
				rt.Fatalf("Schedule(%s) moved pinned VM %s from %s to %s", name, vm.name, pins[vm.name], host)
			case pins[vm.name] == "" && hosts.FindHostByName(host) == nil:
				rt.Fatalf("Schedule(%s) put VM %s on %q, which is not a cluster host", name, vm.name, host)
			}
		}

		if name == "isolate-experiment" {
			for host := range placed {
				if h := hosts.FindHostByName(host); len(placed) != 1 || h == nil || h.VMs != 0 {
					rt.Fatalf("Schedule(isolate-experiment) put the VMs on hosts %v, want one host without VMs", placed)
				}
			}

			return
		}

		var stays []string

		for _, vm := range vms {
			if !vm.external && pins[vm.name] == "gone" {
				stays = append(stays, "VM "+vm.name+" stays on host gone, which is not a schedulable cluster host")
			}
		}

		slices.Sort(stays)

		got := slices.DeleteFunc(slices.Clone(warnings), func(w string) bool { return strings.HasPrefix(w, "host ") })
		if diff := cmp.Diff(stays, got, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("Schedule(%s) pin warnings mismatch (-want +got):\n%s", name, diff)
		}
	})
}
