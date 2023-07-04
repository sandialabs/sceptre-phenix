package forward

import (
	"encoding/json"
	"fmt"
	"phenix/util/mm"

	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"
)

func forwardExists(l ft.Listener) bool {
	tunnels := mm.GetTunnels(
		mm.NS(l.Exp),
		mm.VMName(l.VM),
		mm.TunnelDestinationHost(l.DstHost),
		mm.TunnelDestinationPort(l.DstPort),
	)

	return len(tunnels) > 0
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
	for _, l := range forwards {
		if !forwardExists(l) {
			deleteForward(l)
		}
	}
}
