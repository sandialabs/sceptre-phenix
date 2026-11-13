package capture

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// vlanAliasRegex matches a network as minimega reports it, such as EXP_1 (101), around its alias.
var vlanAliasRegex = regexp.MustCompile(`^(.*) \(\d*\)$`)

// defaultBridge is the bridge of an experiment that names none, as phenix makes it.
const defaultBridge = "phenix"

// pair is one bridge of one host.
type pair struct {
	host, bridge string
}

// Budgets returns the mirror budget of each host and bridge that the running experiment exp uses, sorted by host,
// then bridge. Its error wraps ErrNotRunning for an experiment that does not run.
func Budgets(ctx context.Context, exp string) ([]Budget, error) {
	if err := supported(ctx); err != nil {
		return nil, err
	}

	stored, running, err := runningExperiment(exp)
	if err != nil {
		return nil, err
	}

	if !running {
		return nil, fmt.Errorf("%w: %s", ErrNotRunning, exp)
	}

	return budgets(ctx, stored, nil)
}

// budgets returns the mirror budget of each host and bridge where a VM of exp has an interface on one of vlans (on any
// VLAN when vlans is empty).
func budgets(ctx context.Context, exp *types.Experiment, vlans []string) ([]Budget, error) {
	pairs, err := usedPairs(ctx, exp, vlans)
	if err != nil {
		return nil, err
	}

	if len(pairs) == 0 {
		return []Budget{}, nil
	}

	var hosts []string

	for _, p := range pairs {
		if !slices.Contains(hosts, p.host) {
			hosts = append(hosts, p.host)
		}
	}

	mirrors, err := mm.GetBridgeMirrors(mm.MirrorHosts(hosts), mm.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("listing the Open vSwitch mirrors of %s: %w", strings.Join(hosts, ", "), err)
	}

	return countMirrors(exp.Metadata.Name, pairs, mirrors), nil
}

// countMirrors returns the budget of each pair of the experiment exp, in order, from the mirrors listed.
func countMirrors(exp string, pairs []pair, mirrors []mm.BridgeMirror) []Budget {
	budgets := make([]Budget, 0, len(pairs))

	for _, p := range pairs {
		b := Budget{Host: p.host, Bridge: p.bridge, Total: 0, Experiment: 0, Limit: MirrorLimit, Level: LevelOK}

		for _, m := range mirrors {
			if !strings.EqualFold(m.Host, p.host) || m.Bridge != p.bridge {
				continue
			}

			b.Total++

			if m.Namespace == exp {
				b.Experiment++
			}
		}

		b.Level = level(b.Total, b.Experiment)
		budgets = append(budgets, b)
	}

	return budgets
}

// level is the budget level of a bridge with total mirrors, mine of them the experiment's.
func level(total, mine int) string {
	switch {
	case total >= MirrorLimit:
		return LevelFull
	case total >= nearBudget:
		return LevelNear
	case mine > halfBudget:
		return LevelHalf
	}

	return LevelOK
}

// refused is why a capture that needs a mirror on each of budgets cannot start, or "" when it can.
func refused(budgets []Budget) string {
	for _, b := range budgets {
		if b.Total >= MirrorLimit {
			return fmt.Sprintf("%s / %s has %d of %d Open vSwitch mirrors", b.Host, b.Bridge, b.Total, MirrorLimit)
		}
	}

	return ""
}

// budgetWarnings are the warnings of a capture of exp that started with one more mirror on each of budgets.
func budgetWarnings(exp string, budgets []Budget) []string {
	var warnings []string

	for _, b := range budgets {
		total, mine := b.Total+1, b.Experiment+1
		where := b.Host + " / " + b.Bridge

		if mine > halfBudget {
			warnings = append(warnings, fmt.Sprintf("%s uses %d of %d mirrors on %s", exp, mine, MirrorLimit, where))
		}

		if total >= nearBudget {
			left := max(MirrorLimit-total, 0)

			verb := "are"
			if left == 1 {
				verb = "is"
			}

			warnings = append(warnings, fmt.Sprintf("%s has %d of %d mirrors; %d %s left (%d by %s, %d by others)",
				where, total, MirrorLimit, left, verb, mine, exp, total-mine))
		}
	}

	return warnings
}

// usedPairs returns the hosts and bridges where the VMs of exp, as minimega runs them, have an interface on one of
// vlans (any VLAN when vlans is empty), sorted. An interface is on the bridges of the topology interfaces on its VLAN,
// or on the experiment's default bridge.
func usedPairs(ctx context.Context, exp *types.Experiment, vlans []string) ([]pair, error) {
	name := exp.Metadata.Name

	vms, obs := mm.GetVMInfoObserved(mm.NS(name), mm.Context(ctx))
	if obs.Err != nil {
		return nil, fmt.Errorf("listing the VMs of experiment %s: %w", name, obs.Err)
	}

	nodes := make(map[string]ifaces.NodeSpec)

	if topology := exp.Spec.Topology(); topology != nil {
		for _, node := range topology.Nodes() {
			if node.General() != nil {
				nodes[node.General().Hostname()] = node
			}
		}
	}

	fallback := exp.Spec.DefaultBridge()
	if fallback == "" {
		fallback = defaultBridge
	}

	var pairs []pair

	for _, vm := range vms {
		if vm.Host == "" {
			continue
		}

		for _, network := range vm.Networks {
			alias := network
			if match := vlanAliasRegex.FindStringSubmatch(network); match != nil {
				alias = match[1]
			}

			if alias == "" || alias == "disconnected" || !wanted(vlans, alias) {
				continue
			}

			for _, bridge := range interfaceBridges(nodes[vm.Name], alias, fallback) {
				if p := (pair{host: vm.Host, bridge: bridge}); !slices.Contains(pairs, p) {
					pairs = append(pairs, p)
				}
			}
		}
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		return cmp.Or(strings.Compare(a.host, b.host), strings.Compare(a.bridge, b.bridge))
	})

	return pairs, nil
}

// wanted reports whether the VLAN alias is one of vlans, or vlans is empty.
func wanted(vlans []string, alias string) bool {
	return len(vlans) == 0 || slices.ContainsFunc(vlans, func(v string) bool { return strings.EqualFold(v, alias) })
}

// interfaceBridges are the bridges of node's interfaces on the VLAN alias, fallback for an interface that names none
// or when the node has no interface on it.
func interfaceBridges(node ifaces.NodeSpec, alias, fallback string) []string {
	if node == nil || node.Network() == nil {
		return []string{fallback}
	}

	var bridges []string

	for _, iface := range node.Network().Interfaces() {
		if !strings.EqualFold(iface.VLAN(), alias) {
			continue
		}

		bridge := iface.Bridge()
		if bridge == "" {
			bridge = fallback
		}

		if !slices.Contains(bridges, bridge) {
			bridges = append(bridges, bridge)
		}
	}

	if len(bridges) == 0 {
		return []string{fallback}
	}

	return bridges
}
