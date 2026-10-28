package v1

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	ifaces "phenix/types/interfaces"
)

func TestNetworkNilReceiver(t *testing.T) {
	t.Parallel()

	var n *Network

	if n.Interfaces() != nil || n.Routes() != nil || n.OSPF() != nil || n.Rulesets() != nil || n.NAT() != nil {
		t.Error("accessors of a nil *Network return values, want nil")
	}

	if (&Network{}).OSPF() != nil {
		t.Error("OSPF() of a Network without OSPF is not nil")
	}
}

func TestNetworkAccessors(t *testing.T) {
	t.Parallel()

	cost := 5
	area := 0
	dead, hello, retrans := 40, 10, 5

	n := &Network{
		InterfacesF: []*Interface{
			{NameF: "eth0", VLANF: "EXP", AddressF: "10.0.0.1", MaskF: 24},
			{NameF: "MGMT", VLANF: "MGMT", AddressF: "172.16.0.1", MaskF: 16},
		},
		RoutesF: []Route{{DestinationF: "0.0.0.0/0", NextF: "10.0.0.254", CostF: &cost}},
		OSPFF: &OSPF{
			RouterIDF:               "1.1.1.1",
			DeadIntervalF:           &dead,
			HelloIntervalF:          &hello,
			RetransmissionIntervalF: &retrans,
			AreasF:                  []Area{{AreaIDF: &area, AreaNetworksF: []AreaNetwork{{NetworkF: "10.0.0.0/24"}}}},
		},
		RulesetsF: []*Ruleset{{NameF: "rs", DescriptionF: "d", DefaultF: "drop"}},
		NATF:      []NAT{{InF: []string{"eth0"}, OutF: "MGMT"}},
	}

	if got := len(n.Interfaces()); got != 2 {
		t.Errorf("len(Interfaces()) = %d, want 2", got)
	}

	route := n.Routes()[0]
	if route.Destination() != "0.0.0.0/0" || route.Next() != "10.0.0.254" || route.Cost() == nil || *route.Cost() != 5 {
		t.Errorf("Routes()[0] = %+v, want the route that was set", route)
	}

	ospf := n.OSPF()
	if ospf.RouterID() != "1.1.1.1" || *ospf.DeadInterval() != 40 || *ospf.HelloInterval() != 10 || *ospf.RetransmissionInterval() != 5 {
		t.Errorf("OSPF() = %+v, want the OSPF config that was set", ospf)
	}

	gotArea := ospf.Areas()[0]
	if *gotArea.AreaID() != 0 || gotArea.AreaNetworks()[0].Network() != "10.0.0.0/24" {
		t.Errorf("OSPF().Areas()[0] = %+v, want area 0 with 10.0.0.0/24", gotArea)
	}

	rs := n.Rulesets()[0]
	if rs.Name() != "rs" || rs.Description() != "d" || rs.Default() != "drop" || len(rs.Rules()) != 0 {
		t.Errorf("Rulesets()[0] = %+v, want the ruleset that was set", rs)
	}

	nat := n.NAT()[0]
	if diff := cmp.Diff([]string{"eth0"}, nat.In()); diff != "" || nat.Out() != "MGMT" {
		t.Errorf("NAT()[0] = in %v out %q, want in [eth0] out MGMT", nat.In(), nat.Out())
	}

	if got := n.InterfaceAddress("mgmt"); got != "172.16.0.1" {
		t.Errorf("InterfaceAddress(mgmt) = %q, want 172.16.0.1", got)
	}

	if got := n.InterfaceAddress("eth9"); got != "" {
		t.Errorf("InterfaceAddress(eth9) = %q, want empty", got)
	}

	if got := n.InterfaceVLAN("EXP"); got != "eth0" {
		t.Errorf("InterfaceVLAN(EXP) = %q, want eth0", got)
	}

	if got := n.InterfaceVLAN("nope"); got != "" {
		t.Errorf("InterfaceVLAN(nope) = %q, want empty", got)
	}

	if got := n.InterfaceMask("ETH0"); got != 24 {
		t.Errorf("InterfaceMask(ETH0) = %d, want 24", got)
	}

	if got := n.InterfaceMask("eth9"); got != 0 {
		t.Errorf("InterfaceMask(eth9) = %d, want 0", got)
	}

	n.AddRuleset(&Ruleset{NameF: "added"})
	n.SetRulesets(append(n.Rulesets(), ifaces.NodeNetworkRuleset(&Ruleset{NameF: "set"})))

	names := make([]string, 0, len(n.RulesetsF))
	for _, r := range n.RulesetsF {
		names = append(names, r.NameF)
	}

	if diff := cmp.Diff([]string{"rs", "added", "set"}, names); diff != "" {
		t.Errorf("rulesets after AddRuleset and SetRulesets (-want +got):\n%s", diff)
	}
}

