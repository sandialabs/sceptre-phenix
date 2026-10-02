package forward

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"
)

// forwardExists reports whether the tunnel behind a forward is still open,
// given the VM's tunnels as mm.GetTunnels lists them without filters. A tunnel
// matches as minimega's filters would match it: on each part the forward sets,
// case-insensitively. The source port is the tunnel's own port on the cluster
// host, so another user's tunnel to the same destination does not count. A
// QEMU forward is not a tunnel and always counts as open.
func forwardExists(l ft.Listener, tunnels []map[string]string) bool {
	if l.QEMU {
		return true
	}

	for _, row := range tunnels {
		if l.DstHost != "" && !strings.EqualFold(row["dst"], l.DstHost) {
			continue
		}

		if l.DstPort != 0 && row["dst port"] != strconv.Itoa(l.DstPort) {
			continue
		}

		if l.ClusterPort != 0 && row["src port"] != strconv.Itoa(l.ClusterPort) {
			continue
		}

		return true
	}

	return false
}

func deleteForward(l ft.Listener) {
	data := map[string]any{"key": l.ToKey()}
	body, _ := json.Marshal(data)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/forwards", "delete", fmt.Sprintf("%s/%s", l.Exp, l.VM)),
		bt.NewResource("experiment/vm/forward", fmt.Sprintf("%s/%s", l.Exp, l.VM), "delete"),
		body,
	)

	delete(forwards, l.ToKey())
}

func reapForwards() {
	var (
		// Forwards through the same VM share one tunnel listing.
		tunnels = make(map[string][]map[string]string)
		running = make(map[string]bool)
	)

	for _, l := range forwards {
		// Listing a stopped experiment's tunnels would recreate its minimega
		// namespace. Its forwards are dropped when it stops (see the stop hook),
		// and one starting may not be marked running yet.
		isRunning, ok := running[l.Exp]
		if !ok {
			isRunning = experiment.Running(l.Exp)
			running[l.Exp] = isRunning
		}

		if !isRunning {
			continue
		}

		var vmTunnels []map[string]string

		if !l.QEMU {
			key := l.Exp + "\x00" + l.VM

			listed, ok := tunnels[key]
			if !ok {
				listed = mm.GetTunnels(mm.NS(l.Exp), mm.VMName(l.VM))
				tunnels[key] = listed
			}

			vmTunnels = listed
		}

		if !forwardExists(l, vmTunnels) {
			deleteForward(l)
		}
	}
}
