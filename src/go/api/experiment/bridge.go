package experiment

import (
	"fmt"
	"slices"

	"phenix/app"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/plog"
)

// deleteOwnBridge deletes the bridge of a stopped experiment on each of its hosts (see bridgeHosts), when the
// experiment has a bridge of its own (see ownBridge) and no other running experiment uses that bridge.
func deleteOwnBridge(exp *types.Experiment) error {
	bridge := ownBridge(exp)
	if bridge == "" {
		return nil
	}

	others, failed, err := types.DecodableExperiments(true)
	if err != nil {
		return fmt.Errorf("deleting experiment bridge %s: %w", bridge, err)
	}

	if len(failed) > 0 {
		undecoded := make([]string, 0, len(failed))
		for _, f := range failed {
			undecoded = append(undecoded, f.Name)
		}

		plog.Warn(
			plog.TypeSystem, "keeping experiment bridge, as phenix cannot decode all experiment configs",
			"exp", exp.Metadata.Name, "bridge", bridge, "undecoded", undecoded,
		)

		return nil
	}

	if user := bridgeUser(bridge, exp.Metadata.Name, others); user != "" {
		plog.Warn(
			plog.TypeSystem, "keeping experiment bridge, as another running experiment uses it",
			"exp", exp.Metadata.Name, "bridge", bridge, "user", user,
		)

		return nil
	}

	err = mm.DestroyBridge(bridge, bridgeHosts(exp, mm.Headnode())...)
	if err != nil {
		return fmt.Errorf("deleting experiment bridge %s: %w", bridge, err)
	}

	return nil
}

// ownBridge is the bridge that only experiment exp uses: its default bridge, when the bridge mode is auto and the
// bridge has the experiment's name. It is "" in all other cases, and always for the shared phenix bridge.
func ownBridge(exp *types.Experiment) string {
	if common.BridgeMode != common.BridgeModeAuto {
		return ""
	}

	bridge := exp.Spec.DefaultBridge()
	if bridge == defaultBridgeName || bridge != exp.Metadata.Name {
		return ""
	}

	return bridge
}

// bridgeUser is the name of the first experiment in others, other than self, that uses bridge as its default bridge
// or on an interface, "" for none.
func bridgeUser(bridge, self string, others []*types.Experiment) string {
	for _, other := range others {
		if other.Metadata.Name == self {
			continue
		}

		if other.Spec.DefaultBridge() == bridge {
			return other.Metadata.Name
		}

		for _, node := range other.Spec.Topology().Nodes() {
			for _, iface := range node.Network().Interfaces() {
				if iface.Bridge() == bridge {
					return other.Metadata.Name
				}
			}
		}
	}

	return ""
}

// bridgeHosts are the sorted hosts that can have the experiment's bridge: the hosts of its VMs, the head node, which
// has the SCORCH taps, and the host of the tap app's taps.
func bridgeHosts(exp *types.Experiment, headnode string) []string {
	hosts := []string{headnode}

	for _, host := range exp.Status.Schedules() {
		hosts = append(hosts, host)
	}

	var taps app.TapAppStatus
	if err := exp.Status.ParseAppStatus((app.Tap{}).Name(), &taps); err == nil {
		hosts = append(hosts, taps.Host)
	}

	hosts = slices.DeleteFunc(hosts, func(host string) bool { return host == "" })
	slices.Sort(hosts)

	return slices.Compact(hosts)
}
