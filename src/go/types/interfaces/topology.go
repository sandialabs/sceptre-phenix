package ifaces

type TopologySpec interface {
	Nodes() []NodeSpec

	FindNodeByName(string) NodeSpec
	FindNodesWithLabels(...string) []NodeSpec

	AddNode(string, string) NodeSpec
	RemoveNode(string)

	Init() error
}

type NodeSpec interface {
	Annotations() map[string]interface{}
	Labels() map[string]string
	Type() string
	General() NodeGeneral
	Hardware() NodeHardware
	Network() NodeNetwork
	Injections() []NodeInjection
	Advanced() map[string]string
	Overrides() map[string]string

	SetInjections([]NodeInjection)

	AddLabel(string, string)
	AddHardware(string, int, int) NodeHardware
	AddNetworkInterface(string, string, string) NodeNetworkInterface
	AddNetworkRoute(string, string, int)
	AddInject(string, string, string, string)

	SetAdvanced(map[string]string)
	AddAdvanced(string, string)
	AddOverride(string, string)

	GetAnnotation(string) (interface{}, bool)
}

type NodeGeneral interface {
	Hostname() string
	Description() string
	VMType() string
	Snapshot() *bool
	DoNotBoot() *bool

	SetDoNotBoot(bool)
}

type NodeHardware interface {
	CPU() string
	VCPU() int
	Memory() int
	OSType() string
	Drives() []NodeDrive

	SetVCPU(int)
	SetMemory(int)

	AddDrive(string, int) NodeDrive
}

type NodeDrive interface {
	Image() string
	Interface() string
	CacheMode() string
	InjectPartition() *int

	SetImage(string)
}

type NodeNetwork interface {
	Interfaces() []NodeNetworkInterface
	Routes() []NodeNetworkRoute
	OSPF() NodeNetworkOSPF
	Rulesets() []NodeNetworkRuleset

	SetRulesets([]NodeNetworkRuleset)
	AddRuleset(NodeNetworkRuleset)
}

type NodeNetworkInterface interface {
	Name() string
	Type() string
	Proto() string
	UDPPort() int
	BaudRate() int
	Device() string
	VLAN() string
	Bridge() string
	Autostart() bool
	MAC() string
	MTU() int
	Address() string
	Mask() int
	Gateway() string
	RulesetIn() string
	RulesetOut() string

	SetName(string)
	SetType(string)
	SetProto(string)
	SetUDPPort(int)
	SetBaudRate(int)
	SetDevice(string)
	SetVLAN(string)
	SetBridge(string)
	SetAutostart(bool)
	SetMAC(string)
	SetMTU(int)
	SetAddress(string)
	SetMask(int)
	SetGateway(string)
	SetRulesetIn(string)
	SetRulesetOut(string)
}

type NodeNetworkRoute interface {
	Destination() string
	Next() string
	Cost() *int
}

type NodeNetworkOSPF interface {
	RouterID() string
	Areas() []NodeNetworkOSPFArea
	DeadInterval() *int
	HelloInterval() *int
	RetransmissionInterval() *int
}

type NodeNetworkOSPFArea interface {
	AreaID() *int
	AreaNetworks() []NodeNetworkOSPFAreaNetwork
}

type NodeNetworkOSPFAreaNetwork interface {
	Network() string
}

type NodeNetworkRuleset interface {
	Name() string
	Description() string
	Default() string
	Rules() []NodeNetworkRulesetRule

	UnshiftRule() NodeNetworkRulesetRule
	RemoveRule(int)
}

type NodeNetworkRulesetRule interface {
	ID() int
	Description() string
	Action() string
	Protocol() string
	Source() NodeNetworkRulesetRuleAddrPort
	Destination() NodeNetworkRulesetRuleAddrPort

	SetDescription(string)
	SetAction(string)
	SetProtocol(string)
	SetSource(string, int)
	SetDestination(string, int)
}

type NodeNetworkRulesetRuleAddrPort interface {
	Address() string
	Port() int
}

type NodeInjection interface {
	Src() string
	Dst() string
	Description() string
	Permissions() string
}
