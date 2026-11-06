package v1

import (
	"errors"
	"fmt"
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

// firecrackerTestNode returns a valid firecracker node web1 with one drive and
// nics interfaces. change edits it.
func firecrackerTestNode(nics int, change func(*Node)) Node {
	ifaces := make([]*Interface, nics)
	for i := range ifaces {
		ifaces[i] = &Interface{NameF: fmt.Sprintf("eth%d", i), VLANF: "EXP"}
	}

	n := Node{
		GeneralF: &General{HostnameF: "web1", VMTypeF: "firecracker"},
		HardwareF: &Hardware{
			OSTypeF: "linux", KernelF: "vmlinux", DrivesF: []*Drive{{ImageF: "debian.qc2"}},
		},
		NetworkF: &Network{InterfacesF: ifaces},
	}

	if change != nil {
		change(&n)
	}

	return n
}

func TestNodeValidateFirecracker(t *testing.T) {
	tests := []struct {
		name string
		node Node
		// substr is part of the error, or "" when the node is valid.
		substr string
	}{
		{name: "valid", node: firecrackerTestNode(1, nil)},
		{
			name: "unset os_type, vcpus and snapshot use the defaults",
			node: firecrackerTestNode(1, func(n *Node) { n.HardwareF.OSTypeF = "" }),
		},
		{
			name:   "windows",
			node:   firecrackerTestNode(1, func(n *Node) { n.HardwareF.OSTypeF = "windows" }),
			substr: "run only linux",
		},
		{
			name:   "no kernel",
			node:   firecrackerTestNode(1, func(n *Node) { n.HardwareF.KernelF = " " }),
			substr: "hardware.kernel is not set",
		},
		{name: "32 vcpus", node: firecrackerTestNode(1, func(n *Node) { n.HardwareF.VCPUF = 32 })},
		{
			name:   "33 vcpus",
			node:   firecrackerTestNode(1, func(n *Node) { n.HardwareF.VCPUF = 33 }),
			substr: "have 1 to 32 vCPUs",
		},
		{
			name:   "negative vcpus",
			node:   firecrackerTestNode(1, func(n *Node) { n.HardwareF.VCPUF = -1 }),
			substr: "have 1 to 32 vCPUs",
		},
		{
			name: "qcow2 without snapshot",
			node: firecrackerTestNode(1, func(n *Node) {
				n.GeneralF.SnapshotF = boolPtr(false)
				n.HardwareF.DrivesF[0].ImageF = "debian.QCOW2"
			}),
			substr: "need a raw image",
		},
		{
			name: "raw without snapshot",
			node: firecrackerTestNode(1, func(n *Node) {
				n.GeneralF.SnapshotF = boolPtr(false)
				n.HardwareF.DrivesF[0].ImageF = "debian.raw"
			}),
		},
		{
			name:   "serial interface",
			node:   firecrackerTestNode(1, func(n *Node) { n.NetworkF.InterfacesF[0].TypeF = "serial" }),
			substr: "is a serial interface",
		},
		{
			name:   "qinq interface",
			node:   firecrackerTestNode(1, func(n *Node) { n.NetworkF.InterfacesF[0].QinQF = true }),
			substr: "not supported for firecracker nodes yet",
		},
		{name: "17 devices", node: firecrackerTestNode(14, nil)},
		{
			name: "19 devices",
			node: firecrackerTestNode(15, func(n *Node) {
				n.HardwareF.DrivesF = append(n.HardwareF.DrivesF, &Drive{ImageF: "data.raw"})
			}),
			substr: "node web1 needs 19 Firecracker devices (2 disks, 15 NICs, vsock, entropy); " +
				"the limit is 17 without firecracker-pci",
		},
		{
			name: "no entropy device",
			node: firecrackerTestNode(15, func(n *Node) {
				n.AdvancedF = map[string]string{"firecracker-entropy": "false"}
			}),
		},
		{
			name: "31 devices with pci",
			node: firecrackerTestNode(28, func(n *Node) {
				n.AdvancedF = map[string]string{"firecracker-pci": "true"}
			}),
		},
		{
			name: "32 devices with pci",
			node: firecrackerTestNode(29, func(n *Node) {
				n.AdvancedF = map[string]string{"firecracker-pci": "true"}
			}),
			substr: "the limit is 31 with firecracker-pci",
		},
		{
			name: "overrides and a drive interface only warn",
			node: firecrackerTestNode(1, func(n *Node) {
				n.OverridesF = map[string]string{"a": "b"}
				n.HardwareF.DrivesF[0].IfaceF = "ide"
			}),
		},
		{
			name: "kvm node is not checked",
			node: firecrackerTestNode(1, func(n *Node) {
				n.GeneralF.VMTypeF = "kvm"
				n.HardwareF.OSTypeF = "windows"
				n.HardwareF.KernelF = ""
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.node.validate()

			if tt.substr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, ErrFirecrackerNode) {
				t.Fatalf("validate() = %v, want an error wrapping ErrFirecrackerNode", err)
			}

			if !strings.Contains(err.Error(), tt.substr) {
				t.Errorf("validate() = %q, want it to contain %q", err.Error(), tt.substr)
			}
		})
	}
}

func TestNodeSetDefaultsFirecrackerCPU(t *testing.T) {
	for vmType, want := range map[string]string{"kvm": "Broadwell", "firecracker": ""} {
		t.Run(vmType, func(t *testing.T) {
			n := firecrackerTestNode(1, func(n *Node) { n.GeneralF.VMTypeF = vmType })
			n.setDefaults("phenix")

			if got := n.HardwareF.CPUF; got != want {
				t.Errorf("setDefaults() cpu = %q, want %q", got, want)
			}
		})
	}
}

func TestHardwareFirecrackerAppend(t *testing.T) {
	for args, want := range map[string]string{
		"":                         "root=/dev/vda1 rw",
		"console=ttyS0":            "console=ttyS0 root=/dev/vda1 rw",
		" root=/dev/vda ro quiet ": "root=/dev/vda ro quiet",
		"quiet root=LABEL=rootfs":  "quiet root=LABEL=rootfs",
		"rootdelay=5":              "rootdelay=5 root=/dev/vda1 rw",
	} {
		t.Run(args, func(t *testing.T) {
			if got := (&Hardware{KernelArgsF: args}).FirecrackerAppend(); got != want {
				t.Errorf("FirecrackerAppend(%q) = %q, want %q", args, got, want)
			}
		})
	}
}
