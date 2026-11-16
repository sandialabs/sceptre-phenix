package cmd

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// proxy-env passes when no proxy variable is set, and otherwise warns when a set NO_PROXY or no_proxy does not cover
// localhost, the loopback addresses, the host name and the host of ui.listen-endpoint. It names variables, never values.
func TestProxyEnvCheck(t *testing.T) {
	t.Parallel()

	const proxy = "http://alice:Secret1@proxy.example:3128"

	all := "localhost, 127.0.0.1, ::1, doctor-test-host"

	tests := map[string]struct {
		vars   map[string]string
		listen string
		want   doctorCheck
		hint   string
	}{
		"no proxy": {want: passCheck("no HTTP_PROXY, HTTPS_PROXY, http_proxy or https_proxy variable is set")},
		"empty proxy": {
			vars: map[string]string{"HTTP_PROXY": ""},
			want: passCheck("no HTTP_PROXY, HTTPS_PROXY, http_proxy or https_proxy variable is set"),
		},
		"no NO_PROXY": {
			vars: map[string]string{"HTTPS_PROXY": proxy},
			want: warnCheck("HTTPS_PROXY is set, and no NO_PROXY or no_proxy is set, so it does not cover: " + all),
			hint: "add localhost,127.0.0.1,::1,doctor-test-host to NO_PROXY and to no_proxy, separated by commas, " +
				"in the environment of phenix and its apps",
		},
		"covered": {
			vars: map[string]string{
				"http_proxy": proxy, "HTTPS_PROXY": proxy,
				"NO_PROXY": " LOCALHOST, 127.0.0.0/8 ,::1/128,.example.com,doctor-test-host",
			},
			want: passCheck("http_proxy and HTTPS_PROXY are set, and NO_PROXY covers " + all),
		},
		"star": {
			vars: map[string]string{"https_proxy": proxy, "no_proxy": "*"},
			want: passCheck("https_proxy is set, and no_proxy covers " + all),
		},
		"both lists cover": {
			vars: map[string]string{"HTTP_PROXY": proxy, "NO_PROXY": "*", "no_proxy": "localhost,127.0.0.0/8,::1,doctor-test-host"},
			want: passCheck("HTTP_PROXY is set, and NO_PROXY and no_proxy cover " + all),
		},
		"neither list covers": {
			vars: map[string]string{
				"HTTP_PROXY": proxy, "https_proxy": proxy, "HTTPS_PROXY": proxy,
				"NO_PROXY": "localhost,127.0.0.1,::1", "no_proxy": "doctor-test-host",
			},
			want: warnCheck("HTTP_PROXY, HTTPS_PROXY and https_proxy are set. NO_PROXY does not cover: doctor-test-host. " +
				"no_proxy does not cover: localhost, 127.0.0.1, ::1"),
			hint: "add doctor-test-host,localhost,127.0.0.1,::1 to NO_PROXY and to no_proxy, separated by commas, in the " +
				"environment of phenix and its apps",
		},
		"empty NO_PROXY": {
			vars: map[string]string{"HTTP_PROXY": proxy, "NO_PROXY": ""},
			want: warnCheck("HTTP_PROXY is set, and no NO_PROXY or no_proxy is set, so it does not cover: " + all),
			hint: "add localhost,127.0.0.1,::1,doctor-test-host to NO_PROXY and to no_proxy, separated by commas, " +
				"in the environment of phenix and its apps",
		},
		"each variable has its readers": {
			vars: map[string]string{
				"HTTP_PROXY": proxy, "NO_PROXY": "localhost,127.0.0.1,::1,doctor-test-host",
				"no_proxy": "localhost,127.0.0.1,::1",
			},
			want: warnCheck("HTTP_PROXY is set. no_proxy does not cover: doctor-test-host"),
			hint: "add doctor-test-host to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix " +
				"and its apps",
		},
		"ports": {
			vars: map[string]string{
				"HTTP_PROXY": proxy, "NO_PROXY": "localhost:3000,127.0.0.1:8080,[::1]:3000,doctor-test-host:3000",
			},
			want: warnCheck("HTTP_PROXY is set. NO_PROXY does not cover: 127.0.0.1"),
			hint: "add 127.0.0.1 to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix and its apps",
		},
		"listen host": {
			vars:   map[string]string{"HTTP_PROXY": proxy, "NO_PROXY": "localhost,127.0.0.1,::1,doctor-test-host"},
			listen: "ui.lab.example:443",
			want:   warnCheck("HTTP_PROXY is set. NO_PROXY does not cover: ui.lab.example"),
			hint: "add ui.lab.example to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix " +
				"and its apps",
		},
		"subdomains only": {
			vars: map[string]string{
				"HTTP_PROXY": proxy, "NO_PROXY": "localhost,127.0.0.1,::1,doctor-test-host,.lab.example",
			},
			listen: "lab.example:443",
			want:   warnCheck("HTTP_PROXY is set. NO_PROXY does not cover: lab.example"),
			hint:   "add lab.example to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix and its apps",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			settings := map[string]string{}
			if tc.listen != "" {
				settings["ui.listen-endpoint"] = tc.listen
			}

			in := fakeDoctorInput(newDoctorTestDir(t), settings)
			in.env.lookup = func(name string) (string, bool) {
				v, ok := tc.vars[name]

				return v, ok
			}

			got := proxyEnvCheck(in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("proxyEnvCheck() mismatch (-want +got):\n%s", diff)
			}

			for _, secret := range []string{"Secret1", "alice", "proxy.example"} {
				if strings.Contains(got.Detail, secret) {
					t.Errorf("proxyEnvCheck() = %q, which shows a proxy value", got.Detail)
				}
			}

			if hint := doctorHintOf(got, proxyEnvHint); hint != tc.hint {
				t.Errorf("proxyEnvHint() = %q, want %q", hint, tc.hint)
			}
		})
	}
}

