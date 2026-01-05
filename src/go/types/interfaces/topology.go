package ifaces

import "time"

type TopologySpec interface {
	IncludedTopologies() []string
	Nodes() []NodeSpec
	BootableNodes() []NodeSpec
	SchedulableNodes(string) []NodeSpec

	FindNodeByName(string) NodeSpec
	FindNodesWithLabels(...string) []NodeSpec
	FindDelayedNodes() []NodeSpec
	FindNodesWithVLAN(string) []NodeSpec

	AddNode(string, string) NodeSpec
	RemoveNode(string)

	HasCommands() bool

	// accepts name of default bridge
	Init(string) error
}

type NodeSpec interface {
	Annotations() map[string]interface{}
	Labels() map[string]string
	Type() string
	General() NodeGeneral
	Hardware() NodeHardware
	Network() NodeNetwork
	Injections() []NodeInjection
	Deletions() []NodeDeletion
	Delay() NodeDelay
	Advanced() map[string]string
	Overrides() map[string]string
	Commands() []string
	External() bool

	SetInjections([]NodeInjection)
	SetDeletions([]NodeDeletion)
	SetType(string)
	SetLabels(map[string]string)

	AddAnnotation(string, interface{})
	AddTimerDelay(delay string)
	AddUserDelay(delay bool)
	AddC2Delay(hostname string, useuuid bool)
	AddLabel(string, string)
	AddHardware(string, int, int) NodeHardware
	AddNetworkInterface(string, string, string) NodeNetworkInterface
	AddNetworkRoute(string, string, int)
	// Add NAT section to Node Network
	//
	// Example code:
	//
	//   newNat := map[string][]string{
	//   	"eth0": {"eth1"},
	//   }
	//   nats := []map[string][]string{}
	//   nats = append(nats, newNat)
	//   node.AddNetworkNAT(nats)
	//
	// Example result:
	//
	//   nat:
	//     - in:
	//       - eth1
	//       out: eth0
	AddNetworkNAT(nats []map[string][]string)
	// Add OSPF section to Node Network
	//
	// Example code:
	//
	//   area := map[int][]string{
	//   	0: {"75.75.0.0/16"},
	//   }
	//   node.AddNetworkOSPF("75.75.75.5", 60, 10, 5, area)
	//
	// Example result:
	//
	//   ospf:
	//     router_id: 75.75.75.5
	//     dead_interval: 60
	//     hello_interval: 10
	//     retransmission_interval: 5
	//     areas:
	//       - area_id: 0
	//         area_networks:
	//       	 - network: 75.75.0.0/16
	AddNetworkOSPF(routerID string, dead, hello, retrans int, areas map[int][]string)
	AddInject(string, string, string, string)
	AddDeletion(string, string)

	SetAdvanced(map[string]string)
	AddAdvanced(string, string)
	AddOverride(string, string)
	AddCommand(string)

	GetAnnotation(string) (interface{}, bool)
	Delayed() string
}

type NodeGeneral interface {
	Hostname() string
	Description() string
	VMType() string
	Snapshot() *bool
	SetSnapshot(bool)
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

	SetInjectPartition(*int)
	SetImage(string)
}

type NodeNetwork interface {
	Interfaces() []NodeNetworkInterface
	Routes() []NodeNetworkRoute
	OSPF() NodeNetworkOSPF
	Rulesets() []NodeNetworkRuleset
	NAT() []NodeNetworkNAT

	SetRulesets([]NodeNetworkRuleset)
	AddRuleset(NodeNetworkRuleset)

	InterfaceAddress(string) string
	InterfaceVLAN(string) string
	InterfaceMask(string) int
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
	Driver() string
	MTU() int
	Address() string
	Mask() int
	Gateway() string
	DNS() []string
	QinQ() bool
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
	SetDNS([]string)
	SetQinQ(bool)
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
	Stateful() bool

	SetDescription(string)
	SetAction(string)
	SetProtocol(string)
	SetSource(string, int)
	SetDestination(string, int)
	SetStateful(bool)
}

type NodeNetworkRulesetRuleAddrPort interface {
	Address() string
	Port() int
}

type NodeNetworkNAT interface {
	In() []string
	Out() string
}

type NodeInjection interface {
	Src() string
	Dst() string
	Description() string
	Permissions() string
}

type NodeDeletion interface {
	Path() string
	Description() string
}

type NodeDelay interface {
	Timer() time.Duration
	User() bool
	C2() []NodeC2Delay
}

type NodeC2Delay interface {
	Hostname() string
	UseUUID() bool
}
