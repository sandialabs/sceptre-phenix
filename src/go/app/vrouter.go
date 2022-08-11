package app

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util"
	"phenix/util/mm/mmcli"

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

type DHCPConfig struct {
	ListenAddr string `mapstructure:"listenAddress"`
	Ranges     []struct {
		LowAddr  string `mapstructure:"lowAddress"`
		HighAddr string `mapstructure:"highAddress"`
	} `mapstructure:"ranges"`
	DefaultRoute string            `mapstructure:"defaultRoute"`
	DNS          []string          `mapstructure:"dnsServers"`
	Static       map[string]string `mapstructure:"staticAssignments"`
}

type Emulator struct {
	Ingress    []string `mapstructure:"ingress"`
	Egress     []string `mapstructure:"egress"`
	Name       string   `mapstructure:"name"`
	Bandwidth  string   `mapstructure:"bandwidth"`
	Burst      string   `mapstructure:"burst"`
	Delay      string   `mapstructure:"delay"`
	Corruption string   `mapstructure:"corruption"`
	Loss       string   `mapstructure:"loss"`
	Reordering string   `mapstructure:"reordering"`
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
				fmt.Printf("  === OS Type %s for Node Type %s unsupported ===\n", node.Hardware().OSType(), node.Type())
			}

			continue
		}

		if strings.EqualFold(node.Hardware().OSType(), "linux") {
			fmt.Printf("  === using OS Type 'linux' for Node Type %s is depricated ===\n", node.Type())
			fmt.Printf("  === use 'vyatta', 'vyos', or 'minirouter' OS type instead ===\n")
		}

		var (
			isVyos       = strings.EqualFold(node.Hardware().OSType(), "vyos")
			vrouterDir   = exp.Spec.BaseDir() + "/vrouter"
			vyattaFile   = vrouterDir + "/" + node.General().Hostname() + ".boot"
			vyattaConfig = "/opt/vyatta/etc/config/config.boot"
		)

		if isVyos {
			vyattaConfig = "/boot/vyos/rw/config/config.boot"
		}

		node.AddInject(
			vyattaFile,
			vyattaConfig,
			"", "",
		)

		data := map[string]interface{}{
			"node":     node,
			"ntp-addr": ntpAddr,
			"vyos":     isVyos,
			"passwd":   "vyos", // will only be used if `isVyos` is true
		}

		if passwd, ok := node.GetAnnotation("vrouter/vyos-password"); ok {
			data["passwd"] = passwd // will only be used if `isVyos` is true
		}

		if val, ok := node.GetAnnotation("vrouter/enable-ssh"); ok {
			if addrOrIface, ok := val.(string); ok {
				if ip := net.ParseIP(addrOrIface); ip != nil {
					data["ssh"] = ip.String()
				} else if addr := node.Network().InterfaceAddress(addrOrIface); addr != "" {
					data["ssh"] = addr
				}
			}
		}

		// Check to see if a scenario exists for this experiment and if it contains
		// a "vrouter" app. If so, see if this node has an ipsec metadata entry in
		// the scenario app configuration.
		for _, app := range exp.Apps() {
			if app.Name() == "vrouter" {
				for _, host := range app.Hosts() {
					if host.Hostname() == node.General().Hostname() {
						md := host.Metadata()

						ipsec, err := this.processIPSec(md, node.Network().Interfaces())
						if err != nil {
							return fmt.Errorf("processing IPSec metadata for host %s: %w", host.Hostname(), err)
						}

						data["ipsec"] = ipsec

						if e, ok := md["emulators"]; ok {
							var emulators []Emulator

							if err := mapstructure.Decode(e, &emulators); err != nil {
								return fmt.Errorf("processing emulator metadata for host %s: %w", host.Hostname(), err)
							}

							data["emulators"] = emulators
						}

						break
					}
				}
			}
		}

		if err := os.MkdirAll(vrouterDir, 0755); err != nil {
			return fmt.Errorf("creating experiment vrouter directory path: %w", err)
		}

		if err := tmpl.CreateFileFromTemplate("vyatta.tmpl", data, vyattaFile); err != nil {
			return fmt.Errorf("generating %s config: %w", node.Hardware().OSType(), err)
		}
	}

	return nil
}

