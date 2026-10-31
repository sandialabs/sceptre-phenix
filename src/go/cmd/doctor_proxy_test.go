package cmd

import (
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

// noProxyCovers reads the NO_PROXY forms as Go's net/http does.
func TestNoProxyCovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		list, host, port string
		want             bool
	}{
		{"", "localhost", "80", false},
		{"*", "anything.example", "80", true},
		{"example.com", "example.com", "80", true},
		{"example.com", "ui.example.com", "80", true},
		{".example.com", "example.com", "80", false},
		{".example.com", "ui.example.com", "80", true},
		{"*.example.com", "ui.example.com", "80", true},
		{"*.example.com", "example.com", "80", false},
		{"example.com:3000", "example.com", "3000", true},
		{"example.com:3000", "example.com", "80", false},
		{"10.0.0.0/8", "10.1.2.3", "80", true},
		{"10.0.0.0/8", "11.1.2.3", "80", false},
		{"::1", "::1", "80", true},
		{"[::1]:3000", "::1", "3000", true},
		{"[::1]:3000", "::1", "80", false},
		{"127.0.0.1", "localhost", "80", false},
		{":3000", "localhost", "3000", false},
	}

	for _, tc := range tests {
		if got := noProxyCovers(tc.list, tc.host, tc.port); got != tc.want {
			t.Errorf("noProxyCovers(%q, %q, %q) = %t, want %t", tc.list, tc.host, tc.port, got, tc.want)
		}
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
