package vm_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"inet.af/netaddr"

	"phenix/api/vm"
)

// searchFixture is a search index of six nodes: four IPv4 addresses, one IPv6 address, and one
// address that is not an IP address.
func searchFixture() vm.TopologySearch {
	return vm.TopologySearch{
		Hostname:   map[string]int{"web": 2},
		Disk:       map[string][]int{"/phenix/images/a.qc2": {0, 2}},
		Type:       map[string][]int{"VirtualMachine": {0, 1, 2}},
		OSType:     map[string][]int{"windows": {1}},
		Label:      map[string][]int{"site=a": {0}},
		Annotation: map[string][]int{"owner=b": {1}},
		VLAN:       map[string][]int{"EXP_1": {0, 1}},
		IP: map[string][]int{
			"10.0.0.3":   {3},
			"10.0.0.1":   {1},
			"10.0.1.2":   {2},
			"not-an-ip":  {4},
			"10.0.0.200": {0},
			"fd00::1":    {5},
		},
	}
}

func TestTopologySearchNodes(t *testing.T) {
	tests := []struct {
		name, term, value string
		want              []int
	}{
		{"hostname", "hostname", "web", []int{2}},
		{"unknown hostname", "hostname", "db", nil},
		{"term case does not matter", "HostName", "web", []int{2}},
		{"disk", "disk", "/phenix/images/a.qc2", []int{0, 2}},
		{"node type", "node-type", "VirtualMachine", []int{0, 1, 2}},
		{"os type", "os-type", "windows", []int{1}},
		{"label", "label", "site=a", []int{0}},
		{"annotation", "annotation", "owner=b", []int{1}},
		{"vlan", "vlan", "EXP_1", []int{0, 1}},
		{"one address", "ip", "10.0.0.1", []int{1}},
		{"address not indexed", "ip", "10.9.9.9", nil},
		{"prefix matches its addresses in order", "ip", "10.0.0.0/24", []int{0, 1, 3}},
		{"prefix of one address", "ip", "10.0.1.2/32", []int{2}},
		{"prefix with no addresses", "ip", "192.168.0.0/16", nil},
		{"all IPv4 addresses", "ip", "0.0.0.0/0", []int{0, 1, 2, 3}},
		{"all IPv6 addresses", "ip", "::/0", []int{5}},
		{"IPv6 prefix", "ip", "fd00::/64", []int{5}},
		{"value that is not an address is a key", "ip", "not-an-ip", []int{4}},
		{"unknown term", "mac", "00:00", nil},
	}

	search := searchFixture()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, search.Nodes(tt.term, tt.value)); diff != "" {
				t.Errorf("Nodes(%q, %q) mismatch (-want +got):\n%s", tt.term, tt.value, diff)
			}
		})
	}
}

// An ip search result is sorted and has only indexed nodes. A prefix never matches a value that is
// not an address, and its result is in the result of each wider prefix of the same address family.
// A prefix of one address gives the same result as that address. Other values are index keys.
func FuzzTopologySearchNodesIP(f *testing.F) {
	for _, seed := range []string{
		"10.0.0.0/24", "10.0.0.1", "10.0.0.1/32", "::/0", "0.0.0.0/0", "fd00::1/128", "fd00::/8",
		"::ffff:10.0.0.0/104", "10.0.0.1/33", "", "not-an-ip",
	} {
		f.Add(seed)
	}

	search := searchFixture()
	indexed := map[int]bool{}

	for _, nodes := range search.IP {
		for _, n := range nodes {
			indexed[n] = true
		}
	}

	f.Fuzz(func(t *testing.T, value string) {
		got := search.Nodes("ip", value)

		if !slices.IsSorted(got) {
			t.Errorf("Nodes(ip, %q) = %v, want sorted", value, got)
		}

		for _, n := range got {
			if !indexed[n] {
				t.Errorf("Nodes(ip, %q) = %v, want only indexed nodes; %d is not indexed", value, got, n)
			}
		}

		prefix, err := netaddr.ParseIPPrefix(value)
		if err != nil {
			if diff := cmp.Diff(search.IP[value], got); diff != "" {
				t.Errorf("Nodes(ip, %q) mismatch (-want +got):\n%s", value, diff)
			}

			return
		}

		if slices.Contains(got, 4) {
			t.Errorf("Nodes(ip, %q) = %v, want no node of the value that is not an address", value, got)
		}

		if prefix.IsSingleIP() {
			single := prefix.IP().String()
			if diff := cmp.Diff(search.Nodes("ip", single), got); diff != "" {
				t.Errorf("Nodes(ip, %q) mismatch with Nodes(ip, %q) (-want +got):\n%s", value, single, diff)
			}
		}

		for bits := int(prefix.Bits()) - 1; bits >= 0; bits-- {
			wider := netaddr.IPPrefixFrom(prefix.IP(), uint8(bits)).String()
			widerNodes := search.Nodes("ip", wider)

			for _, n := range got {
				if !slices.Contains(widerNodes, n) {
					t.Fatalf("Nodes(ip, %q) has %d, want it in Nodes(ip, %q) = %v", value, n, wider, widerNodes)
				}
			}
		}
	})
}
