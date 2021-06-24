package v1

import (
	"fmt"
	"net"
	"strings"

	ifaces "phenix/types/interfaces"
)

type Network struct {
	InterfacesF []*Interface `json:"interfaces" yaml:"interfaces" structs:"interfaces" mapstructure:"interfaces"`
	RoutesF     []Route      `json:"routes" yaml:"routes" structs:"routes" mapstructure:"routes"`
	OSPFF       *OSPF        `json:"ospf" yaml:"ospf" structs:"ospf" mapstructure:"ospf"`
	RulesetsF   []*Ruleset   `json:"rulesets" yaml:"rulesets" structs:"rulesets" mapstructure:"rulesets"`
}

func (this *Network) Interfaces() []ifaces.NodeNetworkInterface {
	if this == nil {
		return nil
	}

	interfaces := make([]ifaces.NodeNetworkInterface, len(this.InterfacesF))

	for i, iface := range this.InterfacesF {
		interfaces[i] = iface
	}

	return interfaces
}

func (this *Network) Routes() []ifaces.NodeNetworkRoute {
	if this == nil {
		return nil
	}

	routes := make([]ifaces.NodeNetworkRoute, len(this.RoutesF))

	for i, r := range this.RoutesF {
		routes[i] = r
	}

	return routes
}

func (this *Network) OSPF() ifaces.NodeNetworkOSPF {
	if this == nil {
		return nil
	}

	// fun times... https://glucn.medium.com/golang-an-interface-holding-a-nil-value-is-not-nil-bb151f472cc7
	// probably other places we need to do this too... :shrug:
	if this.OSPFF == nil {
		return nil
	}

	return this.OSPFF
}

func (this *Network) Rulesets() []ifaces.NodeNetworkRuleset {
	if this == nil {
		return nil
	}

	sets := make([]ifaces.NodeNetworkRuleset, len(this.RulesetsF))

	for i, r := range this.RulesetsF {
		sets[i] = r
	}

	return sets
}

func (this *Network) SetRulesets(rules []ifaces.NodeNetworkRuleset) {
	sets := make([]*Ruleset, len(rules))

	for i, r := range rules {
		sets[i] = r.(*Ruleset)
	}

	this.RulesetsF = sets
}

func (this *Network) AddRuleset(rule ifaces.NodeNetworkRuleset) {
	this.RulesetsF = append(this.RulesetsF, rule.(*Ruleset))
}

type Interface struct {
	NameF       string `json:"name" yaml:"name" structs:"name" mapstructure:"name"`
	TypeF       string `json:"type" yaml:"type" structs:"type" mapstructure:"type"`
	ProtoF      string `json:"proto" yaml:"proto" structs:"proto" mapstructure:"proto"`
	UDPPortF    int    `json:"udp_port" yaml:"udp_port" structs:"udp_port" mapstructure:"udp_port"`
	BaudRateF   int    `json:"baud_rate" yaml:"baud_rate" structs:"baud_rate" mapstructure:"baud_rate"`
	DeviceF     string `json:"device" yaml:"device" structs:"device" mapstructure:"device"`
	VLANF       string `json:"vlan" yaml:"vlan" structs:"vlan" mapstructure:"vlan"`
	BridgeF     string `json:"bridge" yaml:"bridge" structs:"bridge" mapstructure:"bridge"`
	AutostartF  bool   `json:"autostart" yaml:"autostart" structs:"autostart" mapstructure:"autostart"`
	MACF        string `json:"mac" yaml:"mac" structs:"mac" mapstructure:"mac"`
	MTUF        int    `json:"mtu" yaml:"mtu" structs:"mtu" mapstructure:"mtu"`
	AddressF    string `json:"address" yaml:"address" structs:"address" mapstructure:"address"`
	MaskF       int    `json:"mask" yaml:"mask" structs:"mask" mapstructure:"mask"`
	GatewayF    string `json:"gateway" yaml:"gateway" structs:"gateway" mapstructure:"gateway"`
	RulesetInF  string `json:"ruleset_in" yaml:"ruleset_in" structs:"ruleset_in" mapstructure:"ruleset_in"`
	RulesetOutF string `json:"ruleset_out" yaml:"ruleset_out" structs:"ruleset_out" mapstructure:"ruleset_out"`
}

func (this Interface) Name() string {
	return this.NameF
}

func (this Interface) Type() string {
	return this.TypeF
}

func (this Interface) Proto() string {
	return this.ProtoF
}

func (this Interface) UDPPort() int {
	return this.UDPPortF
}

func (this Interface) BaudRate() int {
	return this.BaudRateF
}

func (this Interface) Device() string {
	return this.DeviceF
}

func (this Interface) VLAN() string {
	return this.VLANF
}

func (this Interface) Bridge() string {
	return this.BridgeF
}

func (this Interface) Autostart() bool {
	return this.AutostartF
}

