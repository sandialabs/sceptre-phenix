package tap

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-multierror"
	"inet.af/netaddr"

	"phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
)

func (t *Tap) Init(bridge string, opts ...Option) {
	t.o = NewOptions(opts...)

	if t.Bridge == "" {
		t.Bridge = bridge
	}
}

func (t *Tap) Create(host string) (netaddr.IPPrefix, error) {
	err := t.create(host)
	if err != nil {
		// attempt to clean up any progress already made
		_ = t.delete(host)

		return netaddr.IPPrefix{}, fmt.Errorf("creating host tap for VLAN %s: %w", t.VLAN, err)
	}

	if t.External.Enabled {
		used := t.o.used
		used = append(used, netaddr.MustParseIPPrefix(t.IP))

		pair, err := util.UnusedSubnet(t.o.subnet, used)
		if err != nil {
			return netaddr.IPPrefix{}, fmt.Errorf(
				"getting unused pair of IPs for VLAN %s host tap external connectivity: %w",
				t.VLAN,
				err,
			)
		}

		if err := t.connect(host, pair); err != nil {
			// attempt to clean up progress we already made
			_ = t.disconnect(host)
			_ = t.delete(host)

			return netaddr.IPPrefix{}, fmt.Errorf(
				"connecting host tap for VLAN %s for external access: %w",
				t.VLAN,
				err,
			)
		}

		return pair, nil
	}

	return netaddr.IPPrefix{}, nil
}

func (t *Tap) Delete(host string) error {
	var errs error

	if t.External.Enabled {
		err := t.disconnect(host)
		if err != nil {
			errs = multierror.Append(
				errs,
				fmt.Errorf("disconnecting host tap for VLAN %s: %w", t.VLAN, err),
			)
		}
	}

	err := t.delete(host)
	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting host tap for VLAN %s: %w", t.VLAN, err))
	}

	return errs
}

func (t *Tap) create(host string) error {
	opts := []mm.TapOption{
		mm.TapHost(host), mm.TapNS(t.o.exp), mm.TapName(t.Name),
		mm.TapBridge(t.Bridge), mm.TapVLANAlias(t.VLAN), mm.TapIP(t.IP),
		mm.TapNetNS(t.Name),
	}

	err := mm.TapVLAN(opts...)
	if err != nil {
		return fmt.Errorf("creating tap on host %s: %w", host, err)
	}

	return nil
}

func (t *Tap) delete(host string) error {
	opts := []mm.TapOption{
		mm.TapHost(host), mm.TapNS(t.o.exp),
		mm.TapName(t.Name), mm.TapDelete(), mm.TapNetNS(t.Name),
	}

	err := mm.TapVLAN(opts...)
	if err != nil {
		return fmt.Errorf("deleting tap on host %s: %w", host, err)
	}

	return nil
}

func (t *Tap) connect(host string, subnet netaddr.IPPrefix) error {
	if err := mm.ValidateShellWord(t.Name); err != nil {
		return fmt.Errorf("tap name: %w", err)
	}

	t.Subnet = subnet.Masked().String()

	var (
		name  = t.Name
		peer  = t.Name[:9] + "_right"
		left  = subnet.IP().Next()
		right = subnet.IP().Next().Next()
	)

	plog.Info(plog.TypeSystem, "connecting tap for external access on host", "tap", name, "host", host)

	ns := func(action string) string {
		return fmt.Sprintf("%s in network namespace %s on host %s", action, name, host)
	}
	sys := func(action string) string { return fmt.Sprintf("%s in system namespace on host %s", action, host) }

	steps := []mm.ShellStep{
		{
			Action:  fmt.Sprintf("creating veth pair for tap %s on host %s", name, host),
			Command: fmt.Sprintf("ip link add %s type veth peer name %s", name, peer),
		},
		{
			Action:  fmt.Sprintf("attaching veth interface to network namespace %s on host %s", name, host),
			Command: fmt.Sprintf("ip link set dev %s netns %s", peer, name),
		},
		{Action: ns("renaming veth interface"), Command: fmt.Sprintf("ip netns exec %s ip link set %s name eth0", name, peer)},
		{Action: ns("enabling veth interface"), Command: fmt.Sprintf("ip netns exec %s ip link set dev eth0 up", name)},
		{
			Action:  ns("setting IP for veth interface"),
			Command: fmt.Sprintf("ip netns exec %s ip addr add %s/30 dev eth0", name, right),
		},
		{
			Action:  ns("setting default route"),
			Command: fmt.Sprintf("ip netns exec %s ip route add default via %s", name, left),
		},
		{
			Action:  ns("configuring iptables masquerading"),
			Command: fmt.Sprintf("ip netns exec %s iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE", name),
		},
		{
			Action:  sys("configuring iptables masquerading"),
			Command: fmt.Sprintf("iptables -t nat -A POSTROUTING -s %s ! -o %s -j MASQUERADE", right, name),
		},
		{
			Action:  sys("configuring iptables forwarding"),
			Command: fmt.Sprintf("iptables -A FORWARD -i %s ! -o %s -j ACCEPT", name, name),
		},
		{
			Action:  sys("configuring iptables forwarding"),
			Command: fmt.Sprintf("iptables -A FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", name),
		},
		{Action: sys("setting IP for veth interface"), Command: fmt.Sprintf("ip addr add %s/30 dev %s", left, name)},
		{Action: sys("enabling veth interface"), Command: fmt.Sprintf("ip link set dev %s up", name)},
	}

	return mm.MeshShellSteps(host, steps...)
}

func (t *Tap) disconnect(host string) error {
	if err := mm.ValidateShellWord(t.Name); err != nil {
		return fmt.Errorf("tap name: %w", err)
	}

	subnet, err := netaddr.ParseIPPrefix(t.Subnet)
	if err != nil {
		return fmt.Errorf("parsing tap subnet %s: %w", t.Subnet, err)
	}

	var (
		name  = t.Name
		right = subnet.IP().Next().Next()
	)

	plog.Info(plog.TypeSystem, "deleting iptables configs in the system namespace on host", "host", host)

	steps := []mm.ShellStep{
		{
			Action:  "deleting iptables masquerading in system namespace",
			Command: fmt.Sprintf("iptables -t nat -D POSTROUTING -s %s ! -o %s -j MASQUERADE", right, name),
		},
		{
			Action:  "deleting iptables forwarding in system namespace",
			Command: fmt.Sprintf("iptables -D FORWARD -i %s ! -o %s -j ACCEPT", name, name),
		},
		{
			Action:  "deleting iptables forwarding in system namespace",
			Command: fmt.Sprintf("iptables -D FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", name),
		},
	}

	return mm.MeshShellEach(context.Background(), host, steps...)
}