func TestInterfaceAccessors(t *testing.T) {
	t.Parallel()

	var i Interface

	i.SetName("eth0")
	i.SetType("ethernet")
	i.SetProto("static")
	i.SetUDPPort(8000)
	i.SetBaudRate(9600)
	i.SetDevice("/dev/ttyS0")
	i.SetVLAN("EXP")
	i.SetBridge("br0")
	i.SetAutostart(true)
	i.SetMAC("00:11:22:33:44:55")
	i.SetMTU(1500)
	i.DriverF = "e1000"
	i.SetAddress("10.0.0.1")
	i.SetMask(24)
	i.SetGateway("10.0.0.254")
	i.SetDNS([]string{"8.8.8.8"})
	i.SetQinQ(true)
	i.SetRulesetIn("in")
	i.SetRulesetOut("out")

	want := Interface{
		NameF: "eth0", TypeF: "ethernet", ProtoF: "static", UDPPortF: 8000, BaudRateF: 9600,
		DeviceF: "/dev/ttyS0", VLANF: "EXP", BridgeF: "br0", AutostartF: true, MACF: "00:11:22:33:44:55",
		DriverF: "e1000", MTUF: 1500, AddressF: "10.0.0.1", MaskF: 24, GatewayF: "10.0.0.254", DNSF: []string{"8.8.8.8"},
		QinQF: true, RulesetInF: "in", RulesetOutF: "out",
	}

	if diff := cmp.Diff(want, i); diff != "" {
		t.Errorf("Interface after setters (-want +got):\n%s", diff)
	}

	got := Interface{
		NameF: i.Name(), TypeF: i.Type(), ProtoF: i.Proto(), UDPPortF: i.UDPPort(), BaudRateF: i.BaudRate(),
		DeviceF: i.Device(), VLANF: i.VLAN(), BridgeF: i.Bridge(), AutostartF: i.Autostart(), MACF: i.MAC(),
		DriverF: i.Driver(), MTUF: i.MTU(), AddressF: i.Address(), MaskF: i.Mask(), GatewayF: i.Gateway(), DNSF: i.DNS(),
		QinQF: i.QinQ(), RulesetInF: i.RulesetIn(), RulesetOutF: i.RulesetOut(),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Interface getters (-want +got):\n%s", diff)
	}
}

func TestInterfaceAddressing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr     string
		mask     int
		wantLink string
		wantMask string
	}{
		{addr: "10.1.2.3", mask: 24, wantLink: "10.1.2.0/24", wantMask: "255.255.255.0"},
		{addr: "192.168.0.130", mask: 25, wantLink: "192.168.0.128/25", wantMask: "255.255.255.128"},
		{addr: "172.16.5.4", mask: 0, wantLink: "0.0.0.0/0", wantMask: "0.0.0.0"},
		{addr: "not-an-ip", mask: 24, wantLink: "not-an-ip/24", wantMask: "0.0.0.0"},
	}

	for _, tt := range tests {
		i := Interface{AddressF: tt.addr, MaskF: tt.mask}

		if got := i.LinkAddress(); got != tt.wantLink {
			t.Errorf("Interface{%s/%d}.LinkAddress() = %q, want %q", tt.addr, tt.mask, got, tt.wantLink)
		}

		if got := i.NetworkMask(); got != tt.wantMask {
			t.Errorf("Interface{%s/%d}.NetworkMask() = %q, want %q", tt.addr, tt.mask, got, tt.wantMask)
		}
	}
}

