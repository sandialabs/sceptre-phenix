package util

import (
	"errors"
	"fmt"
	"net"
	"slices"

	"inet.af/netaddr"
)

var private []*net.IPNet //nolint:gochecknoglobals // global constant

// ErrNoUnusedSubnet is the error that UnusedSubnet returns when every subnet from its start to the end of the address
// space is used.
var ErrNoUnusedSubnet = errors.New("no unused subnet")

func init() { //nolint:gochecknoinits // private ip blocks
	for _, cidr := range []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // RFC3927 link-local
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local addr
	} {
		_, block, _ := net.ParseCIDR(cidr)
		private = append(private, block)
	}
}

func PrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	for _, block := range private {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// UnusedSubnet returns the first subnet of start's size, at or after start, that is not in used. It compares the
// subnets without their host bits.
func UnusedSubnet(start netaddr.IPPrefix, used []netaddr.IPPrefix) (netaddr.IPPrefix, error) {
	if !start.IsValid() {
		return netaddr.IPPrefix{}, fmt.Errorf("invalid start subnet %q", start)
	}

	subnet := start.Masked()

	for slices.ContainsFunc(used, func(u netaddr.IPPrefix) bool { return u.Masked() == subnet }) {
		next := subnet.Range().To().Next()
		if next.IsZero() {
			return netaddr.IPPrefix{}, fmt.Errorf("%w of size /%d at or after %s", ErrNoUnusedSubnet, start.Bits(), start.Masked())
		}

		subnet = netaddr.IPPrefixFrom(next, start.Bits())
	}

	return subnet, nil
}
