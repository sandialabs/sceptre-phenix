package util_test

import (
	"errors"
	"net"
	"testing"

	"inet.af/netaddr"

	"phenix/util"
)

func TestPrivateIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "127.0.0.1", want: true},
		{ip: "127.255.255.254", want: true},
		{ip: "10.0.0.1", want: true},
		{ip: "10.255.255.255", want: true},
		{ip: "11.0.0.1", want: false},
		{ip: "172.16.0.1", want: true},
		{ip: "172.31.255.255", want: true},
		{ip: "172.32.0.1", want: false},
		{ip: "172.15.255.255", want: false},
		{ip: "192.168.1.1", want: true},
		{ip: "192.169.0.1", want: false},
		{ip: "169.254.10.20", want: true},
		{ip: "224.0.0.251", want: true},
		{ip: "8.8.8.8", want: false},
		{ip: "::1", want: true},
		{ip: "fe80::1", want: true},
		{ip: "ff02::1", want: true},
		{ip: "fc00::1", want: true},
		{ip: "fd12:3456::1", want: true},
		{ip: "2001:db8::1", want: false},
		{ip: "::ffff:10.1.2.3", want: true},
		{ip: "::ffff:8.8.4.4", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			t.Parallel()

			if got := util.PrivateIP(net.ParseIP(tt.ip)); got != tt.want {
				t.Errorf("PrivateIP(%s) = %t, want %t", tt.ip, got, tt.want)
			}
		})
	}

	if util.PrivateIP(nil) {
		t.Error("PrivateIP(nil) = true, want false")
	}
}

func TestUnusedSubnet(t *testing.T) {
	t.Parallel()

	p := netaddr.MustParseIPPrefix

	tests := []struct {
		name  string
		start string
		used  []netaddr.IPPrefix
		want  string
	}{
		{name: "nothing used", start: "10.213.47.0/30", want: "10.213.47.0/30"},
		{
			name: "other subnets used", start: "10.213.47.0/30",
			used: []netaddr.IPPrefix{p("10.0.0.0/30"), p("10.213.47.4/30")}, want: "10.213.47.0/30",
		},
		{name: "start used", start: "10.213.47.0/30", used: []netaddr.IPPrefix{p("10.213.47.0/30")}, want: "10.213.47.4/30"},
		{
			name: "a run used, in any order", start: "10.213.47.0/30",
			used: []netaddr.IPPrefix{p("10.213.47.8/30"), p("10.213.47.0/30"), p("10.213.47.4/30")}, want: "10.213.47.12/30",
		},
		{name: "into the next octet", start: "10.0.0.252/30", used: []netaddr.IPPrefix{p("10.0.0.252/30")}, want: "10.0.1.0/30"},
		{name: "larger subnets", start: "192.168.0.0/24", used: []netaddr.IPPrefix{p("192.168.0.0/24")}, want: "192.168.1.0/24"},
		{name: "IPv6", start: "fd00::/64", used: []netaddr.IPPrefix{p("fd00::/64")}, want: "fd00:0:0:1::/64"},
		{name: "start with host bits", start: "10.0.0.1/30", used: []netaddr.IPPrefix{p("10.0.0.0/30")}, want: "10.0.0.4/30"},
		{name: "used with host bits", start: "10.0.0.0/30", used: []netaddr.IPPrefix{p("10.0.0.2/30")}, want: "10.0.0.4/30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := util.UnusedSubnet(p(tt.start), tt.used)
			if err != nil {
				t.Fatalf("UnusedSubnet(%s, %v) error = %v, want nil", tt.start, tt.used, err)
			}

			if got != p(tt.want) {
				t.Errorf("UnusedSubnet(%s, %v) = %s, want %s", tt.start, tt.used, got, tt.want)
			}
		})
	}
}

// UnusedSubnet returns ErrNoUnusedSubnet, and does not loop, when every subnet from start to the end of the address
// space is used. It refuses a start that is not a valid prefix.
func TestUnusedSubnetNone(t *testing.T) {
	t.Parallel()

	p := netaddr.MustParseIPPrefix

	tests := []struct {
		name  string
		start netaddr.IPPrefix
		used  []netaddr.IPPrefix
		want  error
	}{
		{
			name: "IPv4 used up", start: p("255.255.255.248/30"),
			used: []netaddr.IPPrefix{p("255.255.255.248/30"), p("255.255.255.252/30")}, want: util.ErrNoUnusedSubnet,
		},
		{
			name: "IPv4 used up and a zero prefix used", start: p("255.255.255.252/30"),
			used: []netaddr.IPPrefix{p("255.255.255.252/30"), {}}, want: util.ErrNoUnusedSubnet,
		},
		{
			name: "IPv6 used up", start: p("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00/120"),
			used: []netaddr.IPPrefix{p("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00/120")}, want: util.ErrNoUnusedSubnet,
		},
		{name: "zero start", start: netaddr.IPPrefix{}, used: []netaddr.IPPrefix{{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := util.UnusedSubnet(tt.start, tt.used)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Errorf("UnusedSubnet(%s, %v) = %s, %v, want error %v", tt.start, tt.used, got, err, tt.want)
			}
		})
	}
}