func (this Interface) MAC() string {
	return this.MACF
}

func (this Interface) MTU() int {
	return this.MTUF
}

func (this Interface) Address() string {
	return this.AddressF
}

func (this Interface) Mask() int {
	return this.MaskF
}

func (this Interface) Gateway() string {
	return this.GatewayF
}

func (this Interface) RulesetIn() string {
	return this.RulesetInF
}

func (this Interface) RulesetOut() string {
	return this.RulesetOutF
}

func (this *Interface) SetName(name string) {
	this.NameF = name
}

func (this *Interface) SetType(typ string) {
	this.TypeF = typ
}

func (this *Interface) SetProto(proto string) {
	this.ProtoF = proto
}

func (this *Interface) SetUDPPort(port int) {
	this.UDPPortF = port
}

func (this *Interface) SetBaudRate(rate int) {
	this.BaudRateF = rate
}

func (this *Interface) SetDevice(dev string) {
	this.DeviceF = dev
}

func (this *Interface) SetVLAN(vlan string) {
	this.VLANF = vlan
}

func (this *Interface) SetBridge(br string) {
	this.BridgeF = br
}

func (this *Interface) SetAutostart(auto bool) {
	this.AutostartF = auto
}

func (this *Interface) SetMAC(mac string) {
	this.MACF = mac
}

func (this *Interface) SetMTU(mtu int) {
	this.MTUF = mtu
}

func (this *Interface) SetAddress(addr string) {
	this.AddressF = addr
}

func (this *Interface) SetMask(mask int) {
	this.MaskF = mask
}

func (this *Interface) SetGateway(gw string) {
	this.GatewayF = gw
}

func (this *Interface) SetRulesetIn(rule string) {
	this.RulesetInF = rule
}

func (this *Interface) SetRulesetOut(rule string) {
	this.RulesetOutF = rule
}

type Route struct {
	DestinationF string `json:"destination" yaml:"destination" structs:"destination" mapstructure:"destination"`
	NextF        string `json:"next" yaml:"next" structs:"next" mapstructure:"next"`
	CostF        *int   `json:"cost" yaml:"cost" structs:"cost" mapstructure:"cost"`
}

func (this Route) Destination() string {
	return this.DestinationF
}

func (this Route) Next() string {
	return this.NextF
}

func (this Route) Cost() *int {
	return this.CostF
}

type OSPF struct {
	RouterIDF               string `json:"router_id" yaml:"router_id" structs:"router_id" mapstructure:"router_id"`
	AreasF                  []Area `json:"areas" yaml:"areas" structs:"areas" mapstructure:"areas"`
	DeadIntervalF           *int   `json:"dead_interval" yaml:"dead_interval" structs:"dead_interval" mapstructure:"dead_interval"`
	HelloIntervalF          *int   `json:"hello_interval" yaml:"hello_interval" structs:"hello_interval" mapstructure:"hello_interval"`
	RetransmissionIntervalF *int   `json:"retransmission_interval" yaml:"retransmission_interval" structs:"retransmission_interval" mapstructure:"retransmission_interval"`
}

func (this OSPF) RouterID() string {
	return this.RouterIDF
}

func (this OSPF) Areas() []ifaces.NodeNetworkOSPFArea {
	areas := make([]ifaces.NodeNetworkOSPFArea, len(this.AreasF))

	for i, a := range this.AreasF {
		areas[i] = a
	}

	return areas
}

func (this OSPF) DeadInterval() *int {
	return this.DeadIntervalF
}

func (this OSPF) HelloInterval() *int {
	return this.HelloIntervalF
}

func (this OSPF) RetransmissionInterval() *int {
	return this.RetransmissionIntervalF
}

type Area struct {
	AreaIDF       *int          `json:"area_id" yaml:"area_id" structs:"area_id" mapstructure:"area_id"`
	AreaNetworksF []AreaNetwork `json:"area_networks" yaml:"area_networks" structs:"area_networks" mapstructure:"area_networks"`
}

func (this Area) AreaID() *int {
	return this.AreaIDF
}

func (this Area) AreaNetworks() []ifaces.NodeNetworkOSPFAreaNetwork {
	nets := make([]ifaces.NodeNetworkOSPFAreaNetwork, len(this.AreaNetworksF))

	for i, n := range this.AreaNetworksF {
		nets[i] = n
	}

	return nets
}

type AreaNetwork struct {
	NetworkF string `json:"network" yaml:"network" structs:"network" mapstructure:"network"`
}

func (this AreaNetwork) Network() string {
	return this.NetworkF
}

type Ruleset struct {
	NameF        string  `json:"name" yaml:"name" structs:"name" mapstructure:"name"`
	DescriptionF string  `json:"description" yaml:"description" structs:"description" mapstructure:"description"`
	DefaultF     string  `json:"default" yaml:"default" structs:"default" mapstructure:"default"`
	RulesF       []*Rule `json:"rules" yaml:"rules" structs:"rules" mapstructure:"rules"`
}

