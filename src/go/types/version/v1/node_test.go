package v1

import (
	"strings"
	"testing"
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
