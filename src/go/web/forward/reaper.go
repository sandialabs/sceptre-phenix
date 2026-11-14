package forward

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"
)

// forwardExists reports whether the tunnel behind a forward is still open among the VM's tunnels, matching each part the
// forward sets case-insensitively, as minimega's filters do. The source port is the tunnel's own port on the cluster
// host, so another user's tunnel to the same destination does not count; a QEMU forward always counts as open.
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

// removeLocked forgets a forward. forwardsMu must be held.
func removeLocked(l ft.Listener) {
	delete(forwards, l.ToKey())
}

// announceDeleted tells clients the forwards are gone. It broadcasts, so it is
// called after forwardsMu is released.
func announceDeleted(ls []ft.Listener) {
	for _, l := range ls {
		data := map[string]any{"key": l.ToKey()}
		body, _ := json.Marshal(data)

		broker.Broadcast(
			bt.NewRequestPolicy("vms/forwards", "delete", fmt.Sprintf("%s/%s", l.Exp, l.VM)),
			bt.NewResource("experiment/vm/forward", fmt.Sprintf("%s/%s", l.Exp, l.VM), "delete"),
			body,
		)
	}
}

// reapForwards drops every forward whose tunnel has closed.
func reapForwards() {
	reap(func(ft.Listener) bool { return true })
}

// reapVMForwards drops the given VM's forwards whose tunnels have closed.
// Request paths use it instead of reapForwards (left to the background ticker)
// so they ask minimega about one VM at most: one tunnel listing, and none when
// the VM has no tunnel-backed forwards.
func reapVMForwards(exp, vm string) {
	reap(func(l ft.Listener) bool { return l.Exp == exp && l.VM == vm })
}

// reap drops the forwards match selects whose tunnels have closed. It holds
// forwardsMu only to read and edit the map, never while it asks the store or
// minimega, so it must be called without it. A forward changed meanwhile is
// left for the next reap.
func reap(match func(ft.Listener) bool) {
	forwardsMu.Lock()

	var candidates []ft.Listener

	for _, l := range forwards {
		if match(l) {
			candidates = append(candidates, l)
		}
	}

	forwardsMu.Unlock()

	stale := closedForwards(candidates)
	if len(stale) == 0 {
		return
	}

	forwardsMu.Lock()

	var gone []ft.Listener

	for _, l := range stale {
		if forwards[l.ToKey()] == l {
			removeLocked(l)

			gone = append(gone, l)
		}
	}

	forwardsMu.Unlock()

	announceDeleted(gone)
}

// closedForwards returns the forwards of running experiments whose tunnels
// minimega no longer lists. It lists the tunnels of up to
// mmcli.ReadOnlyLimit() VMs at once.
func closedForwards(ls []ft.Listener) []ft.Listener {
	var (
		running = make(map[string]bool)
		live    []ft.Listener
		// Forwards through the same VM share one tunnel listing.
		keys    []tunnelKey
		tunnels = make(map[tunnelKey][]map[string]string)
	)

	for _, l := range ls {
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

		live = append(live, l)

		if key := (tunnelKey{exp: l.Exp, vm: l.VM}); !l.QEMU && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}

	var (
		listed = make([][]map[string]string, len(keys))
		group  errgroup.Group
	)

	group.SetLimit(mmcli.ReadOnlyLimit())

	for i, key := range keys {
		group.Go(func() error {
			listed[i] = mm.GetTunnels(mm.NS(key.exp), mm.VMName(key.vm))

			return nil
		})
	}

	_ = group.Wait()

	for i, key := range keys {
		tunnels[key] = listed[i]
	}

	var closed []ft.Listener

	for _, l := range live {
		var vmTunnels []map[string]string

		if !l.QEMU {
			vmTunnels = tunnels[tunnelKey{exp: l.Exp, vm: l.VM}]
		}

		if !forwardExists(l, vmTunnels) {
			closed = append(closed, l)
		}
	}

	return closed
}

// tunnelKey names the VM whose tunnels one listing gives.
type tunnelKey struct {
	exp, vm string
}
