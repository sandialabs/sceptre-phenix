package v0

import (
	"fmt"
	"net"
	"strings"

	ifaces "phenix/types/interfaces"
)

const ruleIDDecrement = 10

type Network struct {
	InterfacesF []*Interface `json:"interfaces" mapstructure:"interfaces" structs:"interfaces" yaml:"interfaces"`
	RoutesF     []Route      `json:"routes"     mapstructure:"routes"     structs:"routes"     yaml:"routes"`
	OSPFF       *OSPF        `json:"ospf"       mapstructure:"ospf"       structs:"ospf"       yaml:"ospf"`
	RulesetsF   []*Ruleset   `json:"rulesets"   mapstructure:"rulesets"   structs:"rulesets"   yaml:"rulesets"`
}

func (n Network) Interfaces() []ifaces.NodeNetworkInterface {
	interfaces := make([]ifaces.NodeNetworkInterface, len(n.InterfacesF))

	for i, iface := range n.InterfacesF {
		interfaces[i] = iface
	}

	return interfaces
}

func (n Network) Routes() []ifaces.NodeNetworkRoute {
	routes := make([]ifaces.NodeNetworkRoute, len(n.RoutesF))

	for i, r := range n.RoutesF {
		routes[i] = r
	}

	return routes
}

func (n Network) OSPF() ifaces.NodeNetworkOSPF { //nolint:ireturn // interface
	return n.OSPFF
}

func (n Network) Rulesets() []ifaces.NodeNetworkRuleset {
	sets := make([]ifaces.NodeNetworkRuleset, len(n.RulesetsF))

	for i, r := range n.RulesetsF {
		sets[i] = r
	}

	return sets
}

func (Network) NAT() []ifaces.NodeNetworkNAT {
	return nil
}

func (n *Network) SetRulesets(rules []ifaces.NodeNetworkRuleset) {
	sets := make([]*Ruleset, len(rules))

	for i, r := range rules {
		s, _ := r.(*Ruleset)
		sets[i] = s
	}

	n.RulesetsF = sets
}

func (n *Network) AddRuleset(rule ifaces.NodeNetworkRuleset) {
	r, _ := rule.(*Ruleset)
	n.RulesetsF = append(n.RulesetsF, r)
}

func (n *Network) InterfaceAddress(name string) string {
	for _, iface := range n.InterfacesF {
		if strings.EqualFold(iface.NameF, name) {
			return iface.AddressF
		}
	}

	return ""
}

func (n *Network) InterfaceVLAN(vlan string) string {
	for _, iface := range n.InterfacesF {
		if iface.VLAN() == vlan {
			return iface.NameF
		}
	}

	return ""
}

func (n *Network) InterfaceMask(name string) int {
	for _, iface := range n.InterfacesF {
		if strings.EqualFold(iface.NameF, name) {
			return iface.MaskF
		}
	}

	return 0
}

type Interface struct {
	NameF       string   `json:"name"        mapstructure:"name"        structs:"name"        yaml:"name"`
	TypeF       string   `json:"type"        mapstructure:"type"        structs:"type"        yaml:"type"`
	ProtoF      string   `json:"proto"       mapstructure:"proto"       structs:"proto"       yaml:"proto"`
	UDPPortF    int      `json:"udp_port"    mapstructure:"udp_port"    structs:"udp_port"    yaml:"udp_port"`
	BaudRateF   int      `json:"baud_rate"   mapstructure:"baud_rate"   structs:"baud_rate"   yaml:"baud_rate"`
	DeviceF     string   `json:"device"      mapstructure:"device"      structs:"device"      yaml:"device"`
	VLANF       string   `json:"vlan"        mapstructure:"vlan"        structs:"vlan"        yaml:"vlan"`
	BridgeF     string   `json:"bridge"      mapstructure:"bridge"      structs:"bridge"      yaml:"bridge"`
	AutostartF  bool     `json:"autostart"   mapstructure:"autostart"   structs:"autostart"   yaml:"autostart"`
	MACF        string   `json:"mac"         mapstructure:"mac"         structs:"mac"         yaml:"mac"`
	DriverF     string   `json:"driver"      mapstructure:"driver"      structs:"driver"      yaml:"driver"`
	MTUF        int      `json:"mtu"         mapstructure:"mtu"         structs:"mtu"         yaml:"mtu"`
	AddressF    string   `json:"address"     mapstructure:"address"     structs:"address"     yaml:"address"`
	MaskF       int      `json:"mask"        mapstructure:"mask"        structs:"mask"        yaml:"mask"`
	GatewayF    string   `json:"gateway"     mapstructure:"gateway"     structs:"gateway"     yaml:"gateway"`
	DNSF        []string `json:"dns"         mapstructure:"dns"         structs:"dns"         yaml:"dns"`
	QinQF       bool     `json:"qinq"        mapstructure:"qinq"        structs:"qinq"        yaml:"qinq"`
	RulesetInF  string   `json:"ruleset_in"  mapstructure:"ruleset_in"  structs:"ruleset_in"  yaml:"ruleset_in"`
	RulesetOutF string   `json:"ruleset_out" mapstructure:"ruleset_out" structs:"ruleset_out" yaml:"ruleset_out"`
}

