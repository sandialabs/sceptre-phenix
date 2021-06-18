package app

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"phenix/internal/mm/mmcli"
	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util"

	"github.com/mitchellh/mapstructure"
)

type ACLConfig struct {
	Ingress  map[string]string `mapstructure:"ingress"`
	Egress   map[string]string `mapstructure:"egress"`
	Rulesets []interface{}     `mapstructure:"rulesets"`
}

type IPSecConfig struct {
	Interfaces map[string]string `mapstructure:"-"`
	Sites      []struct {
		Local        string `mapstructure:"local"`
		Peer         string `mapstructure:"peer"`
		PresharedKey string `mapstructure:"-"`
		Tunnels      []struct {
			Local  string `mapstructure:"local"`
			Remote string `mapstructure:"remote"`
		} `mapstructure:"tunnels"`
	} `mapstructure:"ipsec"`
}

type Vrouter struct {
	ipsecPresharedKeys map[string]string
}

func (Vrouter) Init(...Option) error {
	return nil
}

func (Vrouter) Name() string {
	return "vrouter"
}

func (this Vrouter) Configure(ctx context.Context, exp *types.Experiment) error {
	// Check to see if a scenario exists for this experiment and if it contains
	// a "vrouter" app. If so, update the topology with the app's ACL configs.
	for _, app := range exp.Apps() {
		if app.Name() == "vrouter" {
			for _, host := range app.Hosts() {
				node := exp.Spec.Topology().FindNodeByName(host.Hostname())

				if node == nil {
					// TODO: handle this better? Like warn the user perhaps?
					continue
				}

				if err := this.processACL(host.Metadata(), node.Network()); err != nil {
					return fmt.Errorf("processing ACL metadata for host %s: %w", host.Hostname(), err)
				}
			}
		}
	}

	return nil
}

func (this *Vrouter) PreStart(ctx context.Context, exp *types.Experiment) error {
	var (
		ntpServers = exp.Spec.Topology().FindNodesWithLabels("ntp-server")
		ntpAddr    string
	)

	if len(ntpServers) != 0 {
		// Just take first server if more than one are labeled.
		for _, iface := range ntpServers[0].Network().Interfaces() {
			if strings.EqualFold(iface.VLAN(), "mgmt") {
				ntpAddr = iface.Address()
				break
			}
		}
	}

	// loop through nodes
	for _, node := range exp.Spec.Topology().Nodes() {
		if !strings.EqualFold(node.Type(), "router") && !strings.EqualFold(node.Type(), "firewall") {
			continue
		}

		// We ignore os_type `minirouter` here since its config is handled entirely
		// in the post-start stage. We also don't log if `minirouter` since it is
		// supported, just not here. Including os_type `linux` is for legacy
		// support.
		if !util.StringSliceContains([]string{"vyatta", "vyos", "linux"}, strings.ToLower(node.Hardware().OSType())) {
			if strings.ToLower(node.Hardware().OSType()) != "minirouter" {
				fmt.Printf("  === OS Type %s for Node Type %s unsupported ===", node.Hardware().OSType(), node.Type())
			}

			continue
		}

		vrouterDir := exp.Spec.BaseDir() + "/vrouter"
		vyattaFile := vrouterDir + "/" + node.General().Hostname() + ".boot"

		node.AddInject(
			vyattaFile,
			"/opt/vyatta/etc/config/config.boot",
			"", "",
		)

		data := map[string]interface{}{
			"node":     node,
			"ntp-addr": ntpAddr,
		}

		// Check to see if a scenario exists for this experiment and if it contains
		// a "vrouter" app. If so, see if this node has an ipsec metadata entry in
		// the scenario app configuration.
		for _, app := range exp.Apps() {
			if app.Name() == "vrouter" {
				for _, host := range app.Hosts() {
					if host.Hostname() == node.General().Hostname() {
						ipsec, err := this.processIPSec(host.Metadata(), node.Network().Interfaces())
						if err != nil {
							return fmt.Errorf("processing IPSec metadata for host %s: %w", host.Hostname(), err)
						}

						data["ipsec"] = ipsec

						break
					}
				}
			}
		}

		if err := os.MkdirAll(vrouterDir, 0755); err != nil {
			return fmt.Errorf("creating experiment vrouter directory path: %w", err)
		}

		if err := tmpl.CreateFileFromTemplate("vyatta.tmpl", data, vyattaFile); err != nil {
			return fmt.Errorf("generating vyatta config: %w", err)
		}
	}

	return nil
}