func TestNetworkSetDefaults(t *testing.T) {
	t.Parallel()

	setInTopo := true
	notSetInTopo := false

	n := &Network{InterfacesF: []*Interface{
		{NameF: "same", BridgeF: "br0"},
		{NameF: "empty"},
		{NameF: "default", BridgeF: testBridge},
		{NameF: "custom", BridgeF: "custom"},
		{NameF: "marked custom", BridgeF: "other", BridgeSetInTopo: &setInTopo},
		{NameF: "marked default", BridgeF: "old", BridgeSetInTopo: &notSetInTopo},
	}}

	n.SetDefaults("br0")

	var got []string
	for _, i := range n.InterfacesF {
		got = append(got, i.BridgeF)
	}

	if diff := cmp.Diff([]string{"br0", "br0", "br0", "custom", "other", "br0"}, got); diff != "" {
		t.Errorf("bridges after SetDefaults(br0) (-want +got):\n%s", diff)
	}

	if n.InterfacesF[0].BridgeSetInTopo != nil {
		t.Error("SetDefaults marked an interface already on the default bridge")
	}

	if *n.InterfacesF[3].BridgeSetInTopo != true || *n.InterfacesF[1].BridgeSetInTopo != false {
		t.Error("SetDefaults did not record whether the topology set the bridge")
	}

	// A second call keeps a bridge the topology set, even for a new default.
	n.SetDefaults("br1")

	if got := n.InterfacesF[3].BridgeF; got != "custom" {
		t.Errorf("bridge set in topology after SetDefaults(br1) = %q, want custom", got)
	}

	if got := n.InterfacesF[1].BridgeF; got != "br1" {
		t.Errorf("default bridge after SetDefaults(br1) = %q, want br1", got)
	}
}

func TestNetworkInterfaceConfig(t *testing.T) {
	t.Parallel()

	n := Network{InterfacesF: []*Interface{
		{BridgeF: testBridge, VLANF: "EXP"},
		{BridgeF: "br0", VLANF: "MGMT", MACF: "00:11:22:33:44:55", DriverF: "e1000", QinQF: true},
		{BridgeF: "br0", VLANF: "X", DriverF: "virtio"},
	}}

	want := "phenix,EXP br0,MGMT,00:11:22:33:44:55,e1000,qinq br0,X,virtio"
	if got := n.InterfaceConfig(); got != want {
		t.Errorf("InterfaceConfig() = %q, want %q", got, want)
	}

	if got := (Network{}).InterfaceConfig(); got != "" {
		t.Errorf("InterfaceConfig() with no interfaces = %q, want empty", got)
	}
}

func ruleIDs(rs *Ruleset) []int {
	ids := make([]int, len(rs.RulesF))
	for i, r := range rs.RulesF {
		ids[i] = r.IDF
	}

	return ids
}

func TestRulesetUnshiftRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ids     []int
		wantNew int
		wantIDs []int
	}{
		{name: "no rules", ids: nil, wantIDs: []int{}},
		{name: "lowest ID is 1", ids: []int{1, 20}, wantIDs: []int{1, 20}},
		{name: "room for a full step", ids: []int{30, 20}, wantNew: 10, wantIDs: []int{10, 30, 20}},
		{name: "less than a step", ids: []int{5}, wantNew: 1, wantIDs: []int{1, 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rs := &Ruleset{}
			for _, id := range tt.ids {
				rs.RulesF = append(rs.RulesF, &Rule{IDF: id})
			}

			got := rs.UnshiftRule()

			if tt.wantNew == 0 {
				if got != nil {
					t.Errorf("UnshiftRule() = %+v, want nil", got)
				}
			} else if got == nil || got.ID() != tt.wantNew {
				t.Errorf("UnshiftRule() = %+v, want a rule with ID %d", got, tt.wantNew)
			}

			if diff := cmp.Diff(tt.wantIDs, ruleIDs(rs)); diff != "" {
				t.Errorf("rule IDs after UnshiftRule() (-want +got):\n%s", diff)
			}
		})
	}
}