// doctorProxyCoverCase is one NO_PROXY list, host and port, and whether the list covers the host on the port.
type doctorProxyCoverCase struct {
	name, list, host, port string
	want                   bool
}

// doctorProxyCoverCases are the NO_PROXY forms that Go's net/http reads, which the table and fuzz tests share.
func doctorProxyCoverCases() []doctorProxyCoverCase {
	return []doctorProxyCoverCase{
		{"empty list", "", "localhost", "80", false},
		{"only commas and spaces", " , ,", "example.com", "80", false},
		{"star", "*", "anything.example", "80", true},
		{"star among others", "a.example, *", "b.example", "80", true},
		{"domain", "example.com", "example.com", "80", true},
		{"domain covers subdomains", "example.com", "ui.example.com", "80", true},
		{"domain is not a suffix of a word", "example.com", "badexample.com", "80", false},
		{"case and spaces", "  EXAMPLE.com ", "Ui.Example.COM", "80", true},
		{"dot domain", ".example.com", "example.com", "80", false},
		{"dot domain covers subdomains", ".example.com", "ui.example.com", "80", true},
		{"star dot domain", "*.example.com", "ui.example.com", "80", true},
		{"star dot domain is not the domain", "*.example.com", "example.com", "80", false},
		{"star dot domain with a port", "*.example.com:3000", "ui.example.com", "3000", true},
		{"domain with its port", "example.com:3000", "example.com", "3000", true},
		{"domain with another port", "example.com:3000", "example.com", "80", false},
		{"CIDR", "10.0.0.0/8", "10.1.2.3", "80", true},
		{"CIDR, another network", "10.0.0.0/8", "11.1.2.3", "80", false},
		{"CIDR, a host name", "10.0.0.0/8", "example.com", "80", false},
		{"IPv6 CIDR", "2001:db8::/32", "2001:db8::5", "80", true},
		{"IPv6 address", "::1", "::1", "80", true},
		{"IPv6 address with its port", "[::1]:3000", "::1", "3000", true},
		{"IPv6 address with another port", "[::1]:3000", "::1", "80", false},
		{"IPv4 address with its port", "10.1.2.3:443", "10.1.2.3", "443", true},
		{"address is not a name", "127.0.0.1", "localhost", "80", false},
		{"address does not cover another", "10.1.2.3", "10.1.2.4", "80", false},
		{"port only", ":3000", "localhost", "3000", false},
		{"second entry", "a.example,b.example", "b.example", "80", true},
	}
}

// noProxyCovers reads the NO_PROXY forms as Go's net/http does.
func TestNoProxyCovers(t *testing.T) {
	t.Parallel()

	for _, tc := range doctorProxyCoverCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := noProxyCovers(tc.list, tc.host, tc.port); got != tc.want {
				t.Errorf("noProxyCovers(%q, %q, %q) = %t, want %t", tc.list, tc.host, tc.port, got, tc.want)
			}
		})
	}
}

