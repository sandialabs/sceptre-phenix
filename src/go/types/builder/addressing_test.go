package builder_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

func TestParseSubnet(t *testing.T) {
	valid := []struct {
		name, text, want string
	}{
		{name: "24-bit prefix", text: "10.1.0.0/24", want: "10.1.0.0/24"},
		{name: "default route", text: "0.0.0.0/0", want: "0.0.0.0/0"},
		{name: "31-bit point to point", text: "192.0.2.4/31", want: "192.0.2.4/31"},
		{name: "single host", text: "192.0.2.7/32", want: "192.0.2.7/32"},
		{name: "12-bit private prefix", text: "172.16.0.0/12", want: "172.16.0.0/12"},
		{name: "all ones network", text: "255.255.255.0/24", want: "255.255.255.0/24"},
	}

	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			prefix, ok := builder.ParseSubnet(test.text)
			if !ok || prefix.String() != test.want {
				t.Errorf("ParseSubnet(%q) = %v, %t, want %s, true", test.text, prefix, ok, test.want)
			}
		})
	}

	invalid := []struct {
		name, text string
	}{
		{name: "empty", text: ""},
		{name: "no prefix length", text: "10.1.0.0"},
		{name: "host bits set", text: "10.1.0.1/24"},
		{name: "prefix length over 32", text: "10.1.0.0/33"},
		{name: "prefix length with a leading zero", text: "10.1.0.0/024"},
		{name: "prefix length with a plus sign", text: "10.1.0.0/+24"},
		{name: "negative zero prefix length", text: "10.1.0.0/-0"},
		{name: "octet with a leading zero", text: "10.01.0.0/16"},
		{name: "leading space", text: " 10.1.0.0/24"},
		{name: "trailing space", text: "10.1.0.0/24 "},
		{name: "IPv6", text: "2001:db8::/64"},
		{name: "IPv4-mapped IPv6", text: "::ffff:10.1.0.0/120"},
		{name: "empty prefix length", text: "10.1.0.0/"},
		{name: "no address", text: "/24"},
		{name: "three octets", text: "10.1.0/24"},
		{name: "octet over 255", text: "256.0.0.0/8"},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if prefix, ok := builder.ParseSubnet(test.text); ok {
				t.Errorf("ParseSubnet(%q) = %v, true, want no subnet", test.text, prefix)
			}
		})
	}
}

func TestIsSubnetHost(t *testing.T) {
	tests := []struct {
		subnet, addr string
		want         bool
	}{
		{"10.1.0.0/24", "10.1.0.1", true},
		{"10.1.0.0/24", "10.1.0.254", true},
		{"10.1.0.0/24", "10.1.0.0", false},
		{"10.1.0.0/24", "10.1.0.255", false},
		{"10.1.0.0/24", "10.1.1.1", false},
		{"10.1.0.0/30", "10.1.0.1", true},
		{"10.1.0.0/30", "10.1.0.3", false},
		{"192.0.2.4/31", "192.0.2.4", true},
		{"192.0.2.4/31", "192.0.2.5", true},
		{"192.0.2.4/31", "192.0.2.6", false},
		{"192.0.2.7/32", "192.0.2.7", true},
		{"192.0.2.7/32", "192.0.2.6", false},
		{"0.0.0.0/0", "0.0.0.0", false},
		{"0.0.0.0/0", "255.255.255.255", false},
		{"0.0.0.0/0", "8.8.8.8", true},
	}

	for _, test := range tests {
		t.Run(test.addr+" in "+test.subnet, func(t *testing.T) {
			subnet, ok := builder.ParseSubnet(test.subnet)
			if !ok {
				t.Fatalf("ParseSubnet(%q) = _, false, want a subnet", test.subnet)
			}

			if got := builder.IsSubnetHost(subnet, netip.MustParseAddr(test.addr)); got != test.want {
				t.Errorf("IsSubnetHost(%s, %s) = %t, want %t", test.subnet, test.addr, got, test.want)
			}
		})
	}
}

// The subnet and the gateway of a network are the Builder's own: a publish
// writes neither, and they change nothing that it writes.
func TestToTopologyOmitsNetworkAddressing(t *testing.T) {
	plain := loadDocumentFixture(t, "document.json")
	addressed := loadDocumentFixture(t, "document.json")

	for i := range addressed.Networks {
		addressed.Networks[i].Subnet = "192.0.2.0/24"
		addressed.Networks[i].Gateway = "192.0.2.254"
	}

	if err := addressed.Validate(); err != nil {
		t.Errorf("Validate() of the addressed document = %v, want nil", err)
	}

	want, err := plain.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology() of the plain document: %v", err)
	}

	got, err := addressed.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology() of the addressed document: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ToTopology() of the addressed document mismatch (-want +got):\n%s", diff)
	}

	config, _, err := addressed.ToTopologyConfig("addressed")
	if err != nil {
		t.Fatalf("ToTopologyConfig(addressed): %v", err)
	}

	encoded := asJSON(t, config)

	for _, text := range []string{"192.0.2.", `"subnet"`} {
		if strings.Contains(encoded, text) {
			t.Errorf("ToTopologyConfig(addressed) = %s, want no %s", encoded, text)
		}
	}
}
