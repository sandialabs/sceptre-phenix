package util

import (
	"fmt"
	"net"

	"golang.org/x/exp/slices"
	"inet.af/netaddr"
)

var private []*net.IPNet

func init() {
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

func UnusedSubnet(start netaddr.IPPrefix, used []netaddr.IPPrefix) (netaddr.IPPrefix, error) {
	var (
		subnet = start
		err    error
	)

	for {
		if slices.Contains(used, subnet) {
			subnet, err = subnet.Range().To().Next().Prefix(start.Bits())
			if err != nil {
				return netaddr.IPPrefix{}, fmt.Errorf("getting next subnet: %w", err)
			}

			continue
		}

		return subnet, nil
	}
}