func (i Interface) Name() string {
	return i.NameF
}

func (i Interface) Type() string {
	return i.TypeF
}

func (i Interface) Proto() string {
	return i.ProtoF
}

func (i Interface) UDPPort() int {
	return i.UDPPortF
}

func (i Interface) BaudRate() int {
	return i.BaudRateF
}

func (i Interface) Device() string {
	return i.DeviceF
}

func (i Interface) VLAN() string {
	return i.VLANF
}

func (i Interface) Bridge() string {
	return i.BridgeF
}

func (i Interface) Autostart() bool {
	return i.AutostartF
}

func (i Interface) MAC() string {
	return i.MACF
}

func (i Interface) Driver() string {
	return i.DriverF
}

func (i Interface) MTU() int {
	return i.MTUF
}

func (i Interface) Address() string {
	return i.AddressF
}

func (i Interface) Mask() int {
	return i.MaskF
}

func (i Interface) Gateway() string {
	return i.GatewayF
}

func (i Interface) DNS() []string {
	return i.DNSF
}

func (i Interface) QinQ() bool {
	return i.QinQF
}

func (i Interface) RulesetIn() string {
	return i.RulesetInF
}

func (i Interface) RulesetOut() string {
	return i.RulesetOutF
}

func (i *Interface) SetName(name string) {
	i.NameF = name
}

func (i *Interface) SetType(typ string) {
	i.TypeF = typ
}

func (i *Interface) SetProto(proto string) {
	i.ProtoF = proto
}

func (i *Interface) SetUDPPort(port int) {
	i.UDPPortF = port
}

func (i *Interface) SetBaudRate(rate int) {
	i.BaudRateF = rate
}

func (i *Interface) SetDevice(dev string) {
	i.DeviceF = dev
}

func (i *Interface) SetVLAN(vlan string) {
	i.VLANF = vlan
}

func (i *Interface) SetBridge(br string) {
	i.BridgeF = br
}

func (i *Interface) SetAutostart(auto bool) {
	i.AutostartF = auto
}

func (i *Interface) SetMAC(mac string) {
	i.MACF = mac
}

func (i *Interface) SetMTU(mtu int) {
	i.MTUF = mtu
}

func (i *Interface) SetAddress(addr string) {
	i.AddressF = addr
}

func (i *Interface) SetMask(mask int) {
	i.MaskF = mask
}

func (i *Interface) SetGateway(gw string) {
	i.GatewayF = gw
}

func (i *Interface) SetDNS(dns []string) {
	i.DNSF = dns
}

func (i *Interface) SetQinQ(q bool) {
	i.QinQF = q
}

func (i *Interface) SetRulesetIn(rule string) {
	i.RulesetInF = rule
}

func (i *Interface) SetRulesetOut(rule string) {
	i.RulesetOutF = rule
}

