package tap

import (
	"fmt"

	"phenix/util"
	"phenix/util/mm"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/hashicorp/go-multierror"
	"inet.af/netaddr"
)

func (this *Tap) Init(opts ...Option) {
	this.o = NewOptions(opts...)

	if this.Bridge == "" {
		this.Bridge = "phenix"
	}
}

func (this *Tap) Create(host string) (netaddr.IPPrefix, error) {
	if err := this.create(host); err != nil {
		// attempt to clean up any progress already made
		this.delete(host)
		return netaddr.IPPrefix{}, fmt.Errorf("creating host tap for VLAN %s: %w", this.VLAN, err)
	}

	if this.External.Enabled {
		used := append(this.o.used, netaddr.MustParseIPPrefix(this.IP))

		pair, err := util.UnusedSubnet(this.o.subnet, used)
		if err != nil {
			return netaddr.IPPrefix{}, fmt.Errorf("getting unused pair of IPs for VLAN %s host tap external connectivity: %w", this.VLAN, err)
		}

		if err := this.connect(host, pair); err != nil {
			// attempt to clean up progress we already made
			this.disconnect(host)
			this.delete(host)

			return netaddr.IPPrefix{}, fmt.Errorf("connecting host tap for VLAN %s for external access: %w", this.VLAN, err)
		}

		return pair, nil
	}

	return netaddr.IPPrefix{}, nil
}

func (this Tap) Delete(host string) error {
	var errs error

	if this.External.Enabled {
		if err := this.disconnect(host); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("disconnecting host tap for VLAN %s: %w", this.VLAN, err))
		}
	}

	if err := this.delete(host); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting host tap for VLAN %s: %w", this.VLAN, err))
	}

	return errs
}

func (this Tap) create(host string) error {
	opts := []mm.TapOption{
		mm.TapHost(host), mm.TapNS(this.o.exp), mm.TapName(this.Name),
		mm.TapBridge(this.Bridge), mm.TapVLANAlias(this.VLAN), mm.TapIP(this.IP),
	}

	if this.o.netns {
		opts = append(opts, mm.TapNetNS(this.Name))
	}

	if err := mm.TapVLAN(opts...); err != nil {
		return fmt.Errorf("creating tap on host %s: %w", host, err)
	}

	return nil
}

func (this Tap) delete(host string) error {
	opts := []mm.TapOption{
		mm.TapHost(host), mm.TapNS(this.o.exp),
		mm.TapName(this.Name), mm.TapDelete(),
	}

	if this.o.netns {
		opts = append(opts, mm.TapNetNS(this.Name))
	}

	if err := mm.TapVLAN(opts...); err != nil {
		return fmt.Errorf("deleting tap on host %s: %w", host, err)
	}

	return nil
}

func (this *Tap) connect(host string, subnet netaddr.IPPrefix) error {
	if !this.o.netns {
		return fmt.Errorf("cannot connect tap %s - not in network namespace", this.Name)
	}

	this.Subnet = subnet.Masked().String()

	var (
		left  = subnet.IP().Next()
		right = subnet.IP().Next().Next()
	)

	log.Info("creating veth pair for tap %s on host %s", this.Name, host)

	cmd := fmt.Sprintf("ip link add %s type veth peer name %s_right", this.Name, this.Name[:9])
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("creating veth pair for tap %s on host %s: %w", this.Name, host, err)
	}

	log.Info("attaching veth interface to network namespace %s on host %s", this.Name, host)

	cmd = fmt.Sprintf("ip link set dev %s_right netns %s", this.Name[:9], this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("attaching veth interface to network namespace %s on host %s: %w", this.Name, host, err)
	}

	log.Info("enabling and configuring veth interface in network namespace %s on host %s", this.Name, host)

	cmd = fmt.Sprintf("ip netns exec %s ip link set %s_right name eth0", this.Name, this.Name[:9])
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("renaming veth interface in network namespace %s on host %s: %w", this.Name, host, err)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip link set dev eth0 up", this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("enabling veth interface in network namespace %s on host %s: %w", this.Name, host, err)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip addr add %s/30 dev eth0", this.Name, right)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("setting IP for veth interface in network namespace %s on host %s: %w", this.Name, host, err)
	}

	cmd = fmt.Sprintf("ip netns exec %s ip route add default via %s", this.Name, left)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("setting default route for network namespace %s on host %s: %w", this.Name, host, err)
	}

	log.Info("configuring iptables in network namespace %s on host %s", this.Name, host)

	cmd = fmt.Sprintf("ip netns exec %s iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE", this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables masquerading in network namespace %s on host %s: %w", this.Name, host, err)
	}

	log.Info("configuring iptables in the system namespace on host %s", host)

	cmd = fmt.Sprintf("iptables -t nat -A POSTROUTING -s %s ! -o %s -j MASQUERADE", right, this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables masquerading in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("iptables -A FORWARD -i %s ! -o %s -j ACCEPT", this.Name, this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables forwarding in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("iptables -A FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables forwarding in system namespace on host %s: %w", host, err)
	}

	log.Info("enabling and configuring veth interface in the system namespace on host %s", host)

	cmd = fmt.Sprintf("ip addr add %s/30 dev %s", left, this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("setting IP for veth interface in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("ip link set dev %s up", this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		return fmt.Errorf("enabling veth interface in system namespace on host %s: %w", host, err)
	}

	return nil
}

func (this Tap) disconnect(host string) error {
	if !this.o.netns {
		return fmt.Errorf("cannot disconnect tap %s - not in network namespace", this.Name)
	}

	subnet, err := netaddr.ParseIPPrefix(this.Subnet)
	if err != nil {
		return fmt.Errorf("parsing tap subnet %s: %w", this.Subnet, err)
	}

	var (
		right = subnet.IP().Next().Next()
		errs  error
	)

	log.Info("deleting iptables configs in the system namespace on host %s", host)

	cmd := fmt.Sprintf("iptables -t nat -D POSTROUTING -s %s ! -o %s -j MASQUERADE", right, this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables masquerading in system namespace on host %s: %w", host, err))
	}

	cmd = fmt.Sprintf("iptables -D FORWARD -i %s ! -o %s -j ACCEPT", this.Name, this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables forwarding in system namespace on host %s: %w", host, err))
	}

	cmd = fmt.Sprintf("iptables -D FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", this.Name)
	if err := mm.MeshShell(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables forwarding in system namespace on host %s: %w", host, err))
	}

	return errs
}
