package app_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"phenix/app"
	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// TestNTPAppSourceIPAddressDirect verifies that an explicit Address is returned
// directly without consulting the experiment topology.
func TestNTPAppSourceIPAddressDirect(t *testing.T) {
	s := app.NTPAppSource{Address: "192.168.1.1"}
	if got := s.IPAddress(nil); got != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1, got %s", got)
	}
}

// TestNTPAppSourceIPAddressMissingInterface verifies that an empty string is
// returned when Interface is not set (nothing to look up).
func TestNTPAppSourceIPAddressMissingInterface(t *testing.T) {
	s := app.NTPAppSource{Hostname: "server01"}
	if got := s.IPAddress(nil); got != "" {
		t.Fatalf("expected empty string, got %s", got)
	}
}

// TestNTPAppSourceIPAddressTopology verifies that IPAddress finds the address
// of the named interface on the named node, with a case-insensitive interface
// name, and gives an empty string for an unknown node or interface.
func TestNTPAppSourceIPAddressTopology(t *testing.T) {
	t.Parallel()

	node := &v1.Node{
		GeneralF: &v1.General{HostnameF: "server01"},
		NetworkF: &v1.Network{InterfacesF: []*v1.Interface{
			{NameF: "mgmt", AddressF: "172.16.0.1"},
			{NameF: "IF0", AddressF: "10.0.0.1"},
		}},
	}
	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "ntp-source-test"},
		Spec:     &v1.ExperimentSpec{TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{node}}},
	}

	tests := map[string]struct {
		source app.NTPAppSource
		want   string
	}{
		"match":             {source: app.NTPAppSource{Hostname: "server01", Interface: "IF0"}, want: "10.0.0.1"},
		"case-insensitive":  {source: app.NTPAppSource{Hostname: "server01", Interface: "if0"}, want: "10.0.0.1"},
		"unknown host":      {source: app.NTPAppSource{Hostname: "server02", Interface: "IF0"}},
		"unknown interface": {source: app.NTPAppSource{Hostname: "server01", Interface: "eth9"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.source.IPAddress(exp); got != tc.want {
				t.Errorf("IPAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNTPLinuxTemplateClient verifies that ntp_linux.tmpl, when given a server
// address, produces a client config pointing at that address.
func TestNTPLinuxTemplateClient(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("ntp_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "server 10.0.0.1 iburst prefer") {
		t.Fatalf("expected upstream server line in client config:\n%s", buf.String())
	}
}

// TestNTPLinuxTemplateServer verifies that ntp_linux.tmpl, when given no
// upstream address, produces a server config that falls back to the local clock.
func TestNTPLinuxTemplateServer(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("ntp_linux.tmpl", app.NTPTemplateData{Source: "", Server: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "server 127.127.1.1 iburst prefer") {
		t.Fatalf("expected local clock reference in server config:\n%s", buf.String())
	}
}

// TestNTPLinuxTemplateClientNoServe verifies that ntp_linux.tmpl client config
// includes noserve so the VM does not serve time to other hosts.
func TestNTPLinuxTemplateClientNoServe(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("ntp_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "noserve") {
		t.Fatalf("expected noserve in client restrict lines:\n%s", buf.String())
	}
}

// TestNTPLinuxTemplateServerServes verifies that ntp_linux.tmpl server config
// does not include noserve so the VM can serve time to NTP clients.
func TestNTPLinuxTemplateServerServes(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("ntp_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "noserve") {
		t.Fatalf("unexpected noserve in server restrict lines:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateClient verifies that chrony_linux.tmpl, when given a
// server address, produces a client config pointing at that address.
func TestChronyLinuxTemplateClient(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "server 10.0.0.1 iburst prefer") {
		t.Fatalf("expected upstream server line in client config:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateServer verifies that chrony_linux.tmpl, when given no
// upstream address, produces a server config that falls back to the local clock.
func TestChronyLinuxTemplateServer(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "", Server: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "local stratum") {
		t.Fatalf("expected local stratum reference in server config:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateClientNoAllowAll verifies that chrony_linux.tmpl
// client config does not include allow all, so the VM does not serve time.
func TestChronyLinuxTemplateClientNoAllowAll(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "allow all") {
		t.Fatalf("unexpected allow all in client config:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateServerAllowAll verifies that chrony_linux.tmpl
// server config includes allow all so the VM serves time to NTP clients.
func TestChronyLinuxTemplateServerAllowAll(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "allow all") {
		t.Fatalf("expected allow all in server config:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateClientMakestep verifies that chrony_linux.tmpl
// includes the makestep directive configured for delayed server connectivity.
func TestChronyLinuxTemplateClientMakestep(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "makestep 1.0 100") {
		t.Fatalf("expected makestep directive for delayed startup:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateClientMaxpoll verifies that chrony_linux.tmpl
// includes maxpoll on the server line to limit backoff after failures.
func TestChronyLinuxTemplateClientMaxpoll(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "maxpoll 6") {
		t.Fatalf("expected maxpoll directive on server line:\n%s", buf.String())
	}
}

// TestChronyLinuxTemplateServerNoUpstreamServer verifies that chrony_linux.tmpl
// does not emit a server directive when no upstream address is provided.
func TestChronyLinuxTemplateServerNoUpstreamServer(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("chrony_linux.tmpl", app.NTPTemplateData{Source: "", Server: true}, &buf); err != nil {
		t.Fatal(err)
	}

	// Check for a directive line (not a comment) starting with "server ".
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "server ") {
			t.Fatalf("unexpected server directive in local-clock config: %q", line)
		}
	}
}

// TestNTPLinuxTemplateClientBurst verifies that ntp_linux.tmpl gives the
// upstream server line iburst, prefer and maxpoll 6.
func TestNTPLinuxTemplateClientBurst(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("ntp_linux.tmpl", app.NTPTemplateData{Source: "10.0.0.1", Server: false}, &buf); err != nil {
		t.Fatal(err)
	}

	out := buf.String()

	if !slices.Contains(strings.Split(out, "\n"), "server 10.0.0.1 iburst prefer maxpoll 6") {
		t.Fatalf("expected the line %q:\n%s", "server 10.0.0.1 iburst prefer maxpoll 6", out)
	}
}

// TestSystemdTimesyncdTemplate verifies that systemd-timesyncd.tmpl renders
// the NTP server address into the correct [Time] section key.
func TestSystemdTimesyncdTemplate(t *testing.T) {
	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate(
		"systemd-timesyncd.tmpl",
		app.NTPTemplateData{Source: "10.0.0.1", Server: false},
		&buf,
	); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "NTP=10.0.0.1") {
		t.Fatalf("expected NTP= line in timesyncd config:\n%s", buf.String())
	}
}
