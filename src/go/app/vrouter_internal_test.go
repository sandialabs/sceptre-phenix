package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// vrouterIfaces returns an inside (eth0) and an outside (eth1) router interface.
func vrouterIfaces() []*v1.Interface {
	return []*v1.Interface{
		{NameF: "eth0", TypeF: "ethernet", ProtoF: "static", VLANF: "inside", AddressF: "10.0.0.1", MaskF: 24},
		{NameF: "eth1", TypeF: "ethernet", ProtoF: "static", VLANF: "outside", AddressF: "192.168.1.1", MaskF: 24},
	}
}

func vrouterNetIfaces() []ifaces.NodeNetworkInterface {
	return (&v1.Network{InterfacesF: vrouterIfaces()}).Interfaces()
}

func vrouterNode(hostname, osType string) *v1.Node {
	return &v1.Node{
		TypeF:     "Router",
		GeneralF:  &v1.General{HostnameF: hostname},
		HardwareF: &v1.Hardware{OSTypeF: osType},
		NetworkF:  &v1.Network{InterfacesF: vrouterIfaces()},
	}
}

func vrouterExperiment(t *testing.T, scenario *v2.ScenarioSpec, nodes ...*v1.Node) *types.Experiment {
	t.Helper()

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "vrouter-test"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "vrouter-test",
			BaseDirF:        t.TempDir(),
			TopologyF:       &v1.TopologySpec{NodesF: nodes},
			ScenarioF:       scenario,
		},
		Status: &v1.ExperimentStatus{},
	}
}

func vrouterScenario(apps ...*v2.ScenarioApp) *v2.ScenarioSpec {
	return &v2.ScenarioSpec{AppsF: apps}
}

// vrouterApp returns a vrouter app with md for the router named rtr.
func vrouterApp(md map[string]any) *v2.ScenarioApp {
	return &v2.ScenarioApp{
		NameF:  appNameVrouter,
		HostsF: []*v2.ScenarioAppHost{{HostnameF: "rtr", MetadataF: md}},
	}
}

// renderVrouter runs PreStart and returns the rendered config of the router named rtr.
func renderVrouter(t *testing.T, exp *types.Experiment) string {
	t.Helper()

	var v Vrouter

	if err := v.PreStart(t.Context(), exp); err != nil {
		t.Fatalf("PreStart() error = %v", err)
	}

	b, err := os.ReadFile(filepath.Join(exp.Spec.BaseDir(), "vrouter", "rtr.boot"))
	if err != nil {
		t.Fatalf("reading rendered config: %v", err)
	}

	return string(b)
}

// TestVrouterPreStartScenarioSNATWins is a regression test: the topology NAT
// rules replaced the source NAT rules that the scenario host metadata gave.
func TestVrouterPreStartScenarioSNATWins(t *testing.T) {
	t.Parallel()

	node := vrouterNode("rtr", osTypeVyos)
	node.NetworkF.NATF = []v1.NAT{{InF: []string{"eth0"}, OutF: "eth1"}}

	md := map[string]any{
		"snat": []any{map[string]any{
			"interface":   "eth1",
			"srcAddr":     "172.16.0.0/16",
			"translation": "192.168.1.50",
		}},
	}

	got := renderVrouter(t, vrouterExperiment(t, vrouterScenario(vrouterApp(md)), node))

	for _, want := range []string{
		"set nat source rule 100 outbound-interface name eth1\n",
		"set nat source rule 100 source address 172.16.0.0/16\n",
		"set nat source rule 100 translation address 192.168.1.50\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config does not contain %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "source address 10.0.0.0/24") {
		t.Errorf("rendered config contains the topology NAT rule, want only the scenario rule:\n%s", got)
	}
}

