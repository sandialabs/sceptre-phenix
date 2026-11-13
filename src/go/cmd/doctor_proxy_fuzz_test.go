package cmd

import (
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"golang.org/x/net/http/httpproxy"
)

// doctorProxyFuzzHost is a lower-case ASCII host name or IPv4 address, as phenix ui's hosts are.
var doctorProxyFuzzHost = regexp.MustCompile(`^[a-z0-9.-]+$`)

// doctorProxyGoCovers reports whether Go's net/http sends a request for host on port past the proxy under list, and
// false second when Go never sends such a host to a proxy.
func doctorProxyGoCovers(list, host, port string) (bool, bool) {
	if host == "localhost" {
		return false, false
	}

	addr, err := netip.ParseAddr(host)

	switch {
	case err == nil && (addr.Zone() != "" || addr.Unmap().IsLoopback()):
		return false, false
	case err != nil && !doctorProxyFuzzHost.MatchString(host):
		return false, false
	}

	cfg := httpproxy.Config{HTTPProxy: "http://proxy.invalid:3128", NoProxy: list}
	proxy, err := cfg.ProxyFunc()(&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)})

	return proxy == nil, err == nil
}

// noProxyCovers agrees with Go's net/http on any NO_PROXY list, for the hosts that Go can send to a proxy.
func FuzzNoProxyCovers(f *testing.F) {
	for _, tc := range append(doctorProxyCoverCases(), doctorProxyGoDifferenceCases()...) {
		port, _ := strconv.ParseUint(tc.port, 10, 16)
		f.Add(tc.list, tc.host, uint16(port))
	}

	f.Fuzz(func(t *testing.T, list, host string, portNumber uint16) {
		port := strconv.Itoa(int(portNumber))

		got := noProxyCovers(list, host, port)

		want, ok := doctorProxyGoCovers(list, host, port)
		if ok && got != want {
			t.Errorf("noProxyCovers(%q, %q, %s) = %t, Go's net/http gives %t", list, host, port, got, want)
		}

		if !noProxyCovers(list+",*", host, port) {
			t.Errorf("noProxyCovers(%q, %q, %s) = false with *", list+",*", host, port)
		}
	})
}