func (Vrouter) PostStart(ctx context.Context, exp *types.Experiment) error {
	var app ifaces.ScenarioApp

	// check if experiment contains a scenario config for this app
	for _, a := range exp.Apps() {
		if a.Name() == "vrouter" {
			app = a
			break
		}
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		if !strings.EqualFold(node.Type(), "router") && !strings.EqualFold(node.Type(), "firewall") {
			continue
		}

		if !strings.EqualFold(node.Hardware().OSType(), "minirouter") {
			continue
		}

		if *node.General().DoNotBoot() {
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
				}

				// We need to set the IP address for both static and OSPF interfaces, so we fallthrough here.
				fallthrough
			case "ospf":
				cmd.Command = fmt.Sprintf("router %s interface %d %s/%d", node.General().Hostname(), idx, iface.Address(), iface.Mask())
				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					return fmt.Errorf("configuring interface for router %s: %w", node.General().Hostname(), err)
				}
			case "dhcp":
				cmd.Command = fmt.Sprintf("router %s interface %d dhcp", node.General().Hostname(), idx)
				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					return fmt.Errorf("configuring interface for router %s: %w", node.General().Hostname(), err)
				}
			}
		}

		for _, route := range node.Network().Routes() {
			cmd.Command = fmt.Sprintf("router %s route static %s %s", node.General().Hostname(), route.Destination(), route.Next())
			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("configuring static route for router %s: %w", node.General().Hostname(), err)
			}
		}

		if node.Network().OSPF() != nil {
			cmd.Command = fmt.Sprintf("router %s rid %s", node.General().Hostname(), node.Network().OSPF().RouterID())
			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("configuring router ID for router %s: %w", node.General().Hostname(), err)
			}

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
						}
					}
				}
			}
		}

		for idx, iface := range node.Network().Interfaces() {
			if name := iface.RulesetIn(); name != "" {
				for _, ruleset := range node.Network().Rulesets() {
					if ruleset.Name() == name {
						if err := addChainRules(cmd, node.General().Hostname(), ruleset); err != nil {
							return fmt.Errorf("processing ruleset rules: %w", err)
						}

						cmd.Command = fmt.Sprintf("router %s fw chain %s apply in %d", node.General().Hostname(), ruleset.Name(), idx)
						if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
							return fmt.Errorf("applying firewall chain to interface for router %s: %w", node.General().Hostname(), err)
						}

						commit = true

						break
					}
				}
			}

			if name := iface.RulesetOut(); name != "" {
				for _, ruleset := range node.Network().Rulesets() {
					if ruleset.Name() == name {
						if err := addChainRules(cmd, node.General().Hostname(), ruleset); err != nil {
							return fmt.Errorf("processing ruleset rules: %w", err)
						}

						cmd.Command = fmt.Sprintf("router %s fw chain %s apply out %d", node.General().Hostname(), ruleset.Name(), idx)
						if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
							return fmt.Errorf("applying firewall chain to interface for router %s: %w", node.General().Hostname(), err)
						}

						commit = true

						break
					}
				}
			}
		}

		if app != nil {
			for _, host := range app.Hosts() {
				if host.Hostname() != node.General().Hostname() {
					continue
				}

				md := host.Metadata()

				if _, ok := md["dhcp"]; ok {
					var dhcp []DHCPConfig

					if err := mapstructure.Decode(md["dhcp"], &dhcp); err != nil {
						return fmt.Errorf("decoding DHCP config: %w", err)
					}

					for _, d := range dhcp {
						for _, r := range d.Ranges {
							cmd.Command = fmt.Sprintf("router %s dhcp %s range %s %s", host.Hostname(), d.ListenAddr, r.LowAddr, r.HighAddr)
							if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
								return fmt.Errorf("configuring DHCP range for router %s: %w", host.Hostname(), err)
							}
						}

						if d.DefaultRoute != "" {
							cmd.Command = fmt.Sprintf("router %s dhcp %s router %s", host.Hostname(), d.ListenAddr, d.DefaultRoute)
							if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
								return fmt.Errorf("configuring DHCP default route for router %s: %w", host.Hostname(), err)
							}
						}

						for _, ns := range d.DNS {
							cmd.Command = fmt.Sprintf("router %s dhcp %s dns %s", host.Hostname(), d.ListenAddr, ns)
							if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
								return fmt.Errorf("configuring DHCP DNS server for router %s: %w", host.Hostname(), err)
							}
						}

						for mac, ip := range d.Static {
							cmd.Command = fmt.Sprintf("router %s dhcp %s static %s %s", host.Hostname(), d.ListenAddr, mac, ip)
							if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
								return fmt.Errorf("configuring DHCP static assignment for router %s: %w", host.Hostname(), err)
							}
						}
					}
				}

				if _, ok := md["dns"]; ok {
					var dns map[string]string

					if err := mapstructure.Decode(md["dns"], &dns); err != nil {
						return fmt.Errorf("decoding DNS config: %w", err)
					}

					for ip, name := range dns {
						cmd.Command = fmt.Sprintf("router %s dns %s %s", host.Hostname(), ip, name)
						if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
							return fmt.Errorf("configuring DNS mapping for router %s: %w", host.Hostname(), err)
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

func addChainRules(cmd *mmcli.Command, node string, ruleset ifaces.NodeNetworkRuleset) error {
	for _, rule := range ruleset.Rules() {
		dst := rule.Destination().Address()

		if port := rule.Destination().Port(); port != 0 {
			dst = fmt.Sprintf("%s:%d", dst, port)
		}

		proto := rule.Protocol()

		if rule.Source() != nil {
			src := rule.Source().Address()

			if port := rule.Source().Port(); port != 0 {
				src = fmt.Sprintf("%s:%d", src, port)
			}

			cmd.Command = fmt.Sprintf("router %s fw chain %s action %s %s %s %s", node, ruleset.Name(), rule.Action(), src, dst, proto)
		} else {
			cmd.Command = fmt.Sprintf("router %s fw chain %s action %s %s %s", node, ruleset.Name(), rule.Action(), dst, proto)
		}

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("adding firewall rule for router %s: %w", node, err)
		}

		cmd.Command = fmt.Sprintf("router %s fw chain %s default action %s", node, ruleset.Name(), ruleset.Default())

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("setting default firewall chain action for router %s: %w", node, err)
		}
	}

	return nil
}

func generateSecret(n int) string {
	var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}

	return string(b)
}
