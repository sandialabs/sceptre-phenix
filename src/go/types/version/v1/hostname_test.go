package v1

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"phenix/util/plog"
)

func TestCheckHostnameKeywords(t *testing.T) {
	tests := []struct {
		hostname string
		osType   string
		wantErr  string
		wantWarn string
	}{
		{hostname: "all", wantErr: "hostname 'all' is reserved"},
		{hostname: "All", wantWarn: "hostname 'All' differs from the reserved name 'all' only by case"},
		{hostname: "ALL", wantWarn: "hostname 'ALL' differs"},
		{hostname: "42", wantErr: "hostname '42' is all digits"},
		{hostname: "007", wantErr: "hostname '007' is all digits"},
		{hostname: "phenix", osType: "windows", wantErr: "hostname 'phenix' can't be used for a Windows node"},
		{hostname: "Phenix", osType: "Windows", wantErr: "hostname 'Phenix' can't be used for a Windows node"},
		{hostname: "phenix", osType: "linux", wantWarn: "hostname 'phenix' matches 'phenix'"},
		{hostname: "PHENIX", osType: "vyos", wantWarn: "hostname 'PHENIX' matches 'phenix'"},
		{hostname: "phenix", wantWarn: "hostname 'phenix' matches 'phenix'"},
		{hostname: "phenix-web", osType: "windows"},
		{hostname: "all-sensors"},
		{hostname: "ball"},
		{hostname: "rtr-01"},
		{hostname: "1e5"},
		{hostname: "0x10"},
	}

	for _, tt := range tests {
		t.Run(tt.hostname+"/"+tt.osType, func(t *testing.T) {
			warning, err := checkHostnameKeywords(tt.hostname, tt.osType)

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}

			switch {
			case tt.wantWarn == "" && warning != "":
				t.Fatalf("expected no warning, got %q", warning)
			case !strings.Contains(warning, tt.wantWarn):
				t.Fatalf("warning %q does not contain %q", warning, tt.wantWarn)
			}
		})
	}
}

func TestTopologyInitReservedHostnames(t *testing.T) {
	node := func(hostname, osType string, external bool) *Node {
		n := &Node{
			GeneralF:  &General{HostnameF: hostname},
			HardwareF: &Hardware{OSTypeF: osType},
		}

		if external {
			n.ExternalF = &external
		}

		return n
	}

	tests := []struct {
		name     string
		node     *Node
		wantErr  bool
		wantWarn bool
	}{
		{name: "internal node named all fails", node: node("all", "linux", false), wantErr: true},
		{name: "all-digit internal node fails", node: node("42", "linux", false), wantErr: true},
		{name: "other casing of all warns", node: node("All", "linux", false), wantWarn: true},
		{name: "windows node named phenix fails", node: node("phenix", "windows", false), wantErr: true},
		{name: "linux node named phenix warns", node: node("phenix", "linux", false), wantWarn: true},
		{name: "external node named all is exempt", node: node("all", "", true)},
		{name: "ordinary hostname passes", node: node("all-sensors", "windows", false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer

			plog.AddHandler(t.Name(), slog.NewTextHandler(&logs, nil))
			t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

			topo := &TopologySpec{NodesF: []*Node{tt.node}}

			err := topo.Init("phenix")
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("Init() error = %v, want error: %t", err, tt.wantErr)
			}

			gotWarn := strings.Contains(logs.String(), "node hostname may cause problems")
			if gotWarn != tt.wantWarn {
				t.Fatalf("warning logged = %t, want %t; logs:\n%s", gotWarn, tt.wantWarn, logs.String())
			}
		})
	}
}