// UnshiftRule adds a rule with an ID lower than all others, or adds nothing
// when the lowest ID is 1.
func TestRulesetUnshiftRuleProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		ids := rapid.SliceOfN(rapid.IntRange(1, 1000), 1, 8).Draw(t, "ids")

		rs := &Ruleset{}
		for _, id := range ids {
			rs.RulesF = append(rs.RulesF, &Rule{IDF: id})
		}

		lowest := ids[0]
		for _, id := range ids {
			lowest = min(lowest, id)
		}

		got := rs.UnshiftRule()

		if lowest == 1 {
			if got != nil || len(rs.RulesF) != len(ids) {
				t.Fatalf("UnshiftRule() with lowest ID 1 = %+v, want nil and no new rule", got)
			}

			return
		}

		if got == nil || got.ID() < 1 || got.ID() >= lowest || rs.RulesF[0].IDF != got.ID() {
			t.Fatalf("UnshiftRule() with lowest ID %d = %+v, want a first rule with an ID in [1, %d)", lowest, got, lowest)
		}
	})
}

func TestRulesetRemoveRule(t *testing.T) {
	t.Parallel()

	rs := &Ruleset{RulesF: []*Rule{{IDF: 10}, {IDF: 20}, {IDF: 30}}}

	rs.RemoveRule(20)
	rs.RemoveRule(99)

	if diff := cmp.Diff([]int{10, 30}, ruleIDs(rs)); diff != "" {
		t.Errorf("rule IDs after RemoveRule(20) and RemoveRule(99) (-want +got):\n%s", diff)
	}
}

func TestRuleAccessors(t *testing.T) {
	t.Parallel()

	r := &Rule{IDF: 10}

	if r.Source() != nil || r.Destination() != nil {
		t.Error("Source() or Destination() of a rule without them is not nil")
	}

	r.SetDescription("allow web")
	r.SetAction("accept")
	r.SetProtocol("tcp")
	r.SetSource("10.0.0.0/24", 0)
	r.SetDestination("10.1.0.5", 443)
	r.SetStateful(true)

	want := &Rule{
		IDF: 10, DescriptionF: "allow web", ActionF: "accept", ProtocolF: "tcp",
		SourceF:      &AddrPort{AddressF: "10.0.0.0/24"},
		DestinationF: &AddrPort{AddressF: "10.1.0.5", PortF: 443},
		StatefulF:    true,
	}

	if diff := cmp.Diff(want, r); diff != "" {
		t.Errorf("Rule after setters (-want +got):\n%s", diff)
	}

	if r.ID() != 10 || r.Description() != "allow web" || r.Action() != "accept" || r.Protocol() != "tcp" || !r.Stateful() {
		t.Errorf("Rule getters = %d %q %q %q %t", r.ID(), r.Description(), r.Action(), r.Protocol(), r.Stateful())
	}

	if r.Source().Address() != "10.0.0.0/24" || r.Destination().Address() != "10.1.0.5" || r.Destination().Port() != 443 {
		t.Errorf("Source(), Destination() = %+v, %+v", r.Source(), r.Destination())
	}

	rs := Ruleset{RulesF: []*Rule{r}}
	if got := rs.Rules(); len(got) != 1 || got[0].ID() != 10 {
		t.Errorf("Rules() = %v, want the one rule", got)
	}
}