func TestVrouterPreStartTopologyNAT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scenario *v2.ScenarioSpec
	}{
		{name: "no scenario"},
		{name: "scenario host without NAT", scenario: vrouterScenario(vrouterApp(map[string]any{}))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := vrouterNode("rtr", osTypeVyos)
			node.NetworkF.NATF = []v1.NAT{{InF: []string{"eth0"}, OutF: "eth1"}}

			got := renderVrouter(t, vrouterExperiment(t, tt.scenario, node))

			for _, want := range []string{
				"set nat source rule 100 outbound-interface name eth1\n",
				"set nat source rule 100 source address 10.0.0.0/24\n",
				"set nat source rule 100 translation address masquerade\n",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("rendered config does not contain %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestVrouterPreStartTopologyNATMissingOutbound(t *testing.T) {
	t.Parallel()

	node := vrouterNode("rtr", osTypeVyos)
	node.NetworkF.NATF = []v1.NAT{{InF: []string{"eth0"}, OutF: "eth9"}}

	var v Vrouter
	if err := v.PreStart(t.Context(), vrouterExperiment(t, nil, node)); err == nil {
		t.Error("PreStart() with an unknown NAT outbound interface error = nil, want an error")
	}
}

func TestVrouterPreStartScenarioServices(t *testing.T) {
	t.Parallel()

	node := vrouterNode("rtr", osTypeVyos)
	node.AnnotationsF = map[string]any{"vrouter/enable-ssh": "eth0", "vrouter/vyos-password": "s3cret"}

	md := map[string]any{
		"ipsec": []any{map[string]any{
			"local":   "192.168.1.1",
			"remote":  "192.168.2.1",
			"peer":    "192.168.2.1",
			"secret":  "psk",
			"tunnels": []any{map[string]any{"local": "10.0.0.0/24", "remote": "10.1.0.0/24"}},
		}, map[string]any{
			"local":   "192.168.1.1",
			"remote":  "192.168.3.1",
			"peer":    "192.168.3.1",
			"secret":  "psk2",
			"tunnels": []any{map[string]any{"local": "10.0.0.0/24", "remote": "10.2.0.0/24"}},
		}},
		"snmp": map[string]any{
			"listenAddress": "10.0.0.1",
			"systemName":    "rtr",
			"communities":   []any{map[string]any{"name": "public", "clients": []any{"10.0.0.9"}}},
		},
		"emulators": []any{map[string]any{"name": "slow", "bandwidth": "1mbit", "delay": 20, "egress": []any{"eth1"}}},
		"dnat": []any{map[string]any{
			"interface":   "eth1",
			"dstPort":     80,
			"protocol":    "tcp",
			"translation": "10.0.0.5:8080",
		}},
	}

	got := renderVrouter(t, vrouterExperiment(t, vrouterScenario(vrouterApp(md)), node))

	for _, want := range []string{
		"set interfaces ethernet eth0 address 10.0.0.1/24\n",
		"set interfaces ethernet eth1 address 192.168.1.1/24\n",
		"set vpn ipsec interface eth1\n",
		"set vpn ipsec ike-group IKE-1W key-exchange ikev2\n",
		"set vpn ipsec ike-group IKE-1W proposal 1 encryption aes256\n",
		"set vpn ipsec ike-group IKE-1W proposal 1 hash sha1\n",
		"set vpn ipsec authentication psk PSK-0 id '192.168.2.1'\n",
		"set vpn ipsec authentication psk PSK-0 secret 'psk'\n",
		"set vpn ipsec authentication psk PSK-1 id '192.168.3.1'\n",
		"set vpn ipsec authentication psk PSK-1 secret 'psk2'\n",
		"set service snmp listen-address 10.0.0.1\n",
		"set service snmp community 'public' authorization ro\n",
		"set service snmp community 'public' client 10.0.0.9\n",
		"set qos policy network-emulator slow bandwidth '1mbit'\n",
		"set qos interface eth1 egress slow\n",
		"set nat destination rule 100 inbound-interface name eth1\n",
		"set nat destination rule 100 translation address 10.0.0.5\n",
		"set nat destination rule 100 translation port '8080'\n",
		"set service ssh listen-address 10.0.0.1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config does not contain %q:\n%s", want, got)
		}
	}

	for _, unwanted := range []string{"set interface ethernet", "esp-group IKE-1W", "psk PSK-KEY"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("rendered config contains %q:\n%s", unwanted, got)
		}
	}

	if diff := cmp.Diff(
		[]string{"/boot/vyos/rw/config/scripts/custom/vyos.script"},
		injectionDsts(node),
	); diff != "" {
		t.Errorf("node injections (-want +got):\n%s", diff)
	}
}

