package cmd

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

const (
	proxyEnvName = "proxy-env"

	// Phrases in the details of rows, which their hints look for.
	proxyCoverPhrase = "does not cover: "
)

// proxyVars are the variables that name a proxy, in both cases, as Go, Python and other programs read them.
//
//nolint:gochecknoglobals // constant list
var proxyVars = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}

// proxyTarget is a host that requests for phenix ui go to, on the port of phenix ui.
type proxyTarget struct {
	host, port string
}

// proxyEnvCheck warns when a proxy variable is set and a set NO_PROXY or no_proxy does not cover each host that
// requests for phenix ui go to. It names the variables, never their values, because a proxy URL can hold a password.
func proxyEnvCheck(in *doctorInput) doctorCheck {
	var set []string

	for _, name := range proxyVars {
		if value, ok := in.env.lookup(name); ok && value != "" {
			set = append(set, name)
		}
	}

	if len(set) == 0 {
		return passCheck("no HTTP_PROXY, HTTPS_PROXY, http_proxy or https_proxy variable is set")
	}

	targets := proxyTargets(in)
	names := make([]string, 0, len(targets))

	for _, t := range targets {
		names = append(names, t.host)
	}

	prefix := joinAnd(set) + " " + isAre(len(set)) + " set"

	// Go reads NO_PROXY first and other programs read no_proxy first, so each set variable has readers
	var (
		parts   []string
		checked []string
	)

	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		list, ok := in.env.lookup(name)
		if !ok || list == "" {
			continue
		}

		checked = append(checked, name)

		var missing []string

		for _, t := range targets {
			if !noProxyCovers(list, t.host, t.port) {
				missing = append(missing, t.host)
			}
		}

		if len(missing) > 0 {
			parts = append(parts, name+" "+proxyCoverPhrase+strings.Join(missing, ", "))
		}
	}

	switch {
	case len(checked) == 0:
		return warnCheck(fmt.Sprintf("%s, and no NO_PROXY or no_proxy is set, so it %s%s",
			prefix, proxyCoverPhrase, strings.Join(names, ", ")))
	case len(parts) > 0:
		return warnCheck(prefix + ". " + strings.Join(parts, ". "))
	}

	return passCheck(fmt.Sprintf("%s, and %s %s %s", prefix, joinAnd(checked), coverVerb(len(checked)),
		strings.Join(names, ", ")))
}

// proxyTargets are the hosts that requests for phenix ui on this host go to: localhost, its loopback addresses, the
// host name, and the host of ui.listen-endpoint, each on the port of phenix ui.
func proxyTargets(in *doctorInput) []proxyTarget {
	endpoint := in.text("ui.listen-endpoint")
	host, port, err := net.SplitHostPort(endpoint)

	switch {
	case err != nil || port == "":
		port = "80"
		if in.text("ui.tls-key") != "" && in.text("ui.tls-cert") != "" {
			port = "443"
		}
	case in.env.lookupPort != nil:
		if _, err := strconv.Atoi(port); err != nil {
			if n, err := in.env.lookupPort(port); err == nil {
				port = strconv.Itoa(n)
			}
		}
	}

	hosts := []string{"localhost", "127.0.0.1", "::1"}

	if in.hostname != nil {
		if name, err := in.hostname(); err == nil && name != "" {
			hosts = append(hosts, strings.ToLower(name))
		}
	}

	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		hosts = append(hosts, strings.ToLower(host))
	}

	targets := make([]proxyTarget, 0, len(hosts))
	for _, h := range slices.Compact(hosts) {
		if !slices.ContainsFunc(targets, func(t proxyTarget) bool { return t.host == h }) {
			targets = append(targets, proxyTarget{host: h, port: port})
		}
	}

	return targets
}

// noProxyCovers reports whether the NO_PROXY list covers host on port, as Go's net/http reads it: "*", a CIDR range,
// an IP address, a domain that also covers its subdomains, ".domain" or "*.domain" for the subdomains only, and each
// of these with a port. Go also never sends localhost and loopback addresses to a proxy, but other programs can.
func noProxyCovers(list, host, port string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	ip := net.ParseIP(host)

	for entry := range strings.SplitSeq(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}

		if entry == "*" {
			return true
		}

		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip != nil && cidr.Contains(ip) {
				return true
			}

			continue
		}

		if noProxyEntryCovers(entry, host, port, ip) {
			return true
		}
	}

	return false
}

// noProxyEntryCovers reports whether the NO_PROXY entry, which is not "*" or a CIDR range, covers host on port.
func noProxyEntryCovers(entry, host, port string, ip net.IP) bool {
	phost, pport, err := net.SplitHostPort(entry)
	if err != nil {
		phost, pport = entry, ""
	} else if strings.HasPrefix(phost, "[") && strings.HasSuffix(phost, "]") {
		phost = phost[1 : len(phost)-1]
	}

	if phost == "" {
		return false
	}

	portOK := pport == "" || pport == port

	if pip := net.ParseIP(phost); pip != nil {
		return ip != nil && pip.Equal(ip) && portOK
	}

	phost = strings.TrimPrefix(phost, "*")
	matchHost := !strings.HasPrefix(phost, ".")

	if matchHost {
		phost = "." + phost
	}

	return (strings.HasSuffix(host, phost) || matchHost && host == phost[1:]) && portOK
}

// proxyEnvHint is what to do about a proxy-env row that warns: add the hosts it names to both variables.
func proxyEnvHint(c doctorCheck) string {
	var hosts []string

	for part := range strings.SplitSeq(c.Detail, ". ") {
		if _, list, ok := strings.Cut(part, proxyCoverPhrase); ok {
			for h := range strings.SplitSeq(list, ", ") {
				if !slices.Contains(hosts, h) {
					hosts = append(hosts, h)
				}
			}
		}
	}

	if len(hosts) == 0 {
		return "set NO_PROXY and no_proxy to the hosts of phenix ui, or unset the proxy variables"
	}

	return fmt.Sprintf("add %s to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix and its apps",
		strings.Join(hosts, ","))
}

// joinAnd joins items with commas and a last "and".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// isAre is "is" for one item and "are" for more.
func isAre(n int) string {
	if n == 1 {
		return "is"
	}

	return "are"
}

// coverVerb is "covers" for one subject and "cover" for more.
func coverVerb(n int) string {
	if n == 1 {
		return "covers"
	}

	return "cover"
}
