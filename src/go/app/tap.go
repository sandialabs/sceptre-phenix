package app

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"phenix/internal/mm"
	"phenix/internal/mm/mmcli"
	"phenix/types"
	"phenix/util"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/hashicorp/go-multierror"
	"inet.af/netaddr"
)

func init() {
	RegisterUserApp(NewTap())
}

type TapAppMetadata struct {
	Taps []TapAppTap `mapstructure:"taps"`
}

type TapAppTap struct {
	Bridge   string            `mapstructure:"bridge"`
	VLAN     string            `mapstructure:"vlan"`
	IP       string            `mapstructure:"ip"`
	External TapAppTapExternal `mapstructure:"externalAccess"`
}

func (this *TapAppTap) Init() {
	if this.Bridge == "" {
		this.Bridge = "phenix"
	}
}

type TapAppTapExternal struct {
	Enabled bool `mapstructure:"enabled"`
	// TODO: add firewall config
}

type TapAppStatus struct {
	Host string                     `json:"host" mapstructure:"host"`
	Taps map[string]TapAppStatusTap `json:"taps" mapstructure:"taps"`
}

type TapAppStatusTap struct {
	Name   string `json:"name" mapstructure:"name"`
	Subnet string `json:"subnet" mapstructure:"subnet"`
}

type Tap struct {
	pairs map[netaddr.IPPrefix]struct{}
}

func NewTap() *Tap {
	return &Tap{
		pairs: make(map[netaddr.IPPrefix]struct{}),
	}
}

func (Tap) Init(...Option) error {
	return nil
}

func (Tap) Name() string {
	return "tap"
}

func (Tap) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Tap) PreStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this *Tap) PostStart(ctx context.Context, exp *types.Experiment) error {
	app := exp.App(this.Name())
	if app == nil {
		// this should never happen...
		return fmt.Errorf("%s app not defined in experiment scenario", this.Name())
	}

	var amd TapAppMetadata
	if err := app.ParseMetadata(&amd); err != nil {
		return fmt.Errorf("decoding %s app metadata: %w", this.Name(), err)
	}

	vlans, err := mm.GetVLANs(mm.NS(exp.Metadata.Name))
	if err != nil {
		return fmt.Errorf("getting experiment VLANs: %w", err)
	}

	hosts, err := mm.GetClusterHosts(true)
	if err != nil {
		return fmt.Errorf("getting list of cluster hosts: %w", err)
	}

	rand.Seed(time.Now().UnixNano())
	host := hosts[rand.Intn(len(hosts))].Name

	status := TapAppStatus{Host: host, Taps: make(map[string]TapAppStatusTap)}

	for _, tmd := range amd.Taps {
		tmd.Init()

		if _, ok := status.Taps[tmd.VLAN]; ok {
			return fmt.Errorf("tap already created for VLAN %s", tmd.VLAN)
		}

		vlan, ok := vlans[tmd.VLAN]
		if !ok {
			return fmt.Errorf("VLAN alias %s not used in experiment", tmd.VLAN)
		}

		// Tap name is random, yet descriptive to the fact that it's a "tapapp" tap.
		name := fmt.Sprintf("%s-tapapp", util.RandomString(8))

		if err := createHostTap(host, tmd.Bridge, name, vlan, tmd.IP); err != nil {
			// clean up any progress already made
			deleteHostTap(host, tmd.Bridge, name)

			return fmt.Errorf("creating host tap for VLAN %s: %w", tmd.VLAN, err)
		}

		tapStatus := TapAppStatusTap{Name: name}

		if tmd.External.Enabled {
			pair, err := this.unusedPair(tmd.IP)
			if err != nil {
				return fmt.Errorf("getting unused pair of IPs for VLAN %s host tap external connectivity: %w", tmd.VLAN, err)
			}

			tapStatus.Subnet = fmt.Sprintf("%s/30", pair)

			if err := connectHostTap(host, name, pair); err != nil {
				// clean up tap we already created
				disconnectHostTap(host, name, pair)
				deleteHostTap(host, tmd.Bridge, name)

				return fmt.Errorf("connecting host tap for VLAN %s for external access: %w", tmd.VLAN, err)
			}
		}

		status.Taps[tmd.VLAN] = tapStatus
	}

	exp.Status.SetAppStatus(this.Name(), status)

	return nil
}