func injectionDsts(node *v1.Node) []string {
	dsts := make([]string, 0, len(node.InjectionsF))
	for _, inj := range node.InjectionsF {
		dsts = append(dsts, inj.DstF)
	}

	return dsts
}

func TestVrouterPreStartScenarioErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		md   map[string]any
	}{
		{name: "ipsec local not found", md: map[string]any{"ipsec": []any{map[string]any{"local": "1.2.3.4"}}}},
		{name: "emulators not a list", md: map[string]any{"emulators": "slow"}},
		{name: "snmp community without name", md: map[string]any{"snmp": map[string]any{"communities": []any{map[string]any{}}}}},
		{name: "snat interface not found", md: map[string]any{"snat": []any{map[string]any{"interface": "eth9"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := vrouterExperiment(t, vrouterScenario(vrouterApp(tt.md)), vrouterNode("rtr", osTypeVyos))

			var v Vrouter
			if err := v.PreStart(t.Context(), exp); err == nil {
				t.Errorf("PreStart() with metadata %v error = nil, want an error", tt.md)
			}
		})
	}
}

func TestVrouterPreStartNodeSelection(t *testing.T) {
	t.Parallel()

	external := true
	disabled := vrouterNode("disabled", osTypeVyos)
	disabled.AnnotationsF = map[string]any{defaultAppsAnnotation: false}

	ext := vrouterNode("ext", osTypeVyos)
	ext.ExternalF = &external

	host := vrouterNode("host", osTypeVyos)
	host.TypeF = "VirtualMachine"

	nodes := []*v1.Node{
		vrouterNode("vyatta", "vyatta"),
		vrouterNode("legacy", osLinux),
		vrouterNode("fw", osTypeVyos),
		vrouterNode("mini", "minirouter"),
		vrouterNode("win", "windows"),
		disabled,
		ext,
		host,
	}
	nodes[2].TypeF = "Firewall"

	exp := vrouterExperiment(t, nil, nodes...)

	var v Vrouter
	if err := v.PreStart(t.Context(), exp); err != nil {
		t.Fatalf("PreStart() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(exp.Spec.BaseDir(), "vrouter"))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}

	if diff := cmp.Diff([]string{"fw.boot", "legacy.boot", "vyatta.boot"}, got); diff != "" {
		t.Errorf("rendered configs (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"/opt/vyatta/etc/config/config.boot"}, injectionDsts(nodes[0])); diff != "" {
		t.Errorf("vyatta node injections (-want +got):\n%s", diff)
	}
}

func TestVrouterPreStartNTP(t *testing.T) {
	t.Parallel()

	ntpApp := func(hostMD map[string]any) *v2.ScenarioApp {
		return &v2.ScenarioApp{
			NameF:     appNameNTP,
			MetadataF: map[string]any{"defaultSource": map[string]any{"address": "10.9.9.9"}},
			HostsF:    []*v2.ScenarioAppHost{{HostnameF: "rtr", MetadataF: hostMD}},
		}
	}

	tests := []struct {
		name     string
		labelled bool
		scenario *v2.ScenarioSpec
		want     string
		wantErr  bool
	}{
		{name: "labelled NTP server", labelled: true, want: "set service ntp server 172.20.0.5 prefer\n"},
		{
			name:     "ntp app host source",
			scenario: vrouterScenario(ntpApp(map[string]any{"client": "ntp", "source": map[string]any{"address": "10.1.1.1"}})),
			want:     "set service ntp server 10.1.1.1 prefer\n",
		},
		{
			name: "ntp app source by hostname and interface",
			scenario: vrouterScenario(ntpApp(map[string]any{
				"client": "ntp",
				"source": map[string]any{"hostname": "rtr", "interface": "eth1"},
			})),
			want: "set service ntp server 192.168.1.1 prefer\n",
		},
		{
			name:     "ntp app default source",
			scenario: vrouterScenario(ntpApp(map[string]any{"client": "ntp"})),
			want:     "set service ntp server 10.9.9.9 prefer\n",
		},
		{
			name: "ntp app without any source",
			scenario: vrouterScenario(
				&v2.ScenarioApp{
					NameF:  appNameNTP,
					HostsF: []*v2.ScenarioAppHost{{HostnameF: "rtr", MetadataF: map[string]any{"client": "ntp"}}},
				},
			),
			wantErr: true,
		},
		{
			name:     "ntp app with another client",
			scenario: vrouterScenario(ntpApp(map[string]any{"client": "chrony"})),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes := []*v1.Node{vrouterNode("rtr", osTypeVyos)}

			if tt.labelled {
				nodes = append(nodes, &v1.Node{
					TypeF:    "VirtualMachine",
					LabelsF:  map[string]string{"ntp-server": "true"},
					GeneralF: &v1.General{HostnameF: "ntp"},
					NetworkF: &v1.Network{InterfacesF: []*v1.Interface{
						{NameF: "eth0", VLANF: "inside", AddressF: "10.0.0.5"},
						{NameF: "mgmt", VLANF: "MGMT", AddressF: "172.20.0.5"},
					}},
				})
			}

			exp := vrouterExperiment(t, tt.scenario, nodes...)

			if tt.wantErr {
				var v Vrouter
				if err := v.PreStart(t.Context(), exp); err == nil {
					t.Error("PreStart() error = nil, want an error")
				}

				return
			}

			if got := renderVrouter(t, exp); !strings.Contains(got, tt.want) {
				t.Errorf("rendered config does not contain %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestVrouterConfigure(t *testing.T) {
	t.Parallel()

	acl := map[string]any{
		"acl": map[string]any{
			"ingress":  map[string]any{"eth0": "in-rules"},
			"rulesets": []any{map[string]any{"name": "in-rules", "default": "drop"}},
		},
	}

	disabled := vrouterNode("disabled", osTypeVyos)
	disabled.AnnotationsF = map[string]any{defaultAppsAnnotation: false}

	rtr := vrouterNode("rtr", osTypeVyos)

	app := &v2.ScenarioApp{
		NameF: appNameVrouter,
		HostsF: []*v2.ScenarioAppHost{
			{HostnameF: "rtr", MetadataF: acl},
			{HostnameF: "disabled", MetadataF: acl},
			{HostnameF: "missing", MetadataF: acl},
		},
	}

	exp := vrouterExperiment(t, vrouterScenario(&v2.ScenarioApp{NameF: "other"}, app), rtr, disabled)

	var v Vrouter
	if err := v.Configure(t.Context(), exp); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	if got := rtr.NetworkF.InterfacesF[0].RulesetOutF; got != "in-rules" {
		t.Errorf("rtr eth0 RulesetOut = %q, want %q", got, "in-rules")
	}

	if got := len(disabled.NetworkF.RulesetsF); got != 0 {
		t.Errorf("node with default apps disabled has %d rulesets, want 0", got)
	}

	app.HostsF[0].MetadataF = map[string]any{"acl": map[string]any{"egress": map[string]any{"eth0": "nope"}}}

	if err := v.Configure(t.Context(), exp); err == nil {
		t.Error("Configure() with an unknown ruleset error = nil, want an error")
	}
}

func TestVrouterProcessACL(t *testing.T) {
	t.Parallel()

	existing := func() []*v1.Ruleset {
		return []*v1.Ruleset{{NameF: "existing", DefaultF: "accept"}}
	}

	tests := []struct {
		name           string
		md             map[string]any
		wantErr        bool
		wantRulesets   []string
		wantRulesetIn  []string
		wantRulesetOut []string
	}{
		{
			name:           "no acl",
			md:             map[string]any{},
			wantRulesets:   []string{"existing"},
			wantRulesetIn:  []string{"", ""},
			wantRulesetOut: []string{"", ""},
		},
		{
			name: "rulesets added once and bound",
			md: map[string]any{"acl": map[string]any{
				"ingress": map[string]any{"eth0": "new"},
				"egress":  map[string]any{"eth1": "existing"},
				"rulesets": []any{
					map[string]any{"name": "new", "default": "drop"},
					map[string]any{"name": "existing", "default": "drop"},
				},
			}},
			wantRulesets:   []string{"existing", "new"},
			wantRulesetIn:  []string{"", "existing"},
			wantRulesetOut: []string{"new", ""},
		},
		{name: "acl not a map", md: map[string]any{"acl": "x"}, wantErr: true},
		{name: "ruleset not a map", md: map[string]any{"acl": map[string]any{"rulesets": []any{"x"}}}, wantErr: true},
		{
			name:    "ingress unknown ruleset",
			md:      map[string]any{"acl": map[string]any{"ingress": map[string]any{"eth0": "nope"}}},
			wantErr: true,
		},
		{
			name:    "ingress unknown interface",
			md:      map[string]any{"acl": map[string]any{"ingress": map[string]any{"eth9": "existing"}}},
			wantErr: true,
		},
		{
			name:    "egress unknown ruleset",
			md:      map[string]any{"acl": map[string]any{"egress": map[string]any{"eth0": "nope"}}},
			wantErr: true,
		},
		{
			name:    "egress unknown interface",
			md:      map[string]any{"acl": map[string]any{"egress": map[string]any{"eth9": "existing"}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			network := &v1.Network{InterfacesF: vrouterIfaces(), RulesetsF: existing()}

			err := Vrouter{}.processACL(tt.md, network)
			if tt.wantErr {
				if err == nil {
					t.Errorf("processACL(%v) error = nil, want an error", tt.md)
				}

				return
			}

			if err != nil {
				t.Fatalf("processACL(%v) error = %v", tt.md, err)
			}

			var names, in, out []string
			for _, rs := range network.RulesetsF {
				names = append(names, rs.NameF)
			}

			for _, iface := range network.InterfacesF {
				in = append(in, iface.RulesetInF)
				out = append(out, iface.RulesetOutF)
			}

			if diff := cmp.Diff(tt.wantRulesets, names); diff != "" {
				t.Errorf("rulesets (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantRulesetIn, in); diff != "" {
				t.Errorf("interface RulesetIn (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantRulesetOut, out); diff != "" {
				t.Errorf("interface RulesetOut (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVrouterProcessNAT(t *testing.T) {
	t.Parallel()

	opts := cmp.AllowUnexported(NATRule{})

	tests := []struct {
		name     string
		md       map[string]any
		wantSrc  []NATRule
		wantDst  []NATRule
		wantErr  bool
		errorMsg string
	}{
		{name: "no nat", md: map[string]any{}},
		{
			name: "snat and dnat",
			md: map[string]any{
				"snat": []any{map[string]any{"interface": "eth1", "srcAddr": "10.0.0.0/24", "translation": "192.168.1.50"}},
				"dnat": []any{map[string]any{"interface": "eth0", "dstPort": 80, "protocol": "tcp", "translation": "10.0.0.5:8080"}},
			},
			wantSrc: []NATRule{{Interface: "eth1", SourceAddress: "10.0.0.0/24", Translation: "192.168.1.50", ifaceIndex: 1}},
			wantDst: []NATRule{{Interface: "eth0", DestinationPort: 80, Protocol: "tcp", Translation: "10.0.0.5:8080", ifaceIndex: 0}},
		},
		{name: "snat not a list", md: map[string]any{"snat": "x"}, wantErr: true},
		{name: "dnat not a list", md: map[string]any{"dnat": "x"}, wantErr: true},
		{name: "snat interface not found", md: map[string]any{"snat": []any{map[string]any{"interface": "eth9"}}}, wantErr: true},
		{name: "dnat interface not found", md: map[string]any{"dnat": []any{map[string]any{"interface": "eth9"}}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var v Vrouter

			src, dst, err := v.processNAT(tt.md, vrouterNetIfaces())
			if tt.wantErr {
				if err == nil {
					t.Errorf("processNAT(%v) error = nil, want an error", tt.md)
				}

				return
			}

			if err != nil {
				t.Fatalf("processNAT(%v) error = %v", tt.md, err)
			}

			if diff := cmp.Diff(tt.wantSrc, src, opts); diff != "" {
				t.Errorf("processNAT() sources (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantDst, dst, opts); diff != "" {
				t.Errorf("processNAT() destinations (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNATRuleTranslation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		translation, wantAddr, wantPort string
	}{
		{translation: "", wantAddr: "", wantPort: ""},
		{translation: "10.0.0.5", wantAddr: "10.0.0.5", wantPort: ""},
		{translation: "10.0.0.5:8080", wantAddr: "10.0.0.5", wantPort: "8080"},
	}

	for _, tt := range tests {
		rule := NATRule{Translation: tt.translation, ifaceIndex: 3}

		if got := rule.TranslationAddress(); got != tt.wantAddr {
			t.Errorf("NATRule{Translation: %q}.TranslationAddress() = %q, want %q", tt.translation, got, tt.wantAddr)
		}

		if got := rule.TranslationPort(); got != tt.wantPort {
			t.Errorf("NATRule{Translation: %q}.TranslationPort() = %q, want %q", tt.translation, got, tt.wantPort)
		}

		if got := rule.InterfaceIndex(); got != 3 {
			t.Errorf("NATRule.InterfaceIndex() = %d, want 3", got)
		}
	}
}

func TestVrouterProcessSNMP(t *testing.T) {
	t.Parallel()

	full := map[string]any{
		"listenAddress": "10.0.0.1",
		"systemName":    "rtr",
		"location":      "lab",
		"contact":       "ops",
		"communities": []any{map[string]any{
			"name":          "public",
			"authorization": "rw",
			"clients":       []any{"10.0.0.9"},
			"trapTargets":   []any{"10.0.0.10"},
		}},
	}

	want := &SNMPConfig{ListenAddr: "10.0.0.1", SystemName: "rtr", Location: "lab", Contact: "ops"}
	want.Communities = append(want.Communities, struct {
		Name          string   `mapstructure:"name"`
		Authorization string   `mapstructure:"authorization"`
		Clients       []string `mapstructure:"clients"`
		TrapTargets   []string `mapstructure:"trapTargets"`
	}{Name: "public", Authorization: "rw", Clients: []string{"10.0.0.9"}, TrapTargets: []string{"10.0.0.10"}})

	tests := []struct {
		name     string
		md       map[string]any
		want     *SNMPConfig
		wantErr  bool
		sentinel error
	}{
		{name: "full", md: map[string]any{"snmp": full}, want: want},
		{name: "missing", md: map[string]any{}, wantErr: true, sentinel: ErrSNMPConfigNotFound},
		{name: "empty", md: map[string]any{"snmp": map[string]any{}}, wantErr: true, sentinel: ErrSNMPConfigNotFound},
		{name: "not a map", md: map[string]any{"snmp": "x"}, wantErr: true},
		{
			name:    "community without name",
			md:      map[string]any{"snmp": map[string]any{"communities": []any{map[string]any{"clients": []any{"1.2.3.4"}}}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Vrouter{}.processSNMP(tt.md)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("processSNMP(%v) = %v, want an error", tt.md, got)
				}

				if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
					t.Errorf("processSNMP(%v) error = %v, want %v", tt.md, err, tt.sentinel)
				}

				if tt.sentinel == nil && errors.Is(err, ErrSNMPConfigNotFound) {
					t.Errorf("processSNMP(%v) error = %v, want an error other than %v", tt.md, err, ErrSNMPConfigNotFound)
				}

				return
			}

			if err != nil {
				t.Fatalf("processSNMP(%v) error = %v", tt.md, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("processSNMP() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVrouterProcessIPSecErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		md       map[string]any
		sentinel error
	}{
		{name: "missing", md: map[string]any{}, sentinel: ErrIPSecConfigNotFound},
		{name: "not a list", md: map[string]any{"ipsec": "x"}},
		{name: "local address not on router", md: map[string]any{"ipsec": []any{map[string]any{"local": "1.2.3.4"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var v Vrouter

			got, err := v.processIPSec(tt.md, vrouterNetIfaces())
			if err == nil {
				t.Fatalf("processIPSec(%v) = %v, want an error", tt.md, got)
			}

			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("processIPSec(%v) error = %v, want %v", tt.md, err, tt.sentinel)
			}
		})
	}
}

func TestVrouterProcessIPSec(t *testing.T) {
	t.Parallel()

	site := func(local, remote, secret string) map[string]any {
		return map[string]any{"ipsec": []any{map[string]any{
			"local":   local,
			"remote":  remote,
			"peer":    remote,
			"secret":  secret,
			"tunnels": []any{map[string]any{"local": "10.0.0.0/24", "remote": "10.1.0.0/24"}},
		}}}
	}

	var v Vrouter

	given, err := v.processIPSec(site("192.168.1.1", "192.168.2.1", "psk"), vrouterNetIfaces())
	if err != nil {
		t.Fatalf("processIPSec() error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"eth1": "eth1"}, given.Interfaces); diff != "" {
		t.Errorf("processIPSec() interfaces (-want +got):\n%s", diff)
	}

	if got := given.Sites[0]; got.PresharedKey != "psk" || got.Peer != "192.168.2.1" || len(got.Tunnels) != 1 {
		t.Errorf("processIPSec() site = %+v, want the given secret, peer and one tunnel", got)
	}

	// The router at the other end of the tunnel has 192.168.2.1 on its eth0.
	otherEnd := (&v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", AddressF: "192.168.2.1"}}}).Interfaces()

	keyOf := func(md map[string]any, nets []ifaces.NodeNetworkInterface) string {
		t.Helper()

		cfg, err := v.processIPSec(md, nets)
		if err != nil {
			t.Fatalf("processIPSec(%v) error = %v", md, err)
		}

		return cfg.Sites[0].PresharedKey
	}

	key := keyOf(site("192.168.1.1", "192.168.2.1", ""), vrouterNetIfaces())
	if len(key) != ipsecSecretLength {
		t.Errorf("generated preshared key %q has length %d, want %d", key, len(key), ipsecSecretLength)
	}

	if got := keyOf(site("192.168.1.1", "192.168.2.1", ""), vrouterNetIfaces()); got != key {
		t.Errorf("preshared key for the same site again = %q, want %q", got, key)
	}

	if got := keyOf(site("192.168.2.1", "192.168.1.1", ""), otherEnd); got != key {
		t.Errorf("preshared key at the other end of the tunnel = %q, want %q", got, key)
	}

	if got := keyOf(site("192.168.1.1", "192.168.3.1", ""), vrouterNetIfaces()); got == key {
		t.Errorf("preshared key for another site pair = %q, want a new key", got)
	}
}
