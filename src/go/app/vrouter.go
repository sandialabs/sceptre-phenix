package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"inet.af/netaddr"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

const (
	appNameVrouter    = "vrouter"
	appNameNTP        = "ntp"
	ipsecSecretLength = 32
)

var (
	ErrIPSecConfigNotFound = errors.New("ipsec config not found")
	ErrSNMPConfigNotFound  = errors.New("snmp config not found")
	ErrNTPConfigNotFound   = errors.New("ntp config not found")
)

type ACLConfig struct {
	Ingress  map[string]string `mapstructure:"ingress"`
	Egress   map[string]string `mapstructure:"egress"`
	Rulesets []any             `mapstructure:"rulesets"`
}

type IPSecConfig struct {
	Interfaces map[string]string `mapstructure:"-"`
	Sites      []struct {
		Local        string `mapstructure:"local"`
		Remote       string `mapstructure:"remote"`
		Peer         string `mapstructure:"peer"`
		PresharedKey string `mapstructure:"secret"`
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

type SNMPConfig struct {
	ListenAddr  string `mapstructure:"listenAddress"`
	SystemName  string `mapstructure:"systemName"`
	Location    string `mapstructure:"location"`
	Contact     string `mapstructure:"contact"`
	Communities []struct {
		Name          string   `mapstructure:"name"`
		Authorization string   `mapstructure:"authorization"`
		Clients       []string `mapstructure:"clients"`
		TrapTargets   []string `mapstructure:"trapTargets"`
	} `mapstructure:"communities"`
}

type Emulator struct {
	Ingress    []string `mapstructure:"ingress"`
	Egress     []string `mapstructure:"egress"`
	Name       string   `mapstructure:"name"`
	Bandwidth  string   `mapstructure:"bandwidth"`
	Delay      int      `mapstructure:"delay"`
	Corruption int      `mapstructure:"corruption"`
	Loss       int      `mapstructure:"loss"`
	Reordering int      `mapstructure:"reordering"`
}

type NATRule struct {
	Interface          string `mapstructure:"interface"`
	SourceAddress      string `mapstructure:"srcAddr"`
	SourcePort         int    `mapstructure:"srcPort"`
	DestinationAddress string `mapstructure:"destAddr"`
	DestinationPort    int    `mapstructure:"dstPort"`
	Protocol           string `mapstructure:"protocol"`
	Translation        string `mapstructure:"translation"`

	// Needs to be set via configuration code.
	ifaceIndex int
}

func (r NATRule) InterfaceIndex() int {
	return r.ifaceIndex
}

func (r NATRule) TranslationAddress() string {
	if strings.Contains(r.Translation, ":") {
		parts := strings.Split(r.Translation, ":")

		return parts[0]
	}

	return r.Translation
}

func (r NATRule) TranslationPort() string {
	if strings.Contains(r.Translation, ":") {
		parts := strings.Split(r.Translation, ":")

		return parts[1]
	}

	return ""
}

type Vrouter struct {
	ipsecPresharedKeys map[string]string
}

func (Vrouter) Init(...Option) error {
	return nil
}

func (Vrouter) Name() string {
	return appNameVrouter
}

func (v Vrouter) Configure(ctx context.Context, exp *types.Experiment) error {
	// Check to see if a scenario exists for this experiment and if it contains
	// a "vrouter" app. If so, update the topology with the app's ACL configs.
	for _, app := range exp.Apps() {
		if app.Name() == appNameVrouter {
			for _, host := range app.Hosts() {
				node := exp.Spec.Topology().FindNodeByName(host.Hostname())

				if node == nil {
					//nolint:godox // TODO
					// TODO: handle this better? Like warn the user perhaps?
					continue
				}

				err := v.processACL(host.Metadata(), node.Network())
				if err != nil {
					return fmt.Errorf(
						"processing ACL metadata for host %s: %w",
						host.Hostname(),
						err,
					)
				}
			}
		}
	}

	return nil
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (v *Vrouter) PreStart(ctx context.Context, exp *types.Experiment) error {
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
		if node.External() {
			continue
		}

		if !strings.EqualFold(node.Type(), "router") &&
			!strings.EqualFold(node.Type(), "firewall") {
			continue
		}

		// We ignore os_type `minirouter` here since its config is handled entirely
		// in the post-start stage. We also don't log if `minirouter` since it is
		// supported, just not here. Including os_type `linux` is for legacy
		// support.
		if !util.StringSliceContains(
			[]string{"vyatta", "vyos", "linux"},
			strings.ToLower(node.Hardware().OSType()),
		) {
			if strings.ToLower(node.Hardware().OSType()) != "minirouter" {
				plog.Warn(
					plog.TypePhenixApp,
					"OS Type unsupported for Node Type",
					"os",
					node.Hardware().OSType(),
					"type",
					node.Type(),
				)
			}

			continue
		}

		if strings.EqualFold(node.Hardware().OSType(), "linux") {
			plog.Warn(
				plog.TypePhenixApp,
				"OS Type 'linux' for Node Type is deprecated; use 'vyatta', 'vyos', or 'minirouter' instead",
				"type",
				node.Type(),
			)
		}

		var (
			isVyos       = strings.EqualFold(node.Hardware().OSType(), "vyos")
			vrouterDir   = exp.Spec.BaseDir() + "/vrouter"
			vyattaFile   = vrouterDir + "/" + node.General().Hostname() + ".boot"
			vyattaConfig = "/opt/vyatta/etc/config/config.boot"
		)

		if isVyos {
			vyattaConfig = "/boot/vyos/rw/config/scripts/custom/vyos.script"
		}

		node.AddInject(
			vyattaFile,
			vyattaConfig,
			"", "",
		)

		if ntpAddr == "" {
			var err error

			if ntpAddr, err = configureNTP(
				exp,
				node.General().Hostname(),
			); err != nil &&
				!errors.Is(err, ErrNTPConfigNotFound) {
				return fmt.Errorf("configuring NTP for host %s: %w", node.General().Hostname(), err)
			}
		}

		data := map[string]any{
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
			if app.Name() == appNameVrouter {
				for _, host := range app.Hosts() {
					if host.Hostname() == node.General().Hostname() {
						md := host.Metadata()

						ipsec, err := v.processIPSec(md, node.Network().Interfaces())
						if err != nil && !errors.Is(err, ErrIPSecConfigNotFound) {
							return fmt.Errorf(
								"processing IPSec metadata for host %s: %w",
								host.Hostname(),
								err,
							)
						}

						data["ipsec"] = ipsec

						if e, ok := md["emulators"]; ok {
							var emulators []Emulator

							err := mapstructure.Decode(e, &emulators)
							if err != nil {
								return fmt.Errorf(
									"processing emulator metadata for host %s: %w",
									host.Hostname(),
									err,
								)
							}

							data["emulators"] = emulators
						}

						snmp, err := v.processSNMP(md)
						if err != nil && !errors.Is(err, ErrSNMPConfigNotFound) {
							return fmt.Errorf(
								"processing SNMP metadata for host %s: %w",
								host.Hostname(),
								err,
							)
						}

						data["snmp"] = snmp

						sources, destinations, err := v.processNAT(md, node.Network().Interfaces())
						if err != nil {
							return fmt.Errorf(
								"processing NAT metadata for host %s: %w",
								host.Hostname(),
								err,
							)
						}

						data["snat"] = sources
						data["dnat"] = destinations

						break
					}
				}

				break
			}
		}

		// If app host metadata didn't include NAT configs, then see if NAT was
		// specified in the topology network config.
		if _, ok := data["nat"]; !ok {
			var sources []NATRule

			for _, n := range node.Network().NAT() {
				ifaceIndex := -1

				var rules []NATRule

				for i, iface := range node.Network().Interfaces() {
					if iface.Name() == n.Out() {
						ifaceIndex = i

						continue
					}

					if iface.Type() != "ethernet" || iface.Proto() != "static" {
						continue
					}

					if !util.StringSliceContains(n.In(), iface.Name()) {
						continue
					}

					net := netaddr.IPPrefixFrom(
						netaddr.MustParseIP(iface.Address()),
						uint8(iface.Mask()), //nolint:gosec // integer overflow conversion int -> uint8
					)

					rules = append(rules, NATRule{SourceAddress: net.Masked().String()}) //nolint:exhaustruct // partial initialization
				}

				if ifaceIndex < 0 {
					return fmt.Errorf(
						"nat outbound interface %s specified for host %s not found",
						n.Out(),
						node.General().Hostname(),
					)
				}

				// Go back and set the outbound interface index for each rule.
				for i, rule := range rules {
					rule.ifaceIndex = ifaceIndex
					rules[i] = rule
				}

				sources = append(sources, rules...)
			}

			if len(sources) > 0 {
				data["snat"] = sources
			}
		}

		err := os.MkdirAll(vrouterDir, 0o750)
		if err != nil {
			return fmt.Errorf("creating experiment vrouter directory path: %w", err)
		}

		err = tmpl.CreateFileFromTemplate("vyatta.tmpl", data, vyattaFile)
		if err != nil {
			return fmt.Errorf("generating %s config: %w", node.Hardware().OSType(), err)
		}
	}

	return nil
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (Vrouter) PostStart(ctx context.Context, exp *types.Experiment) error {
	var app ifaces.ScenarioApp

	// check if experiment contains a scenario config for this app
	for _, a := range exp.Apps() {
		if a.Name() == appNameVrouter {
			app = a

			break
		}
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		if !strings.EqualFold(node.Type(), "router") &&
			!strings.EqualFold(node.Type(), "firewall") {
			continue
		}

		if !strings.EqualFold(node.Hardware().OSType(), "minirouter") {
			continue
		}

		if *node.General().DoNotBoot() {
			continue
		}

		cmd := mmcli.NewNamespacedCommand(exp.Metadata.Name)

		for idx, iface := range node.Network().Interfaces() {
			switch strings.ToLower(iface.Proto()) {
			case "static":
				// We only want to set a default route if OSPF isn't being used.
				if iface.Gateway() != "" {
					cmd.Command = fmt.Sprintf(
						"router %s gw %s",
						node.General().Hostname(),
						iface.Gateway(),
					)

					err := mmcli.ErrorResponse(mmcli.Run(cmd))
					if err != nil {
						return fmt.Errorf(
							"configuring default gateway for router %s: %w",
							node.General().Hostname(),
							err,
						)
					}
				}

				// We need to set the IP address for both static and OSPF interfaces, so we fallthrough here.
				fallthrough
			case "ospf":
				cmd.Command = fmt.Sprintf(
					"router %s interface %d %s/%d",
					node.General().Hostname(),
					idx,
					iface.Address(),
					iface.Mask(),
				)

				err := mmcli.ErrorResponse(mmcli.Run(cmd))
				if err != nil {
					return fmt.Errorf(
						"configuring interface for router %s: %w",
						node.General().Hostname(),
						err,
					)
				}
			case "dhcp":
				cmd.Command = fmt.Sprintf(
					"router %s interface %d dhcp",
					node.General().Hostname(),
					idx,
				)

				err := mmcli.ErrorResponse(mmcli.Run(cmd))
				if err != nil {
					return fmt.Errorf(
						"configuring interface for router %s: %w",
						node.General().Hostname(),
						err,
					)
				}
			}
		}

		for _, route := range node.Network().Routes() {
			cmd.Command = fmt.Sprintf(
				"router %s route static %s %s",
				node.General().Hostname(),
				route.Destination(),
				route.Next(),
			)

			err := mmcli.ErrorResponse(mmcli.Run(cmd))
			if err != nil {
				return fmt.Errorf(
					"configuring static route for router %s: %w",
					node.General().Hostname(),
					err,
				)
			}
		}

		if node.Network().OSPF() != nil {
			cmd.Command = fmt.Sprintf(
				"router %s rid %s",
				node.General().Hostname(),
				node.Network().OSPF().RouterID(),
			)

			err := mmcli.ErrorResponse(mmcli.Run(cmd))
			if err != nil {
				return fmt.Errorf(
					"configuring router ID for router %s: %w",
					node.General().Hostname(),
					err,
				)
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

							cmd.Command = fmt.Sprintf(
								"router %s route ospf %d %d",
								node.General().Hostname(),
								aid,
								idx,
							)

							err := mmcli.ErrorResponse(mmcli.Run(cmd))
							if err != nil {
								return fmt.Errorf(
									"configuring OSPF area network for router %s: %w",
									node.General().Hostname(),
									err,
								)
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
						err := addChainRules(cmd, node.General().Hostname(), ruleset)
						if err != nil {
							return fmt.Errorf("processing ruleset rules: %w", err)
						}

						cmd.Command = fmt.Sprintf(
							"router %s fw chain %s apply in %d",
							node.General().Hostname(),
							ruleset.Name(),
							idx,
						)

						err = mmcli.ErrorResponse(mmcli.Run(cmd))
						if err != nil {
							return fmt.Errorf(
								"applying firewall chain to interface for router %s: %w",
								node.General().Hostname(),
								err,
							)
						}

						break
					}
				}
			}

			if name := iface.RulesetOut(); name != "" {
				for _, ruleset := range node.Network().Rulesets() {
					if ruleset.Name() == name {
						err := addChainRules(cmd, node.General().Hostname(), ruleset)
						if err != nil {
							return fmt.Errorf("processing ruleset rules: %w", err)
						}

						cmd.Command = fmt.Sprintf(
							"router %s fw chain %s apply out %d",
							node.General().Hostname(),
							ruleset.Name(),
							idx,
						)

						err = mmcli.ErrorResponse(mmcli.Run(cmd))
						if err != nil {
							return fmt.Errorf(
								"applying firewall chain to interface for router %s: %w",
								node.General().Hostname(),
								err,
							)
						}

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

					err := mapstructure.Decode(md["dhcp"], &dhcp)
					if err != nil {
						return fmt.Errorf("decoding DHCP config: %w", err)
					}

					for _, d := range dhcp {
						for _, r := range d.Ranges {
							cmd.Command = fmt.Sprintf(
								"router %s dhcp %s range %s %s",
								host.Hostname(),
								d.ListenAddr,
								r.LowAddr,
								r.HighAddr,
							)

							err := mmcli.ErrorResponse(mmcli.Run(cmd))
							if err != nil {
								return fmt.Errorf(
									"configuring DHCP range for router %s: %w",
									host.Hostname(),
									err,
								)
							}
						}

						if d.DefaultRoute != "" {
							cmd.Command = fmt.Sprintf(
								"router %s dhcp %s router %s",
								host.Hostname(),
								d.ListenAddr,
								d.DefaultRoute,
							)

							err := mmcli.ErrorResponse(mmcli.Run(cmd))
							if err != nil {
								return fmt.Errorf(
									"configuring DHCP default route for router %s: %w",
									host.Hostname(),
									err,
								)
							}
						}

						for _, ns := range d.DNS {
							cmd.Command = fmt.Sprintf(
								"router %s dhcp %s dns %s",
								host.Hostname(),
								d.ListenAddr,
								ns,
							)

							err := mmcli.ErrorResponse(mmcli.Run(cmd))
							if err != nil {
								return fmt.Errorf(
									"configuring DHCP DNS server for router %s: %w",
									host.Hostname(),
									err,
								)
							}
						}

						for mac, ip := range d.Static {
							cmd.Command = fmt.Sprintf(
								"router %s dhcp %s static %s %s",
								host.Hostname(),
								d.ListenAddr,
								mac,
								ip,
							)

							err := mmcli.ErrorResponse(mmcli.Run(cmd))
							if err != nil {
								return fmt.Errorf(
									"configuring DHCP static assignment for router %s: %w",
									host.Hostname(),
									err,
								)
							}
						}
					}
				}

				if _, ok := md["dns"]; ok {
					var dns map[string]string

					err := mapstructure.Decode(md["dns"], &dns)
					if err != nil {
						return fmt.Errorf("decoding DNS config: %w", err)
					}

					for ip, name := range dns {
						cmd.Command = fmt.Sprintf("router %s dns %s %s", host.Hostname(), ip, name)

						err := mmcli.ErrorResponse(mmcli.Run(cmd))
						if err != nil {
							return fmt.Errorf(
								"configuring DNS mapping for router %s: %w",
								host.Hostname(),
								err,
							)
						}
					}
				}
			}
		}

		cmd.Command = fmt.Sprintf("router %s commit", node.General().Hostname())

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("committing config for router %s: %w", node.General().Hostname(), err)
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

//nolint:funlen // complex logic
func (Vrouter) processACL(md map[string]any, network ifaces.NodeNetwork) error {
	if _, ok := md["acl"]; !ok {
		return nil
	}

	var acl ACLConfig

	err := mapstructure.Decode(md["acl"], &acl)
	if err != nil {
		return fmt.Errorf("decoding ACL ingress/egress config: %w", err)
	}

	for _, rule := range acl.Rulesets {
		iface, _ := version.GetStoredSpecForKind("Ruleset")

		err := mapstructure.Decode(rule, &iface)
		if err != nil {
			return fmt.Errorf("decoding ACL ruleset: %w", err)
		}

		ruleset, ok := iface.(ifaces.NodeNetworkRuleset)
		if !ok {
			//nolint:godox // TODO
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

func (v *Vrouter) processIPSec(
	md map[string]any,
	nets []ifaces.NodeNetworkInterface,
) (*IPSecConfig, error) {
	if _, ok := md["ipsec"]; !ok {
		return nil, ErrIPSecConfigNotFound
	}

	if v.ipsecPresharedKeys == nil {
		v.ipsecPresharedKeys = make(map[string]string)
	}

	var ipsec IPSecConfig

	err := mapstructure.Decode(md, &ipsec)
	if err != nil {
		return nil, fmt.Errorf("decoding IPSec config: %w", err)
	}

	ipsec.Interfaces = make(map[string]string)

	for idx, site := range ipsec.Sites {
		var found bool

		for i, net := range nets {
			if site.Local == net.Address() {
				iface := fmt.Sprintf("eth%d", i)
				ipsec.Interfaces[iface] = iface

				found = true

				break
			}
		}

		if !found {
			return nil, fmt.Errorf("no router interface found for local address %s", site.Local)
		}

		if site.PresharedKey == "" {
			k := site.Local + "-" + site.Remote

			if key, ok := v.ipsecPresharedKeys[k]; ok {
				site.PresharedKey = key
			} else {
				k := site.Remote + "-" + site.Local

				if key, ok := v.ipsecPresharedKeys[k]; ok {
					site.PresharedKey = key
				} else {
					key := generateSecret(ipsecSecretLength)

					v.ipsecPresharedKeys[k] = key
					site.PresharedKey = key
				}
			}
		}

		ipsec.Sites[idx] = site
	}

	return &ipsec, nil
}

func (Vrouter) processSNMP(md map[string]any) (*SNMPConfig, error) {
	raw, ok := md["snmp"]
	if !ok {
		return nil, ErrSNMPConfigNotFound
	}

	var snmp SNMPConfig

	err := mapstructure.Decode(raw, &snmp)
	if err != nil {
		return nil, fmt.Errorf("decoding SNMP config: %w", err)
	}

	for cidx := range snmp.Communities {
		community := &snmp.Communities[cidx]
		if community.Name == "" {
			return nil, fmt.Errorf("snmp community at index %d must include a name", cidx)
		}
	}

	if snmp.ListenAddr == "" && snmp.SystemName == "" && snmp.Location == "" &&
		snmp.Contact == "" &&
		len(snmp.Communities) == 0 {
		return nil, ErrSNMPConfigNotFound
	}

	return &snmp, nil
}

func (v *Vrouter) processNAT(
	md map[string]any,
	nets []ifaces.NodeNetworkInterface,
) ([]NATRule, []NATRule, error) {
	var (
		sources      []NATRule
		destinations []NATRule
	)

	if _, ok := md["snat"]; ok {
		err := mapstructure.Decode(md["snat"], &sources)
		if err != nil {
			return nil, nil, fmt.Errorf("processing SNAT metadata: %w", err)
		}

		for i, rule := range sources {
			rule.ifaceIndex = -1

			for j, iface := range nets {
				if rule.Interface == iface.Name() {
					rule.ifaceIndex = j
					sources[i] = rule

					break
				}
			}

			if rule.ifaceIndex < 0 {
				return nil, nil, fmt.Errorf("nat outbound interface %s not found", rule.Interface)
			}
		}
	}

	if _, ok := md["dnat"]; ok {
		err := mapstructure.Decode(md["dnat"], &destinations)
		if err != nil {
			return nil, nil, fmt.Errorf("processing DNAT metadata: %w", err)
		}

		for i, rule := range destinations {
			rule.ifaceIndex = -1

			for j, iface := range nets {
				if rule.Interface == iface.Name() {
					rule.ifaceIndex = j
					destinations[i] = rule

					break
				}
			}

			if rule.ifaceIndex < 0 {
				return nil, nil, fmt.Errorf("nat inbound interface %s not found", rule.Interface)
			}
		}
	}

	return sources, destinations, nil
}

func configureNTP(exp *types.Experiment, hostname string) (string, error) {
	// Check to see if a scenario exists for this experiment and if it contains
	// a "ntp" app. If so, use it to configure NTP for the experiment.
	for _, app := range exp.Apps() {
		if app.Name() == appNameNTP {
			var amd NTPAppMetadata

			_ = mapstructure.Decode(app.Metadata(), &amd)

			// Might be an empty string, but that's okay... for now.
			defaultSource := amd.DefaultSource.IPAddress(exp)

			for _, host := range app.Hosts() {
				if host.Hostname() != hostname {
					continue
				}

				var hmd NTPAppHostMetadata
				_ = mapstructure.Decode(host.Metadata(), &hmd)

				source := hmd.Source.IPAddress(exp)

				if hmd.Client == appNameNTP {
					if source == "" {
						if defaultSource == "" {
							return "", fmt.Errorf(
								"no NTP source configured for host %s (and no default source configured)",
								host.Hostname(),
							)
						}

						source = defaultSource
					}

					return source, nil
				} else {
					return "", fmt.Errorf(
						"the only valid NTP client for vrouters is 'ntp' (%s was provided)",
						hmd.Client,
					)
				}
			}
		}
	}

	return "", ErrNTPConfigNotFound
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

			cmd.Command = fmt.Sprintf(
				"router %s fw chain %s action %s %s %s %s",
				node,
				ruleset.Name(),
				rule.Action(),
				src,
				dst,
				proto,
			)
		} else {
			cmd.Command = fmt.Sprintf(
				"router %s fw chain %s action %s %s %s",
				node,
				ruleset.Name(),
				rule.Action(),
				dst,
				proto,
			)
		}

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("adding firewall rule for router %s: %w", node, err)
		}

		cmd.Command = fmt.Sprintf(
			"router %s fw chain %s default action %s",
			node,
			ruleset.Name(),
			ruleset.Default(),
		)

		err = mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("setting default firewall chain action for router %s: %w", node, err)
		}
	}

	return nil
}

func generateSecret(n int) string {
	rng := rand.New(rand.NewPCG(uint64(time.Now().UTC().UnixNano()), 1)) //nolint:gosec // weak random number generator

	chars := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rng.IntN(len(chars))]
	}

	return string(b)
}