func (Tap) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this *Tap) Cleanup(ctx context.Context, exp *types.Experiment) error {
	app := exp.App(this.Name())
	if app == nil {
		// this should never happen...
		return fmt.Errorf("%s app not defined in experiment scenario", this.Name())
	}

	var amd TapAppMetadata
	if err := app.ParseMetadata(&amd); err != nil {
		return fmt.Errorf("decoding %s app metadata: %w", this.Name(), err)
	}

	var status TapAppStatus
	if err := exp.Status.ParseAppStatus(this.Name(), &status); err != nil {
		return fmt.Errorf("getting experiment status for %s app: %w", this.Name(), err)
	}

	var (
		host = status.Host
		errs error
	)

	for _, tmd := range amd.Taps {
		tmd.Init()

		tap, ok := status.Taps[tmd.VLAN]
		if !ok {
			errs = multierror.Append(errs, fmt.Errorf("missing tap name for VLAN %s", tmd.VLAN))
			continue
		}

		if tmd.External.Enabled {
			pair, err := netaddr.ParseIPPrefix(tap.Subnet)
			if err != nil {
				errs = multierror.Append(errs, fmt.Errorf("parsing subnet for VLAN %s host tap external connectivity: %w", tmd.VLAN, err))
			} else if err := disconnectHostTap(host, tap.Name, pair.IP()); err != nil {
				errs = multierror.Append(errs, fmt.Errorf("disconnecting host tap for VLAN %s: %w", tmd.VLAN, err))
			}
		}

		if err := deleteHostTap(host, tmd.Bridge, tap.Name); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("deleting host tap for VLAN %s: %w", tmd.VLAN, err))
		}
	}

	return errs
}

func (this *Tap) unusedPair(tap string) (netaddr.IP, error) {
	// We will use a /30 for each veth pair, starting within some obscure /24 that
	// we think no one would ever use in an experiment.
	// The first thing to do is make sure the /30 is not contained within the
	// network the tap's IP is in. If so, pick a different /8.
	// After that, we need to make sure there are no other running experiments
	// using the /30 we want to use here. If there is, pick a different /30.

	inet, err := netaddr.ParseIPPrefix(tap)
	if err != nil {
		return netaddr.IP{}, fmt.Errorf("parsing tap IP %s: %w", tap, err)
	}

	running, err := types.RunningExperiments()
	if err != nil {
		return netaddr.IP{}, fmt.Errorf("getting running experiments: %w", err)
	}

	var statuses []TapAppStatus

	for _, exp := range running {
		var status TapAppStatus
		if err := exp.Status.ParseAppStatus(this.Name(), &status); err == nil {
			statuses = append(statuses, status)
		}
	}

	// HACK: this is being lazy... figure out the bit-wise way to do this
	incrSubnet := func(octets *[4]byte) error {
		octets[3] += 4

		if octets[3] > 255 {
			octets[3] = 0
			octets[2] += 1

			if octets[2] > 255 {
				octets[2] = 0
				octets[1] += 1

				if octets[1] > 255 {
					return fmt.Errorf("unable to determine next subnet")
				}
			}
		}

		return nil
	}

	octets := [4]byte{10, 213, 47, 0}

	for {
		pair := netaddr.IPPrefixFrom(netaddr.IPFrom4(octets), 30)

		if inet.Contains(pair.IP()) {
			// change 2nd octet since tap network could likely be a /8.
			octets[1] += 1
			continue
		}

		masked := pair.Masked()

		if _, ok := this.pairs[masked]; ok {
			if err := incrSubnet(&octets); err != nil {
				break
			}

			continue
		}

		var collision bool

		for _, s := range statuses {
			for _, t := range s.Taps {
				other, err := netaddr.ParseIPPrefix(t.Subnet)
				if err != nil {
					continue
				}

				if masked.Contains(other.IP()) {
					collision = true
					break
				}
			}

			if collision {
				break
			}
		}

		if collision {
			if err := incrSubnet(&octets); err != nil {
				break
			}

			continue
		}

		this.pairs[masked] = struct{}{}
		return masked.IP(), nil
	}

	return netaddr.IP{}, fmt.Errorf("unable to determine subnet to use for tap pair")
}

func createHostTap(host, bridge, name string, vlan int, ip string) error {
	log.Info("creating tap %s on host %s", name, host)

	cmd := fmt.Sprintf(
		"shell ovs-vsctl add-port %s %s tag=%d -- set interface %s type=internal",
		bridge, name, vlan, name,
	)

	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("creating tap on host %s: %w", host, err)
	}

	log.Info("creating network namespace for tap %s on host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns add %s", name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("creating network namespace on host %s: %w", host, err)
	}

	log.Info("moving tap %s to network namespace on host %s", name, host)

	cmd = fmt.Sprintf("shell ip link set dev %s netns %s", name, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("moving tap to network namespace on host %s: %w", host, err)
	}

	log.Info("bringing tap %s up in network namespace on host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns exec %s ip link set dev %s up", name, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("bringing tap up in network namespace on host %s: %w", host, err)
	}

	log.Info("setting IP address for tap %s in network namespace on host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns exec %s ip addr add %s dev %s", name, ip, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("setting IP address for tap in network namespace on host %s: %w", host, err)
	}

	return nil
}

