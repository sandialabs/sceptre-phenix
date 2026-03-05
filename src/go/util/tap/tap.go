package tap

import (
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
	}

	if t.o.netns {
		opts = append(opts, mm.TapNetNS(t.Name))
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
		mm.TapName(t.Name), mm.TapDelete(),
	}

	if t.o.netns {
		opts = append(opts, mm.TapNetNS(t.Name))
	}

	err := mm.TapVLAN(opts...)
	if err != nil {
		return fmt.Errorf("deleting tap on host %s: %w", host, err)
	}

	return nil
}

func (t *Tap) connect(host string, subnet netaddr.IPPrefix) error { //nolint:funlen // complex logic
	if !t.o.netns {
		return fmt.Errorf("cannot connect tap %s - not in network namespace", t.Name)
	}

	t.Subnet = subnet.Masked().String()

	var (
		left  = subnet.IP().Next()
		right = subnet.IP().Next().Next()
	)

	plog.Info(plog.TypeSystem, "creating veth pair for tap on host", "tap", t.Name, "host", host)

	cmd := fmt.Sprintf("ip link add %s type veth peer name %s_right", t.Name, t.Name[:9])

	err := mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf("creating veth pair for tap %s on host %s: %w", t.Name, host, err)
	}

	plog.Info(
		plog.TypeSystem,
		"attaching veth interface to network namespace on host",
		"ns",
		t.Name,
		"host",
		host,
	)

	cmd = fmt.Sprintf("ip link set dev %s_right netns %s", t.Name[:9], t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"attaching veth interface to network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	plog.Info(
		plog.TypeSystem,
		"enabling and configuring veth interface in network namespace on host",
		"ns",
		t.Name,
		"host",
		host,
	)

	cmd = fmt.Sprintf("ip netns exec %s ip link set %s_right name eth0", t.Name, t.Name[:9])

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"renaming veth interface in network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip link set dev eth0 up", t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"enabling veth interface in network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip addr add %s/30 dev eth0", t.Name, right)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"setting IP for veth interface in network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip route add default via %s", t.Name, left)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"setting default route for network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	plog.Info(
		plog.TypeSystem,
		"configuring iptables in network namespace on host",
		"ns",
		t.Name,
		"host",
		host,
	)

	cmd = fmt.Sprintf(
		"ip netns exec %s iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE",
		t.Name,
	)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"configuring iptables masquerading in network namespace %s on host %s: %w",
			t.Name,
			host,
			err,
		)
	}

	plog.Info(plog.TypeSystem, "configuring iptables in the system namespace on host", "host", host)

	cmd = fmt.Sprintf("iptables -t nat -A POSTROUTING -s %s ! -o %s -j MASQUERADE", right, t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"configuring iptables masquerading in system namespace on host %s: %w",
			host,
			err,
		)
	}

	cmd = fmt.Sprintf("iptables -A FORWARD -i %s ! -o %s -j ACCEPT", t.Name, t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"configuring iptables forwarding in system namespace on host %s: %w",
			host,
			err,
		)
	}

	cmd = fmt.Sprintf(
		"iptables -A FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
		t.Name,
	)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"configuring iptables forwarding in system namespace on host %s: %w",
			host,
			err,
		)
	}

	plog.Info(
		plog.TypeSystem,
		"enabling and configuring veth interface in the system namespace on host",
		"host",
		host,
	)

	cmd = fmt.Sprintf("ip addr add %s/30 dev %s", left, t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf(
			"setting IP for veth interface in system namespace on host %s: %w",
			host,
			err,
		)
	}

	cmd = fmt.Sprintf("ip link set dev %s up", t.Name)

	err = mm.MeshShell(host, cmd)
	if err != nil {
		return fmt.Errorf("enabling veth interface in system namespace on host %s: %w", host, err)
	}

	return nil
}

func (t *Tap) disconnect(host string) error {
	if !t.o.netns {
		return fmt.Errorf("cannot disconnect tap %s - not in network namespace", t.Name)
	}

	subnet, err := netaddr.ParseIPPrefix(t.Subnet)
	if err != nil {
		return fmt.Errorf("parsing tap subnet %s: %w", t.Subnet, err)
	}

	var (
		right = subnet.IP().Next().Next()
		errs  error
	)

	plog.Info(
		plog.TypeSystem,
		"deleting iptables configs in the system namespace on host",
		"host",
		host,
	)

	cmd := fmt.Sprintf("iptables -t nat -D POSTROUTING -s %s ! -o %s -j MASQUERADE", right, t.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(
			errs,
			fmt.Errorf(
				"deleting iptables masquerading in system namespace on host %s: %w",
				host,
				err,
			),
		)
	}

	cmd = fmt.Sprintf("iptables -D FORWARD -i %s ! -o %s -j ACCEPT", t.Name, t.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(
			errs,
			fmt.Errorf(
				"deleting iptables forwarding in system namespace on host %s: %w",
				host,
				err,
			),
		)
	}

	cmd = fmt.Sprintf(
		"iptables -D FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
		t.Name,
	)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(
			errs,
			fmt.Errorf(
				"deleting iptables forwarding in system namespace on host %s: %w",
				host,
				err,
			),
		)
	}

	return errs
}
