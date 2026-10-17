package builder_test

import (
	"net/netip"
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestParseSubnet(t *testing.T) {
	valid := map[string]string{
		"10.1.0.0/24":      "10.1.0.0/24",
		"0.0.0.0/0":        "0.0.0.0/0",
		"192.0.2.4/31":     "192.0.2.4/31",
		"192.0.2.7/32":     "192.0.2.7/32",
		"172.16.0.0/12":    "172.16.0.0/12",
		"255.255.255.0/24": "255.255.255.0/24",
	}

	for text, want := range valid {
		prefix, ok := builder.ParseSubnet(text)
		if !ok || prefix.String() != want {
			t.Errorf("ParseSubnet(%q) = %v, %t; want %s", text, prefix, ok, want)
		}
	}

	invalid := []string{
		"", "10.1.0.0", "10.1.0.1/24", "10.1.0.0/33", "10.1.0.0/024", "10.1.0.0/+24",
		"10.1.0.0/-0", "10.01.0.0/16", " 10.1.0.0/24", "10.1.0.0/24 ", "2001:db8::/64",
		"::ffff:10.1.0.0/120", "10.1.0.0/", "/24", "10.1.0/24", "256.0.0.0/8",
	}

	for _, text := range invalid {
		if prefix, ok := builder.ParseSubnet(text); ok {
			t.Errorf("ParseSubnet(%q) = %v; want no subnet", text, prefix)
		}
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
		subnet, ok := builder.ParseSubnet(test.subnet)
		if !ok {
			t.Fatalf("ParseSubnet(%q) failed", test.subnet)
		}

		if got := builder.IsSubnetHost(subnet, netip.MustParseAddr(test.addr)); got != test.want {
			t.Errorf("IsSubnetHost(%s, %s) = %t; want %t", test.subnet, test.addr, got, test.want)
		}
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
		t.Fatalf("Validate: %v", err)
	}

	want, err := plain.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	got, err := addressed.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology: %v", err)
	}

	if asJSON(t, got) != asJSON(t, want) {
		t.Fatalf("the subnet or the gateway changed the topology:\nwant: %s\ngot:  %s", asJSON(t, want), asJSON(t, got))
	}

	config, _, err := addressed.ToTopologyConfig("addressed")
	if err != nil {
		t.Fatalf("ToTopologyConfig: %v", err)
	}

	encoded := asJSON(t, config)

	for _, text := range []string{"192.0.2.", `"subnet"`} {
		if strings.Contains(encoded, text) {
			t.Fatalf("the topology holds %s: %s", text, encoded)
		}
	}
}