func (this Ruleset) Name() string {
	return this.NameF
}

func (this Ruleset) Description() string {
	return this.DescriptionF
}

func (this Ruleset) Default() string {
	return this.DefaultF
}

func (this Ruleset) Rules() []ifaces.NodeNetworkRulesetRule {
	rules := make([]ifaces.NodeNetworkRulesetRule, len(this.RulesF))

	for i, r := range this.RulesF {
		rules[i] = r
	}

	return rules
}

func (this *Ruleset) UnshiftRule() ifaces.NodeNetworkRulesetRule {
	var min int

	for _, rule := range this.RulesF {
		if min == 0 || rule.IDF < min {
			min = rule.IDF
		}
	}

	if min <= 1 {
		return nil
	}

	r := &Rule{IDF: min - 10}

	if r.IDF < 1 {
		r.IDF = 1
	}

	this.RulesF = append([]*Rule{r}, this.RulesF...)

	return r
}

func (this *Ruleset) RemoveRule(id int) {
	idx := -1

	for i, rule := range this.RulesF {
		if rule.IDF == id {
			idx = i
			break
		}
	}

	if idx != -1 {
		this.RulesF = append(this.RulesF[:idx], this.RulesF[idx+1:]...)
	}
}

type Rule struct {
	IDF          int       `json:"id" yaml:"id" structs:"id" mapstructure:"id"`
	DescriptionF string    `json:"description" yaml:"description" structs:"description" mapstructure:"description"`
	ActionF      string    `json:"action" yaml:"action" structs:"action" mapstructure:"action"`
	ProtocolF    string    `json:"protocol" yaml:"protocol" structs:"protocol" mapstructure:"protocol"`
	SourceF      *AddrPort `json:"source" yaml:"source" structs:"source" mapstructure:"source"`
	DestinationF *AddrPort `json:"destination" yaml:"destination" structs:"destination" mapstructure:"destination"`
}

func (this Rule) ID() int {
	return this.IDF
}

func (this Rule) Description() string {
	return this.DescriptionF
}

func (this Rule) Action() string {
	return this.ActionF
}

func (this Rule) Protocol() string {
	return this.ProtocolF
}

func (this Rule) Source() ifaces.NodeNetworkRulesetRuleAddrPort {
	// fun times... https://glucn.medium.com/golang-an-interface-holding-a-nil-value-is-not-nil-bb151f472cc7
	if this.SourceF == nil {
		return nil
	}

	return this.SourceF
}

func (this Rule) Destination() ifaces.NodeNetworkRulesetRuleAddrPort {
	// fun times... https://glucn.medium.com/golang-an-interface-holding-a-nil-value-is-not-nil-bb151f472cc7
	if this.DestinationF == nil {
		return nil
	}

	return this.DestinationF
}

func (this *Rule) SetDescription(d string) {
	this.DescriptionF = d
}

func (this *Rule) SetAction(a string) {
	this.ActionF = a
}

func (this *Rule) SetProtocol(p string) {
	this.ProtocolF = p
}

func (this *Rule) SetSource(a string, p int) {
	this.SourceF = &AddrPort{AddressF: a, PortF: p}
}

func (this *Rule) SetDestination(a string, p int) {
	this.DestinationF = &AddrPort{AddressF: a, PortF: p}
}

type AddrPort struct {
	AddressF string `json:"address" yaml:"address" structs:"address" mapstructure:"address"`
	PortF    int    `json:"port" yaml:"port" structs:"port" mapstructure:"port"`
}

func (this AddrPort) Address() string {
	return this.AddressF
}

func (this AddrPort) Port() int {
	return this.PortF
}

func (this *Network) SetDefaults() {
	for idx, iface := range this.InterfacesF {
		if iface.BridgeF == "" {
			iface.BridgeF = "phenix"
			this.InterfacesF[idx] = iface
		}
	}
}

func (this Network) InterfaceConfig() string {
	configs := make([]string, len(this.InterfacesF))

	for i, iface := range this.InterfacesF {
		config := []string{iface.BridgeF, iface.VLANF}

		if iface.MACF != "" {
			config = append(config, iface.MACF)
		}

		configs[i] = strings.Join(config, ",")
	}

	return strings.Join(configs, " ")
}

func (this Interface) LinkAddress() string {
	addr := fmt.Sprintf("%s/%d", this.AddressF, this.MaskF)

	_, n, err := net.ParseCIDR(addr)
	if err != nil {
		return addr
	}

	return n.String()
}

func (this Interface) NetworkMask() string {
	addr := fmt.Sprintf("%s/%d", this.AddressF, this.MaskF)

	_, n, err := net.ParseCIDR(addr)
	if err != nil {
		// This should really mess someone up...
		return "0.0.0.0"
	}

	m := n.Mask

	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}