func (Vrouter) PostStart(ctx context.Context, exp *types.Experiment) error {
	for _, node := range exp.Spec.Topology().Nodes() {
		if !strings.EqualFold(node.Type(), "router") && !strings.EqualFold(node.Type(), "firewall") {
			continue
		}

		if !strings.EqualFold(node.Hardware().OSType(), "minirouter") {
			continue
		}

		var (
			commit bool
			cmd    = mmcli.NewNamespacedCommand(exp.Metadata.Name)
		)

		for idx, iface := range node.Network().Interfaces() {
			switch strings.ToLower(iface.Proto()) {
			case "static":
				// We only want to set a default route if OSPF isn't being used.
				if iface.Gateway() != "" {
					cmd.Command = fmt.Sprintf("router %s gw %s", node.General().Hostname(), iface.Gateway())
					if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
						return fmt.Errorf("configuring default gateway for router %s: %w", node.General().Hostname(), err)
					}

					commit = true
				}

				// We need to set the IP address for both static and OSPF interfaces, so we fallthrough here.
				fallthrough
			case "ospf":
				cmd.Command = fmt.Sprintf("router %s interface %d %s/%d", node.General().Hostname(), idx, iface.Address(), iface.Mask())
				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					return fmt.Errorf("configuring interface for router %s: %w", node.General().Hostname(), err)
				}

				commit = true
			case "dhcp":
				cmd.Command = fmt.Sprintf("router %s interface %d dhcp", node.General().Hostname(), idx)
				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					return fmt.Errorf("configuring interface for router %s: %w", node.General().Hostname(), err)
				}

				commit = true
			}
		}

		for _, route := range node.Network().Routes() {
			cmd.Command = fmt.Sprintf("router %s route static %s %s", node.General().Hostname(), route.Destination(), route.Next())
			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("configuring static route for router %s: %w", node.General().Hostname(), err)
			}

			commit = true
		}

		if node.Network().OSPF() != nil {
			cmd.Command = fmt.Sprintf("router %s rid %s", node.General().Hostname(), node.Network().OSPF().RouterID())
			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("configuring router ID for router %s: %w", node.General().Hostname(), err)
			}

			commit = true

			for _, area := range node.Network().OSPF().Areas() {
				for idx, iface := range node.Network().Interfaces() {
					if !strings.EqualFold(iface.Proto(), "ospf") {
						continue
					}

					ip := net.ParseIP(iface.Address())
					if ip == nil {
						continue
					}

					for _, network := range area.AreaNetworks() {
						_, ipnet, err := net.ParseCIDR(network.Network())
						if err != nil {
							continue
						}

						if ipnet.Contains(ip) {
							var aid int // assume area ID of 0 if not provided

							if area.AreaID() != nil {
								aid = *area.AreaID()
							}

							cmd.Command = fmt.Sprintf("router %s route ospf %d %d", node.General().Hostname(), aid, idx)
							if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
								return fmt.Errorf("configuring OSPF area network for router %s: %w", node.General().Hostname(), err)
							}

							commit = true
						}
					}
				}
			}
		}

		if commit {
			cmd.Command = fmt.Sprintf("router %s commit", node.General().Hostname())
			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("committing config for router %s: %w", node.General().Hostname(), err)
			}
		}
	}

	return nil
}

func (Vrouter) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Vrouter) Cleanup(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Vrouter) processACL(md map[string]interface{}, network ifaces.NodeNetwork) error {
	if _, ok := md["acl"]; !ok {
		return nil
	}

	var acl ACLConfig

	if err := mapstructure.Decode(md["acl"], &acl); err != nil {
		return fmt.Errorf("decoding ACL ingress/egress config: %w", err)
	}

	for _, rule := range acl.Rulesets {
		iface, _ := version.GetStoredSpecForKind("Ruleset")

		if err := mapstructure.Decode(rule, &iface); err != nil {
			return fmt.Errorf("decoding ACL ruleset: %w", err)
		}

		ruleset, ok := iface.(ifaces.NodeNetworkRuleset)
		if !ok {
			// TODO: handle this better? Like warn the user perhaps?
			continue
		}

		var exists bool

		for _, r := range network.Rulesets() {
			if ruleset.Name() == r.Name() {
				exists = true
				break
			}
		}

		if !exists {
			network.AddRuleset(ruleset)
		}
	}

	for iface, rule := range acl.Ingress {
		var found bool

		for _, ruleset := range network.Rulesets() {
			if rule == ruleset.Name() {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("no ruleset named %s (for interface %s ingress) found", rule, iface)
		}

		found = false

		for _, net := range network.Interfaces() {
			if iface == net.Name() {
				net.SetRulesetOut(rule)

				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("no interface named %s (for ruleset %s) found", iface, rule)
		}
	}

	for iface, rule := range acl.Egress {
		var found bool

		for _, ruleset := range network.Rulesets() {
			if rule == ruleset.Name() {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("no ruleset named %s (for interface %s egress) found", rule, iface)
		}

		found = false

		for _, net := range network.Interfaces() {
			if iface == net.Name() {
				net.SetRulesetIn(rule)

				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("no interface named %s (for ruleset %s) found", iface, rule)
		}
	}

	return nil
}

func (this *Vrouter) processIPSec(md map[string]interface{}, nets []ifaces.NodeNetworkInterface) (*IPSecConfig, error) {
	if _, ok := md["ipsec"]; !ok {
		return nil, nil
	}

	if this.ipsecPresharedKeys == nil {
		this.ipsecPresharedKeys = make(map[string]string)
		rand.Seed(time.Now().UTC().UnixNano())
	}

	var ipsec IPSecConfig

	if err := mapstructure.Decode(md, &ipsec); err != nil {
		return nil, fmt.Errorf("decoding IPSec config: %w", err)
	}

	ipsec.Interfaces = make(map[string]string)

	for idx, site := range ipsec.Sites {
		var found bool

		for idx, net := range nets {
			if site.Local == net.Address() {
				iface := fmt.Sprintf("eth%d", idx)
				ipsec.Interfaces[iface] = iface

				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("no router interface found for local address %s", site.Local)
		}

		k := site.Local + "-" + site.Peer

		if key, ok := this.ipsecPresharedKeys[k]; ok {
			site.PresharedKey = key
		} else {
			k := site.Peer + "-" + site.Local

			if key, ok := this.ipsecPresharedKeys[k]; ok {
				site.PresharedKey = key
			} else {
				key := generateSecret(32)

				this.ipsecPresharedKeys[k] = key
				site.PresharedKey = key
			}
		}

		ipsec.Sites[idx] = site
	}

	return &ipsec, nil
}

func generateSecret(n int) string {
	var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}

	return string(b)
}