// doctorProxyGoDifferenceCases are NO_PROXY forms that a simple reading gets wrong: Go's net/http never matches an IP
// address against a domain entry, trims only a "*." prefix, and reads a non-ASCII entry or host as punycode.
func doctorProxyGoDifferenceCases() []doctorProxyCoverCase {
	return []doctorProxyCoverCase{
		{"domain entry, IP host", "0.0.5", "10.0.0.5", "80", false},
		{"domain entry, IPv4-mapped host", "0.0.5", "::ffff:10.0.0.5", "80", false},
		{"star without a dot", "*example.com", "example.com", "80", false},
		{"star without a dot, subdomain", "*example.com", "ui.example.com", "80", false},
		{"star with only a port", "*:80", "ui.example.", "80", false},
		{"non-ASCII entry", "b\u00fccher.example", "xn--bcher-kva.example", "80", true},
		{"non-ASCII entry, subdomain", ".b\u00fccher.example", "ui.xn--bcher-kva.example", "80", true},
		{"non-ASCII host", "xn--bcher-kva.example", "b\u00fccher.example", "80", true},
	}
}

// noProxyCovers agrees with Go's net/http on the forms that a simple reading of NO_PROXY gets wrong.
func TestNoProxyCoversGoDifferences(t *testing.T) {
	t.Parallel()

	for _, tc := range doctorProxyGoDifferenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := noProxyCovers(tc.list, tc.host, tc.port); got != tc.want {
				t.Errorf("noProxyCovers(%q, %q, %q) = %t, want %t", tc.list, tc.host, tc.port, got, tc.want)
			}
		})
	}
}

// Whatever the list, "*" covers every host, the host itself covers it, and more entries never cover less.
func TestNoProxyCoversProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		entry := rapid.StringMatching(`[a-z0-9.*:/\[\] -]{0,20}`)
		list := strings.Join(rapid.SliceOfN(entry, 0, 5).Draw(t, "entries"), ",")
		host := rapid.OneOf(
			rapid.StringMatching(`[a-z][a-z0-9-]{0,8}(\.[a-z]{2,5}){0,2}`),
			rapid.SampledFrom([]string{"127.0.0.1", "::1", "10.1.2.3", "localhost"}),
		).Draw(t, "host")
		port := rapid.SampledFrom([]string{"80", "443", "3000"}).Draw(t, "port")
		extra := entry.Draw(t, "extra")

		covered := noProxyCovers(list, host, port)

		if !noProxyCovers(list+",*", host, port) {
			t.Fatalf("noProxyCovers(%q, %q) = false with *", list+",*", host)
		}

		if !noProxyCovers(list+","+host, host, port) {
			t.Fatalf("noProxyCovers(%q, %q) = false with the host itself", list+","+host, host)
		}

		if covered && !noProxyCovers(list+","+extra, host, port) {
			t.Fatalf("noProxyCovers(%q, %q) = false, but %q covers it", list+","+extra, host, list)
		}
	})
}

