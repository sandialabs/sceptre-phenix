package v1

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func boolPtr(b bool) *bool { return &b }

func nodeWithGateways(external *bool, gateways ...string) Node {
	ifaces := make([]*Interface, len(gateways))
	for i, gw := range gateways {
		ifaces[i] = &Interface{
			NameF:    "eth" + string(rune('0'+i)),
			GatewayF: gw,
		}
	}

	return Node{
		GeneralF:  &General{HostnameF: "test-node"},
		ExternalF: external,
		NetworkF:  &Network{InterfacesF: ifaces},
	}
}

func nodeWithIfaces(ifaces ...*Interface) Node {
	return Node{
		GeneralF: &General{HostnameF: "test-node"},
		NetworkF: &Network{InterfacesF: ifaces},
	}
}

func TestNodeValidate(t *testing.T) {
	tests := []struct {
		name    string
		node    Node
		wantErr bool
		// substr, when set, must appear in the returned error message.
		substr string
	}{
		{
			name: "internal node, single gateway",
			node: nodeWithGateways(nil, "10.0.0.1", ""),
		},
		{
			name: "internal node, no gateways",
			node: nodeWithGateways(nil, "", ""),
		},
		{
			name: "internal node, nil network",
			node: Node{GeneralF: &General{HostnameF: "test-node"}},
		},
		{
			name:    "internal node, two gateways",
			node:    nodeWithGateways(nil, "10.0.0.1", "10.0.1.1"),
			wantErr: true,
			substr:  "more than one gateway",
		},
		{
			name:    "internal node, same gateway on two interfaces",
			node:    nodeWithGateways(nil, "10.0.0.1", "10.0.0.1"),
			wantErr: true,
			substr:  "more than one gateway",
		},
		{
			name: "external node with two gateways is exempt",
			node: nodeWithGateways(boolPtr(true), "10.0.0.1", "10.0.1.1"),
		},
		{
			name:    "external key explicitly false is rejected",
			node:    nodeWithGateways(boolPtr(false), "10.0.0.1"),
			wantErr: true,
			substr:  "external key should not be included",
		},
		{
			name: "internal node, duplicate interface names",
			node: nodeWithIfaces(
				&Interface{NameF: "eth0"},
				&Interface{NameF: "eth0"},
			),
			wantErr: true,
			substr:  "defined more than once",
		},
		{
			name: "internal node, gateway on dhcp interface",
			node: nodeWithIfaces(
				&Interface{NameF: "eth0", ProtoF: "dhcp", GatewayF: "10.0.0.1"},
			),
			wantErr: true,
			substr:  "the gateway is ignored",
		},
		{
			name: "internal node, gateway on manual interface",
			node: nodeWithIfaces(
				&Interface{NameF: "eth0", ProtoF: "manual", GatewayF: "10.0.0.1"},
			),
			wantErr: true,
			substr:  "the gateway is ignored",
		},
		{
			name: "internal node, delay timer that parses",
			node: Node{GeneralF: &General{HostnameF: "test-node"}, DelayF: &Delay{TimerF: "1h30m"}},
		},
		{
			name:    "internal node, delay timer without a unit",
			node:    Node{GeneralF: &General{HostnameF: "test-node"}, DelayF: &Delay{TimerF: "5"}},
			wantErr: true,
			substr:  "is not a duration",
		},
		{
			name:    "internal node, delay timer too long for a duration",
			node:    Node{GeneralF: &General{HostnameF: "test-node"}, DelayF: &Delay{TimerF: "9999999999h"}},
			wantErr: true,
			substr:  "is not a duration",
		},
		{
			name: "internal node, gateway on static interface is allowed",
			node: nodeWithIfaces(
				&Interface{
					NameF: "eth0", ProtoF: "static",
					AddressF: "10.0.0.2", MaskF: 24, GatewayF: "10.0.0.1",
				},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.node.validate()

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil")
				}

				if tt.substr != "" && !strings.Contains(err.Error(), tt.substr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.substr)
				}

				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// genNodeInterface draws an interface, or none, from few names, protocols and gateways, so
// repeated names and gateways on dhcp or manual interfaces are common.
func genNodeInterface() *rapid.Generator[*Interface] {
	return rapid.Custom(func(t *rapid.T) *Interface {
		if rapid.IntRange(0, 7).Draw(t, "nil") == 0 {
			return nil
		}

		return &Interface{
			NameF:    rapid.SampledFrom([]string{"", "eth0", "eth1"}).Draw(t, "name"),
			ProtoF:   rapid.SampledFrom([]string{"static", "DHCP", "Manual", ""}).Draw(t, "proto"),
			GatewayF: rapid.SampledFrom([]string{"10.0.0.1", "", "10.0.1.1"}).Draw(t, "gateway"),
		}
	})
}

// nodeIfaceSummary writes each interface as name/proto/gateway.
func nodeIfaceSummary(ifaces []*Interface) []string {
	summary := make([]string, len(ifaces))

	for i, iface := range ifaces {
		summary[i] = "nil"
		if iface != nil {
			summary[i] = iface.NameF + "/" + iface.ProtoF + "/" + iface.GatewayF
		}
	}

	return summary
}

// An internal node is valid exactly when its named interfaces have distinct names, no dhcp
// or manual interface sets a gateway, and at most one interface does; an external one always is.
func TestNodeValidateProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		ifaces := rapid.SliceOfN(genNodeInterface(), rapid.IntRange(0, 2).Draw(t, "min"), 4).Draw(t, "ifaces")
		var external *bool
		if rapid.IntRange(0, 7).Draw(t, "external") == 0 {
			external = boolPtr(true)
		}

		var (
			names    = make(map[string]bool)
			gateways int
			valid    = true
		)

		for _, iface := range ifaces {
			if iface == nil {
				continue
			}

			if iface.NameF != "" {
				valid = valid && !names[iface.NameF]
				names[iface.NameF] = true
			}

			if iface.GatewayF != "" {
				gateways++
				valid = valid && !strings.EqualFold(iface.ProtoF, "dhcp") && !strings.EqualFold(iface.ProtoF, "manual")
			}
		}

		valid = external != nil || valid && gateways <= 1

		node := nodeWithIfaces(ifaces...)
		node.ExternalF = external

		if err := node.validate(); (err == nil) != valid {
			t.Fatalf("Node{interfaces %v, external %v}.validate() = %v, want valid %t",
				nodeIfaceSummary(ifaces), external != nil, err, valid)
		}
	})
}