type Route struct {
	DestinationF string `json:"destination" mapstructure:"destination" structs:"destination" yaml:"destination"`
	NextF        string `json:"next"        mapstructure:"next"        structs:"next"        yaml:"next"`
	CostF        *int   `json:"cost"        mapstructure:"cost"        structs:"cost"        yaml:"cost"`
}

func (r Route) Destination() string {
	return r.DestinationF
}

func (r Route) Next() string {
	return r.NextF
}

func (r Route) Cost() *int {
	return r.CostF
}

type OSPF struct {
	RouterIDF               string `json:"router_id"               mapstructure:"router_id"               structs:"router_id"               yaml:"router_id"`
	AreasF                  []Area `json:"areas"                   mapstructure:"areas"                   structs:"areas"                   yaml:"areas"`
	DeadIntervalF           *int   `json:"dead_interval"           mapstructure:"dead_interval"           structs:"dead_interval"           yaml:"dead_interval"`
	HelloIntervalF          *int   `json:"hello_interval"          mapstructure:"hello_interval"          structs:"hello_interval"          yaml:"hello_interval"`
	RetransmissionIntervalF *int   `json:"retransmission_interval" mapstructure:"retransmission_interval" structs:"retransmission_interval" yaml:"retransmission_interval"`
}

func (o OSPF) RouterID() string {
	return o.RouterIDF
}

func (o OSPF) Areas() []ifaces.NodeNetworkOSPFArea {
	areas := make([]ifaces.NodeNetworkOSPFArea, len(o.AreasF))

	for i, a := range o.AreasF {
		areas[i] = a
	}

	return areas
}

func (o OSPF) DeadInterval() *int {
	return o.DeadIntervalF
}

func (o OSPF) HelloInterval() *int {
	return o.HelloIntervalF
}

func (o OSPF) RetransmissionInterval() *int {
	return o.RetransmissionIntervalF
}

type Area struct {
	AreaIDF       *int          `json:"area_id"       mapstructure:"area_id"       structs:"area_id"       yaml:"area_id"`
	AreaNetworksF []AreaNetwork `json:"area_networks" mapstructure:"area_networks" structs:"area_networks" yaml:"area_networks"`
}

func (a Area) AreaID() *int {
	return a.AreaIDF
}

func (a Area) AreaNetworks() []ifaces.NodeNetworkOSPFAreaNetwork {
	nets := make([]ifaces.NodeNetworkOSPFAreaNetwork, len(a.AreaNetworksF))

	for i, n := range a.AreaNetworksF {
		nets[i] = n
	}

	return nets
}

type AreaNetwork struct {
	NetworkF string `json:"network" mapstructure:"network" structs:"network" yaml:"network"`
}

func (an AreaNetwork) Network() string {
	return an.NetworkF
}

type Ruleset struct {
	NameF        string  `json:"name"        mapstructure:"name"        structs:"name"        yaml:"name"`
	DescriptionF string  `json:"description" mapstructure:"description" structs:"description" yaml:"description"`
	DefaultF     string  `json:"default"     mapstructure:"default"     structs:"default"     yaml:"default"`
	RulesF       []*Rule `json:"rules"       mapstructure:"rules"       structs:"rules"       yaml:"rules"`
}

func (rs Ruleset) Name() string {
	return rs.NameF
}

func (rs Ruleset) Description() string {
	return rs.DescriptionF
}

func (rs Ruleset) Default() string {
	return rs.DefaultF
}

func (rs Ruleset) Rules() []ifaces.NodeNetworkRulesetRule {
	rules := make([]ifaces.NodeNetworkRulesetRule, len(rs.RulesF))

	for i, r := range rs.RulesF {
		rules[i] = r
	}

	return rules
}

func (rs *Ruleset) UnshiftRule() ifaces.NodeNetworkRulesetRule { //nolint:ireturn // interface
	minVal := -1

	for _, rule := range rs.RulesF {
		if minVal == -1 || rule.IDF < minVal {
			minVal = rule.IDF
		}
	}

	if minVal == 0 {
		return nil
	}

	r := &Rule{IDF: minVal - ruleIDDecrement} //nolint:exhaustruct // partial initialization

	if r.IDF < 1 {
		r.IDF = 1
	}

	rs.RulesF = append([]*Rule{r}, rs.RulesF...)

	return r
}