// proxyTargets are localhost, the loopback addresses, the host name and the host of ui.listen-endpoint, once each and
// in lower case, on the port of phenix ui: the endpoint's port or service, else 443 with TLS and 80 without.
func TestProxyTargets(t *testing.T) {
	t.Parallel()

	targets := func(port string, hosts ...string) []proxyTarget {
		out := make([]proxyTarget, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, proxyTarget{host: h, port: port})
		}

		return out
	}

	loopback := []string{"localhost", "127.0.0.1", "::1"}
	withHost := append(slices.Clone(loopback), "doctor-test-host")

	tests := map[string]struct {
		settings map[string]string
		hostname func() (string, error) // nil keeps the doctor-test-host of fakeDoctorInput
		noHost   bool                   // in.hostname is nil
		noLookup bool                   // in.env.lookupPort is nil
		want     []proxyTarget
	}{
		"all addresses":      {settings: map[string]string{"ui.listen-endpoint": "0.0.0.0:3000"}, want: targets("3000", withHost...)},
		"all IPv6 addresses": {settings: map[string]string{"ui.listen-endpoint": "[::]:3000"}, want: targets("3000", withHost...)},
		"loopback address":   {settings: map[string]string{"ui.listen-endpoint": "127.0.0.1:3000"}, want: targets("3000", withHost...)},
		"the host name again": {
			settings: map[string]string{"ui.listen-endpoint": "Doctor-Test-Host:3000"},
			want:     targets("3000", withHost...),
		},
		"listen host": {
			settings: map[string]string{"ui.listen-endpoint": "UI.Lab.Example:8443"},
			want:     targets("8443", append(slices.Clone(withHost), "ui.lab.example")...),
		},
		"listen address": {
			settings: map[string]string{"ui.listen-endpoint": "10.1.2.3:3000"},
			want:     targets("3000", append(slices.Clone(withHost), "10.1.2.3")...),
		},
		"no endpoint":       {settings: map[string]string{"ui.listen-endpoint": ""}, want: targets("80", withHost...)},
		"not host and port": {settings: map[string]string{"ui.listen-endpoint": "3000"}, want: targets("80", withHost...)},
		"no endpoint, TLS": {
			settings: map[string]string{"ui.listen-endpoint": "", "ui.tls-key": "k.pem", "ui.tls-cert": "c.pem"},
			want:     targets("443", withHost...),
		},
		"no endpoint, TLS key only": {
			settings: map[string]string{"ui.listen-endpoint": "", "ui.tls-key": "k.pem"},
			want:     targets("80", withHost...),
		},
		"empty port": {
			settings: map[string]string{"ui.listen-endpoint": "ui.example:"},
			want:     targets("80", append(slices.Clone(withHost), "ui.example")...),
		},
		"service name": {
			settings: map[string]string{"ui.listen-endpoint": ":http"},
			want:     targets("80", withHost...),
		},
		"unknown service name": {
			settings: map[string]string{"ui.listen-endpoint": ":nosuch"},
			want:     targets("nosuch", withHost...),
		},
		"no port lookup": {
			settings: map[string]string{"ui.listen-endpoint": ":http"}, noLookup: true,
			want: targets("http", withHost...),
		},
		"host name not known": {
			hostname: func() (string, error) { return "", errors.New("no host name") },
			want:     targets("3000", loopback...),
		},
		"empty host name": {
			hostname: func() (string, error) { return "", nil },
			want:     targets("3000", loopback...),
		},
		"no host name lookup": {noHost: true, want: targets("3000", loopback...)},
		"host name in capitals": {
			hostname: func() (string, error) { return "WS42", nil },
			want:     targets("3000", append(slices.Clone(loopback), "ws42")...),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(newDoctorTestDir(t), tc.settings)

			switch {
			case tc.noHost:
				in.hostname = nil
			case tc.hostname != nil:
				in.hostname = tc.hostname
			}

			if tc.noLookup {
				in.env.lookupPort = nil
			}

			if diff := cmp.Diff(tc.want, proxyTargets(in), cmp.AllowUnexported(proxyTarget{})); diff != "" {
				t.Errorf("proxyTargets() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The hint names each host that a part of the detail does not cover, once, and asks for both lists when it names none.
func TestProxyEnvHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		detail string
		want   string
	}{
		"no host named": {
			detail: "HTTP_PROXY is set",
			want:   "set NO_PROXY and no_proxy to the hosts of phenix ui, or unset the proxy variables",
		},
		"one part": {
			detail: "HTTP_PROXY is set. NO_PROXY " + proxyCoverPhrase + "a, b",
			want:   "add a,b to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix and its apps",
		},
		"hosts in both parts once": {
			detail: "HTTP_PROXY is set. NO_PROXY " + proxyCoverPhrase + "a, b. no_proxy " + proxyCoverPhrase + "b, c",
			want:   "add a,b,c to NO_PROXY and to no_proxy, separated by commas, in the environment of phenix and its apps",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := proxyEnvHint(warnCheck(tc.detail)); got != tc.want {
				t.Errorf("proxyEnvHint(%q) = %q, want %q", tc.detail, got, tc.want)
			}
		})
	}
}

// joinAnd joins with commas and a last "and", and isAre and coverVerb agree with one or more subjects.
func TestProxyWords(t *testing.T) {
	t.Parallel()

	joins := map[string]struct {
		items []string
		want  string
	}{
		"none":  {items: nil, want: ""},
		"one":   {items: []string{"a"}, want: "a"},
		"two":   {items: []string{"a", "b"}, want: "a and b"},
		"three": {items: []string{"a", "b", "c"}, want: "a, b and c"},
	}

	for name, tc := range joins {
		t.Run("joinAnd "+name, func(t *testing.T) {
			t.Parallel()

			if got := joinAnd(tc.items); got != tc.want {
				t.Errorf("joinAnd(%q) = %q, want %q", tc.items, got, tc.want)
			}
		})
	}

	verbs := map[int][2]string{0: {"are", "cover"}, 1: {"is", "covers"}, 2: {"are", "cover"}, 5: {"are", "cover"}}

	for n, want := range verbs {
		t.Run("verbs of "+strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()

			if got := [2]string{isAre(n), coverVerb(n)}; got != want {
				t.Errorf("isAre(%d), coverVerb(%d) = %q, want %q", n, n, got, want)
			}
		})
	}
}