func deleteHostTap(host, bridge, name string) error {
	var errs error

	log.Info("deleting tap %s from host %s", name, host)

	cmd := fmt.Sprintf("shell ovs-vsctl del-port %s %s", bridge, name)
	if err := meshSend(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting tap %s on host %s: %w", name, host, err))
	}

	log.Info("deleting network namespace %s from host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns delete %s", name)
	if err := meshSend(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting network namespace %s on host %s: %w", name, host, err))
	}

	return errs
}

func connectHostTap(host, name string, subnet netaddr.IP) error {
	var (
		left  = subnet.Next()
		right = subnet.Next().Next()
	)

	log.Info("creating veth pair for tap %s on host %s", name, host)

	cmd := fmt.Sprintf("shell ip link add %s type veth peer %s_right", name, name[:9])
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("creating veth pair for tap %s on host %s: %w", name, host, err)
	}

	log.Info("attaching veth interface to network namespace %s on host %s", name, host)

	cmd = fmt.Sprintf("shell ip link set dev %s_right netns %s", name[:9], name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("attaching veth interface to network namespace %s on host %s: %w", name, host, err)
	}

	log.Info("enabling and configuring veth interface in network namespace %s on host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns exec %s ip link set %s_right name eth0", name, name[:9])
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("renaming veth interface in network namespace %s on host %s: %w", name, host, err)
	}

	cmd = fmt.Sprintf("shell ip netns exec %s ip link set dev eth0 up", name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("enabling veth interface in network namespace %s on host %s: %w", name, host, err)
	}

	cmd = fmt.Sprintf("shell ip netns exec %s ip addr add %s/30 dev eth0", name, right)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("setting IP for veth interface in network namespace %s on host %s: %w", name, host, err)
	}

	cmd = fmt.Sprintf("shell ip netns exec %s ip route add default via %s", name, left)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("setting default route for network namespace %s on host %s: %w", name, host, err)
	}

	log.Info("configuring iptables in network namespace %s on host %s", name, host)

	cmd = fmt.Sprintf("shell ip netns exec %s iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE", name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables masquerading in network namespace %s on host %s: %w", name, host, err)
	}

	log.Info("configuring iptables in the system namespace on host %s", name, host)

	cmd = fmt.Sprintf("shell iptables -t nat -A POSTROUTING -s %s ! -o %s -j MASQUERADE", right, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables masquerading in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("shell iptables -A FORWARD -i %s ! -o %s -j ACCEPT", name, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables forwarding in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("shell iptables -A FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("configuring iptables forwarding in system namespace on host %s: %w", host, err)
	}

	log.Info("enabling and configuring veth interface in the system namespace on host %s", host)

	cmd = fmt.Sprintf("shell ip addr add %s/30 dev %s", left, name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("setting IP for veth interface in system namespace on host %s: %w", host, err)
	}

	cmd = fmt.Sprintf("shell ip link set dev %s up", name)
	if err := meshSend(host, cmd); err != nil {
		return fmt.Errorf("enabling veth interface in system namespace on host %s: %w", host, err)
	}

	return nil
}

func disconnectHostTap(host, name string, subnet netaddr.IP) error {
	var (
		right = subnet.Next().Next()
		errs  error
	)

	log.Info("deleting iptables configs in the system namespace on host %s", name, host)

	cmd := fmt.Sprintf("shell iptables -t nat -D POSTROUTING -s %s ! -o %s -j MASQUERADE", right, name)
	if err := meshSend(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables masquerading in system namespace on host %s: %w", host, err))
	}

	cmd = fmt.Sprintf("shell iptables -D FORWARD -i %s ! -o %s -j ACCEPT", name, name)
	if err := meshSend(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables forwarding in system namespace on host %s: %w", host, err))
	}

	cmd = fmt.Sprintf("shell iptables -D FORWARD -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", name)
	if err := meshSend(host, cmd); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("deleting iptables forwarding in system namespace on host %s: %w", host, err))
	}

	return errs
}

func meshSend(host, command string) error {
	cmd := mmcli.NewCommand()

	if mm.IsHeadnode(host) {
		cmd.Command = command
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s %s", host, command)
	}

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("executing mesh send (%s): %w", cmd.Command, err)
	}

	return nil
}