func (rs *Ruleset) RemoveRule(id int) {
	idx := -1

	for i, rule := range rs.RulesF {
		if rule.IDF == id {
			idx = i

			break
		}
	}

	if idx != -1 {
		rs.RulesF = append(rs.RulesF[:idx], rs.RulesF[idx+1:]...)
	}
}

type Rule struct {
	IDF          int       `json:"id"          mapstructure:"id"          structs:"id"          yaml:"id"`
	DescriptionF string    `json:"description" mapstructure:"description" structs:"description" yaml:"description"`
	ActionF      string    `json:"action"      mapstructure:"action"      structs:"action"      yaml:"action"`
	ProtocolF    string    `json:"protocol"    mapstructure:"protocol"    structs:"protocol"    yaml:"protocol"`
	SourceF      *AddrPort `json:"source"      mapstructure:"source"      structs:"source"      yaml:"source"`
	DestinationF *AddrPort `json:"destination" mapstructure:"destination" structs:"destination" yaml:"destination"`
}

func (r Rule) ID() int {
	return r.IDF
}

func (r Rule) Description() string {
	return r.DescriptionF
}

func (r Rule) Action() string {
	return r.ActionF
}

func (r Rule) Protocol() string {
	return r.ProtocolF
}

func (r Rule) Source() ifaces.NodeNetworkRulesetRuleAddrPort { //nolint:ireturn // interface
	return r.SourceF
}

func (r Rule) Destination() ifaces.NodeNetworkRulesetRuleAddrPort { //nolint:ireturn // interface
	return r.DestinationF
}

func (Rule) Stateful() bool {
	return false
}

func (r *Rule) SetDescription(d string) {
	r.DescriptionF = d
}

func (r *Rule) SetAction(a string) {
	r.ActionF = a
}

func (r *Rule) SetProtocol(p string) {
	r.ProtocolF = p
}

func (r *Rule) SetSource(a string, p int) {
	r.SourceF = &AddrPort{AddressF: a, PortF: p}
}

func (r *Rule) SetDestination(a string, p int) {
	r.DestinationF = &AddrPort{AddressF: a, PortF: p}
}

func (Rule) SetStateful(bool) {}

type AddrPort struct {
	AddressF string `json:"address" mapstructure:"address" structs:"address" yaml:"address"`
	PortF    int    `json:"port"    mapstructure:"port"    structs:"port"    yaml:"port"`
}

func (ap AddrPort) Address() string {
	return ap.AddressF
}

func (ap AddrPort) Port() int {
	return ap.PortF
}

func (n *Network) SetDefaults() {
	for idx, iface := range n.InterfacesF {
		if iface.BridgeF == "" {
			iface.BridgeF = "phenix"
			n.InterfacesF[idx] = iface
		}
	}
}

func (n Network) InterfaceConfig() string {
	configs := make([]string, len(n.InterfacesF))

	for i, iface := range n.InterfacesF {
		config := []string{iface.BridgeF, iface.VLANF}

		if iface.MACF != "" {
			config = append(config, iface.MACF)
		}

		if iface.DriverF != "" {
			config = append(config, iface.DriverF)
		}

		if iface.QinQF {
			config = append(config, "qinq")
		}

		configs[i] = strings.Join(config, ",")
	}

	return strings.Join(configs, " ")
}

func (i Interface) LinkAddress() string {
	addr := fmt.Sprintf("%s/%d", i.AddressF, i.MaskF)

	_, n, err := net.ParseCIDR(addr)
	if err != nil {
		return addr
	}

	return n.String()
}

func (i Interface) NetworkMask() string {
	addr := fmt.Sprintf("%s/%d", i.AddressF, i.MaskF)

	_, n, err := net.ParseCIDR(addr)
	if err != nil {
		// This should really mess someone up...
		return "0.0.0.0"
	}

	m := n.Mask

	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}
